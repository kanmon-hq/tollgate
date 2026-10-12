package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kanmon-hq/tollgate/internal/config"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/ratelimit"
	"github.com/kanmon-hq/tollgate/internal/usecase"
)

func TestScopeHandler_ListScopes(t *testing.T) {
	repo := newFullMockKeyRepo()
	keyUsecase := usecase.NewKeyUsecase(repo)
	inMemLimiter := ratelimit.NewInMemoryRateLimiter(time.Minute)
	defer inMemLimiter.Stop()
	verifyUsecase := usecase.NewVerifyUsecase(repo, inMemLimiter)

	adminKey := "test-secret-123"
	cfg := &config.Config{
		AdminAPIKey: adminKey,
		Routes: []*config.RouteConfig{
			{
				Prefix: "/billing",
				Target: "http://billing:8080",
				Scope:  "billing:*",
				PathRules: []*config.PathRule{
					{
						Path:        "/approve",
						Method:      "POST",
						Scope:       "billing:admin",
						Description: "承認エンドポイント",
					},
					{
						Path:        "/invoices/*",
						Method:      "GET",
						Scope:       "billing:read",
						Description: "請求書閲覧",
					},
				},
			},
			{
				Prefix: "/ai",
				Target: "http://ai:8080",
				Scope:  "ai:*",
			},
		},
	}

	router := NewRouter(cfg, keyUsecase, verifyUsecase, repo, nil)

	// 1. 認証なしの場合 -> 401 Unauthorized
	reqUnauthorized := httptest.NewRequest(http.MethodGet, "/v1/admin/scopes", nil)
	recUnauthorized := httptest.NewRecorder()
	router.ServeHTTP(recUnauthorized, reqUnauthorized)
	if recUnauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized, got %d", recUnauthorized.Code)
	}

	// 2. 正常系: GET /v1/admin/scopes
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/scopes", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Scopes []ScopeItem `json:"scopes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// 収集されたスコープをマップ化
	scopeMap := make(map[string]ScopeItem)
	for _, item := range res.Scopes {
		scopeMap[item.Scope] = item
	}

	expectedScopes := []string{"ai:*", "billing:*", "billing:admin", "billing:read"}
	for _, expected := range expectedScopes {
		if _, ok := scopeMap[expected]; !ok {
			t.Errorf("expected scope %q to be in response, but was missing", expected)
		}
	}

	// PathRule のプロパティ検証
	adminScope, ok := scopeMap["billing:admin"]
	if !ok {
		t.Fatalf("billing:admin not found")
	}
	if adminScope.Method != "POST" {
		t.Errorf("expected method POST, got %q", adminScope.Method)
	}
	if adminScope.Path != "/approve" {
		t.Errorf("expected path /approve, got %q", adminScope.Path)
	}
	if adminScope.Source != "path_rule" {
		t.Errorf("expected source path_rule, got %q", adminScope.Source)
	}

	// ルートスコープの検証
	billingScope, ok := scopeMap["billing:*"]
	if !ok {
		t.Fatalf("billing:* not found")
	}
	if billingScope.Source != "route" {
		t.Errorf("expected source route, got %q", billingScope.Source)
	}
}
