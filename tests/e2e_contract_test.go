package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestE2E_ContractVerification
// Docker Compose やステージング環境等の実稼働コンテナを対象に、HTTP経由で契約を検証するE2E契約テスト。
// 実行条件: 環境変数 E2E_BASE_URL (例: "http://localhost:8080") が設定されている場合のみ実行。
func TestE2E_ContractVerification(t *testing.T) {
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Skip("Skipping E2E contract test: E2E_BASE_URL is not set (e.g. export E2E_BASE_URL=http://localhost:8080)")
	}

	adminKey := os.Getenv("E2E_ADMIN_KEY")
	if adminKey == "" {
		adminKey = os.Getenv("ADMIN_API_KEY")
		if adminKey == "" {
			adminKey = "dev-secret-key-change-in-production"
		}
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// 1. ヘルスチェック契約 (/livez, /readyz)
	t.Run("E2E 1: Healthcheck Endpoints", func(t *testing.T) {
		resLive, err := client.Get(baseURL + "/livez")
		if err != nil {
			t.Fatalf("failed to call /livez: %v", err)
		}
		defer resLive.Body.Close()
		if resLive.StatusCode != http.StatusOK {
			t.Errorf("expected /livez status 200, got %d", resLive.StatusCode)
		}

		resReady, err := client.Get(baseURL + "/readyz")
		if err != nil {
			t.Fatalf("failed to call /readyz: %v", err)
		}
		defer resReady.Body.Close()
		if resReady.StatusCode != http.StatusOK {
			t.Errorf("expected /readyz status 200, got %d", resReady.StatusCode)
		}
	})

	// 2. 共有シークレット不一致の即時拒絶 (401)
	t.Run("E2E 2: Shared Secret Mismatch Rejected", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/admin/keys", nil)
		req.Header.Set("Authorization", "Bearer invalid-admin-secret-999")
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for invalid admin key, got %d", res.StatusCode)
		}
	})

	// 3. API キー発行 & プロキシ契約 & テナント隔離
	t.Run("E2E 3: Proxy & Tenant Contract Lifecycle", func(t *testing.T) {
		tenantID := fmt.Sprintf("e2e-tenant-%d", time.Now().UnixNano())
		serviceID := "billing-service"

		// 3.1 テナントキーの発行
		createPayload := map[string]interface{}{
			"name":           "E2E Integration Key",
			"tenant_id":      tenantID,
			"service_id":     serviceID,
			"rate_limit_rpm": 600,
		}
		bodyBytes, _ := json.Marshal(createPayload)

		reqCreate, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/admin/keys", bytes.NewReader(bodyBytes))
		reqCreate.Header.Set("Authorization", "Bearer "+adminKey)
		reqCreate.Header.Set("Content-Type", "application/json")
		resCreate, err := client.Do(reqCreate)
		if err != nil {
			t.Fatalf("failed to create key via admin API: %v", err)
		}
		defer resCreate.Body.Close()
		if resCreate.StatusCode != http.StatusCreated && resCreate.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resCreate.Body)
			t.Fatalf("failed to create key, status: %d, body: %s", resCreate.StatusCode, string(body))
		}

		var createdKey struct {
			KeyID  string `json:"key_id"`
			RawKey string `json:"raw_key"`
		}
		if err := json.NewDecoder(resCreate.Body).Decode(&createdKey); err != nil {
			t.Fatalf("failed to decode created key: %v", err)
		}

		// 3.2 テナントコンフリクトの拒絶 (403 Fail-Fast)
		reqConflict, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/items", nil)
		reqConflict.Header.Set("Authorization", "Bearer "+createdKey.RawKey)
		reqConflict.Header.Set("X-Tenant-Id", "forged-different-tenant")
		resConflict, err := client.Do(reqConflict)
		if err != nil {
			t.Fatalf("failed to call proxy: %v", err)
		}
		defer resConflict.Body.Close()
		if resConflict.StatusCode != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden on tenant conflict, got %d", resConflict.StatusCode)
		}

		// 3.3 テナント解約時の一括失効 API
		revokeURL := fmt.Sprintf("%s/v1/admin/tenants/%s/revoke", baseURL, tenantID)
		reqRevoke, _ := http.NewRequest(http.MethodPost, revokeURL, nil)
		reqRevoke.Header.Set("Authorization", "Bearer "+adminKey)
		resRevoke, err := client.Do(reqRevoke)
		if err != nil {
			t.Fatalf("failed to call tenant revoke API: %v", err)
		}
		defer resRevoke.Body.Close()
		if resRevoke.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK on revoke, got %d", resRevoke.StatusCode)
		}

		// 3.4 失効後はプロキシリクエストが即座に 403 で拒絶されること
		reqAfterRevoke, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/items", nil)
		reqAfterRevoke.Header.Set("Authorization", "Bearer "+createdKey.RawKey)
		resAfterRevoke, err := client.Do(reqAfterRevoke)
		if err != nil {
			t.Fatalf("failed to call proxy after revoke: %v", err)
		}
		defer resAfterRevoke.Body.Close()
		if resAfterRevoke.StatusCode != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden after tenant revoke, got %d", resAfterRevoke.StatusCode)
		}
	})
}
