package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kanmon-hq/tollgate/internal/config"
	deliveryHttp "github.com/kanmon-hq/tollgate/internal/delivery/http"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/ratelimit"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/sqlrepo"
	"github.com/kanmon-hq/tollgate/internal/usecase"
)

// TestContract_SecurityAndMultiTenant (要件 C-5)
// 3つのOSS結合シナリオにおけるセキュリティ境界、ヘッダー信頼、共有シークレット不一致、ローテーション、解約を網羅検証する契約テスト。
func TestContract_SecurityAndMultiTenant(t *testing.T) {
	ctx := context.Background()

	// 1. インメモリ SQLite リポジトリと RateLimiter の初期化 (完全自己完結)
	repo, err := sqlrepo.NewSQLiteRepository(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to init sqlite repo: %v", err)
	}

	limiter := ratelimit.NewInMemoryRateLimiter(time.Minute)
	defer limiter.Stop()

	keyUsecase := usecase.NewKeyUsecase(repo)
	verifyUsecase := usecase.NewVerifyUsecase(repo, limiter)

	// 2. モックアップストリーム（宛先マイクロサービス）
	var lastReceivedHeader http.Header
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReceivedHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"upstream":"success"}`))
	}))
	defer mockUpstream.Close()

	// 3. プロキシハンドラー & ルーター構築
	cfg := &config.Config{
		Port:        "8000",
		AdminAPIKey: "master-admin-secret",
		Routes: []*deliveryHttp.RouteConfig{
			{
				Prefix:      "/service",
				Target:      mockUpstream.URL,
				StripPrefix: true,
			},
		},
	}

	proxyHandler, err := deliveryHttp.NewMultiTargetProxy(cfg.Routes, "", verifyUsecase)
	if err != nil {
		t.Fatalf("failed to create proxy handler: %v", err)
	}

	router := deliveryHttp.NewRouter(cfg, keyUsecase, verifyUsecase, repo, proxyHandler)

	// ─── シナリオ 1: サービスキー (BFF間) のユーザーコンテキスト透過 ───
	t.Run("Scenario 1: Service Key allows user context pass-through (T-2, C-5)", func(t *testing.T) {
		// サービスキー発行 (TenantID なし)
		svcKeyOut, err := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{
			Name:      "BFF Service Key",
			ServiceID: "bff-service",
		})
		if err != nil {
			t.Fatalf("failed to create service key: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/service/orders", nil)
		req.Header.Set("Authorization", "Bearer "+svcKeyOut.RawKey)
		req.Header.Set("X-Tenant-ID", "tenant-client-456")
		req.Header.Set("X-User-Id", "user-uuid-123")
		req.Header.Set("X-User-Role", "admin")
		req.Header.Set("X-User-Context", "signed-token-xyz")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}

		// サービスキーなのでユーザーコンテキストヘッダーがアップストリームへ透過されること
		if got := lastReceivedHeader.Get("X-User-Id"); got != "user-uuid-123" {
			t.Errorf("expected X-User-Id to be forwarded, got %q", got)
		}
		if got := lastReceivedHeader.Get("X-User-Context"); got != "signed-token-xyz" {
			t.Errorf("expected X-User-Context to be forwarded, got %q", got)
		}
		if got := lastReceivedHeader.Get("X-Service-Id"); got != "bff-service" {
			t.Errorf("expected X-Service-Id 'bff-service', got %q", got)
		}
	})

	// ─── シナリオ 2: テナントキーのユーザーコンテキスト除去 (スプーフィング防止) ───
	t.Run("Scenario 2: Tenant Key strips user context to prevent spoofing (T-2, C-5)", func(t *testing.T) {
		// テナントキー発行 (TenantID あり)
		tenantKeyOut, err := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{
			Name:      "Tenant External Key",
			TenantID:  "tenant-alpha",
			ServiceID: "client-portal",
		})
		if err != nil {
			t.Fatalf("failed to create tenant key: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/service/data", nil)
		req.Header.Set("Authorization", "Bearer "+tenantKeyOut.RawKey)
		req.Header.Set("X-User-Id", "forged-user-id")
		req.Header.Set("X-User-Role", "super-admin")
		req.Header.Set("X-User-Context", "forged-token")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}

		// テナントキーなのでユーザーコンテキストヘッダーが除去されていること
		if got := lastReceivedHeader.Get("X-User-Id"); got != "" {
			t.Errorf("expected X-User-Id to be stripped, got %q", got)
		}
		if got := lastReceivedHeader.Get("X-User-Context"); got != "" {
			t.Errorf("expected X-User-Context to be stripped, got %q", got)
		}
		// X-Tenant-Id はキーの属性値が付与されていること
		if got := lastReceivedHeader.Get("X-Tenant-Id"); got != "tenant-alpha" {
			t.Errorf("expected X-Tenant-Id 'tenant-alpha', got %q", got)
		}
	})

	// ─── シナリオ 3: テナント越境・ヘッダーコンフリクトの即時拒絶 (Fail-Fast) ───
	t.Run("Scenario 3: Tenant header conflict rejected with 403 Fail-Fast (C-5)", func(t *testing.T) {
		tenantKeyOut, _ := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{
			Name:      "Tenant Alpha Key",
			TenantID:  "tenant-alpha",
			ServiceID: "data-service",
		})

		// クライアントがキーの属性と異なる X-Tenant-Id: tenant-beta を詐称して送信
		req := httptest.NewRequest(http.MethodGet, "/service/data", nil)
		req.Header.Set("Authorization", "Bearer "+tenantKeyOut.RawKey)
		req.Header.Set("X-Tenant-Id", "tenant-beta")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden on tenant conflict, got %d", rec.Code)
		}
	})

	// ─── シナリオ 4: 共有シークレット（APIキー / Adminシークレット）不一致の即時拒絶 ───
	t.Run("Scenario 4: Invalid API key or Admin secret rejected with 401 Unauthorized (C-5)", func(t *testing.T) {
		// 1. プロキシ宛ての不正な API キー
		req := httptest.NewRequest(http.MethodGet, "/service/data", nil)
		req.Header.Set("Authorization", "Bearer tlge-live-invalid-key-token-0000000000")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for invalid proxy key, got %d", rec.Code)
		}

		// 2. 管理 API 宛ての不正な Admin シークレット
		adminReq := httptest.NewRequest(http.MethodGet, "/v1/admin/keys", nil)
		adminReq.Header.Set("Authorization", "Bearer wrong-admin-secret")
		adminRec := httptest.NewRecorder()
		router.ServeHTTP(adminRec, adminReq)
		if adminRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for wrong admin secret, got %d", adminRec.Code)
		}
	})

	// ─── シナリオ 5: ゼロダウンタイム・キーローテーション切り替え (T-1, C-2, C-5) ───
	t.Run("Scenario 5: Zero-downtime key rotation dual-validity (T-1, C-5)", func(t *testing.T) {
		origKey, _ := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{
			Name:      "Rotating Service",
			TenantID:  "tenant-rotate",
			ServiceID: "svc-rotate",
		})

		// ローテーション実行 (猶予期間: 1時間)
		rotOut, err := keyUsecase.RotateKey(ctx, origKey.KeyID, entity.RotateKeyInput{
			GracePeriodHours: 1,
		})
		if err != nil {
			t.Fatalf("failed to rotate key: %v", err)
		}

		// 1. 旧キーでのリクエスト -> 猶予期間中のため 200 OK
		reqOld := httptest.NewRequest(http.MethodGet, "/service/ping", nil)
		reqOld.Header.Set("Authorization", "Bearer "+origKey.RawKey)
		recOld := httptest.NewRecorder()
		router.ServeHTTP(recOld, reqOld)
		if recOld.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for old key in grace period, got %d", recOld.Code)
		}

		// 2. 新キーでのリクエスト -> 即時 200 OK
		reqNew := httptest.NewRequest(http.MethodGet, "/service/ping", nil)
		reqNew.Header.Set("Authorization", "Bearer "+rotOut.NewRawKey)
		recNew := httptest.NewRecorder()
		router.ServeHTTP(recNew, reqNew)
		if recNew.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for new key, got %d", recNew.Code)
		}
	})

	// ─── シナリオ 6: テナント解約時の一括失効と他テナントの完全隔離 (T-4, C-4, C-5) ───
	t.Run("Scenario 6: Tenant revocation revokes all target keys without affecting other tenants (T-4, C-4, C-5)", func(t *testing.T) {
		kTarget1, _ := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{Name: "T1-K1", TenantID: "tenant-cancel", ServiceID: "billing"})
		kTarget2, _ := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{Name: "T1-K2", TenantID: "tenant-cancel", ServiceID: "orders"})
		kOther, _ := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{Name: "Other", TenantID: "tenant-stay", ServiceID: "billing"})

		// 管理API経由で一括失効
		revokeReq := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/tenant-cancel/revoke", nil)
		revokeReq.Header.Set("Authorization", "Bearer "+cfg.AdminAPIKey)
		revokeRec := httptest.NewRecorder()
		router.ServeHTTP(revokeRec, revokeReq)

		if revokeRec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on revoke, got %d", revokeRec.Code)
		}

		// 解約テナントのキー1: 403 Forbidden (失効キー)
		req1 := httptest.NewRequest(http.MethodGet, "/service/test", nil)
		req1.Header.Set("Authorization", "Bearer "+kTarget1.RawKey)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)
		if rec1.Code != http.StatusForbidden {
			t.Errorf("expected 403 for kTarget1, got %d", rec1.Code)
		}

		// 解約テナントのキー2: 403 Forbidden (失効キー)
		req2 := httptest.NewRequest(http.MethodGet, "/service/test", nil)
		req2.Header.Set("Authorization", "Bearer "+kTarget2.RawKey)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusForbidden {
			t.Errorf("expected 403 for kTarget2, got %d", rec2.Code)
		}

		// 別テナントのキー: 影響を受けず 200 OK
		reqStay := httptest.NewRequest(http.MethodGet, "/service/test", nil)
		reqStay.Header.Set("Authorization", "Bearer "+kOther.RawKey)
		recStay := httptest.NewRecorder()
		router.ServeHTTP(recStay, reqStay)
		if recStay.Code != http.StatusOK {
			t.Errorf("expected 200 OK for other tenant key, got %d", recStay.Code)
		}
	})

	// ─── シナリオ 7: X-Request-Id の透過と自動生成 (C-3, C-5) ───
	t.Run("Scenario 7: Request ID tracing pass-through and generation (C-3, C-5)", func(t *testing.T) {
		svcKey, _ := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{Name: "Trace Key", TenantID: "tenant-trace", ServiceID: "trace-svc"})

		// 1. クライアントが X-Request-Id を指定した場合 -> アップストリーム＆レスポンスに透過
		req := httptest.NewRequest(http.MethodGet, "/service/trace", nil)
		req.Header.Set("Authorization", "Bearer "+svcKey.RawKey)
		req.Header.Set("X-Request-Id", "client-trace-12345")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if got := lastReceivedHeader.Get("X-Request-Id"); got != "client-trace-12345" {
			t.Errorf("expected upstream X-Request-Id 'client-trace-12345', got %q", got)
		}
		if got := rec.Header().Get("X-Request-Id"); got != "client-trace-12345" {
			t.Errorf("expected response X-Request-Id 'client-trace-12345', got %q", got)
		}

		// 2. クライアントが未指定の場合 -> 自動生成されてアップストリーム＆レスポンスに同一値が付与
		reqAuto := httptest.NewRequest(http.MethodGet, "/service/trace", nil)
		reqAuto.Header.Set("Authorization", "Bearer "+svcKey.RawKey)
		recAuto := httptest.NewRecorder()
		router.ServeHTTP(recAuto, reqAuto)

		autoID := recAuto.Header().Get("X-Request-Id")
		if autoID == "" {
			t.Fatalf("expected auto-generated X-Request-Id")
		}
		if got := lastReceivedHeader.Get("X-Request-Id"); got != autoID {
			t.Errorf("expected upstream to receive same auto-generated ID %q, got %q", autoID, got)
		}
	})
}
