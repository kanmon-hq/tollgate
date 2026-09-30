package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/ratelimit"
	"github.com/kanmon-hq/tollgate/internal/usecase"
)

type mockKeyRepo struct {
	keys map[string]*entity.APIKey
}

func (m *mockKeyRepo) PutKey(ctx context.Context, key *entity.APIKey) error { return nil }
func (m *mockKeyRepo) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	if k, ok := m.keys[keyHash]; ok {
		return k, nil
	}
	return nil, nil
}
func (m *mockKeyRepo) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	return nil, nil
}
func (m *mockKeyRepo) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	return nil, nil
}
func (m *mockKeyRepo) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	return nil
}
func (m *mockKeyRepo) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	return nil, nil
}
func (m *mockKeyRepo) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	return nil, nil
}
func (m *mockKeyRepo) DeleteKey(ctx context.Context, keyHash string) error { return nil }
func (m *mockKeyRepo) IncrementMonthlyUsage(ctx context.Context, keyHash string, month string, increment int64) (int64, error) {
	return 1, nil
}
func (m *mockKeyRepo) UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error {
	return nil
}
func (m *mockKeyRepo) Ping(ctx context.Context) error { return nil }

func setupTestMultiProxy(t *testing.T, routes []*RouteConfig, defaultBackend http.HandlerFunc) (*MultiTargetProxy, *mockKeyRepo) {
	repo := &mockKeyRepo{keys: make(map[string]*entity.APIKey)}
	limiter := ratelimit.NewInMemoryRateLimiter(time.Minute)
	vUsecase := usecase.NewVerifyUsecase(repo, limiter)

	var defURL string
	if defaultBackend != nil {
		ts := httptest.NewServer(defaultBackend)
		t.Cleanup(ts.Close)
		defURL = ts.URL
	}

	proxy, err := NewMultiTargetProxy(routes, defURL, vUsecase)
	if err != nil {
		t.Fatalf("failed to create MultiTargetProxy: %v", err)
	}

	return proxy, repo
}

func TestMultiTargetProxy_RoutingAndStripPrefix(t *testing.T) {
	var llmReceivedPath, mcpReceivedPath, aiReceivedPath string

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmReceivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(llmServer.Close)

	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpReceivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mcpServer.Close)

	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aiReceivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(aiServer.Close)

	routes := []*RouteConfig{
		{Prefix: "/llm", Target: llmServer.URL, Scope: "llm:*", StripPrefix: true},
		{Prefix: "/mcp", Target: mcpServer.URL, Scope: "mcp:*", StripPrefix: true},
		{Prefix: "/ai", Target: aiServer.URL, Scope: "ai:*", StripPrefix: true},
	}

	proxy, repo := setupTestMultiProxy(t, routes, nil)

	// 万能キー (全スコープ許可)
	rawKey := "tlge-live-allpowerfull1234567890"
	repo.keys[usecase.HashKey(rawKey)] = &entity.APIKey{
		KeyID:        "key-admin",
		TenantID:     "tenant-all",
		IsActive:     true,
		Status:       entity.StatusActive,
		Scopes:       []string{"*"},
		RateLimitRPM: 1000,
	}

	// 1. /llm/v1/chat/completions -> llmServer に /v1/chat/completions として届く
	req1 := httptest.NewRequest("POST", "/llm/v1/chat/completions", nil)
	req1.Header.Set("Authorization", "Bearer "+rawKey)
	rec1 := httptest.NewRecorder()
	proxy.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("/llm request failed with status: %d", rec1.Code)
	}
	if llmReceivedPath != "/v1/chat/completions" {
		t.Errorf("expected stripped path '/v1/chat/completions', got: %s", llmReceivedPath)
	}

	// 2. /mcp/tools/list -> mcpServer に /tools/list として届く
	req2 := httptest.NewRequest("GET", "/mcp/tools/list", nil)
	req2.Header.Set("X-API-Key", rawKey)
	rec2 := httptest.NewRecorder()
	proxy.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("/mcp request failed with status: %d", rec2.Code)
	}
	if mcpReceivedPath != "/tools/list" {
		t.Errorf("expected stripped path '/tools/list', got: %s", mcpReceivedPath)
	}

	// 3. /ai/workflows/run -> aiServer に /workflows/run として届く
	req3 := httptest.NewRequest("POST", "/ai/workflows/run", nil)
	req3.Header.Set("Authorization", "Bearer "+rawKey)
	rec3 := httptest.NewRecorder()
	proxy.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("/ai request failed with status: %d", rec3.Code)
	}
	if aiReceivedPath != "/workflows/run" {
		t.Errorf("expected stripped path '/workflows/run', got: %s", aiReceivedPath)
	}
}

func TestMultiTargetProxy_ScopeEnforcement(t *testing.T) {
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mcpServer.Close)

	routes := []*RouteConfig{
		{Prefix: "/mcp", Target: mcpServer.URL, Scope: "mcp:*", StripPrefix: true},
	}

	proxy, repo := setupTestMultiProxy(t, routes, nil)

	// llm:* のみを持つキー (mcp:* を持たない)
	llmOnlyKey := "tlge-live-llmonly1234567890abcdef"
	repo.keys[usecase.HashKey(llmOnlyKey)] = &entity.APIKey{
		KeyID:        "key-llm",
		TenantID:     "tenant-llm",
		IsActive:     true,
		Status:       entity.StatusActive,
		Scopes:       []string{"llm:*"},
		RateLimitRPM: 100,
	}

	// /mcp を叩く -> 403 Forbidden になるべき
	req := httptest.NewRequest("GET", "/mcp/tools", nil)
	req.Header.Set("Authorization", "Bearer "+llmOnlyKey)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status %d (Forbidden) for scope mismatch, got: %d", http.StatusForbidden, rec.Code)
	}
}

func TestMultiTargetProxy_NotFoundWhenNoRouteMatches(t *testing.T) {
	routes := []*RouteConfig{
		{Prefix: "/llm", Target: "http://dummy", Scope: "llm:*"},
	}
	proxy, _ := setupTestMultiProxy(t, routes, nil)

	req := httptest.NewRequest("GET", "/unknown/service", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d for unmapped route, got: %d", http.StatusNotFound, rec.Code)
	}
}

func TestMultiTargetProxy_matchRoute(t *testing.T) {
	routes := []*RouteConfig{
		{Prefix: "/api", Target: "http://dummy-api"},
		{Prefix: "/api/v1", Target: "http://dummy-api-v1"},
		{Prefix: "/auth", Target: "http://dummy-auth"},
	}

	proxy, _ := setupTestMultiProxy(t, routes, nil)

	tests := []struct {
		name           string
		path           string
		expectedPrefix string // 期待される Prefix (マッチしない場合は空文字列)
	}{
		{
			name:           "Exact match",
			path:           "/api",
			expectedPrefix: "/api",
		},
		{
			name:           "Prefix match with slash",
			path:           "/api/users",
			expectedPrefix: "/api",
		},
		{
			name:           "Longest prefix match",
			path:           "/api/v1/users",
			expectedPrefix: "/api/v1",
		},
		{
			name:           "No match for overlapping name without slash",
			path:           "/api-docs",
			expectedPrefix: "", // マッチしない
		},
		{
			name:           "No match for completely unknown paths",
			path:           "/unknown/service",
			expectedPrefix: "", // マッチしない
		},
		{
			name:           "Exact match for another route",
			path:           "/auth",
			expectedPrefix: "/auth",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route := proxy.matchRoute(tc.path)
			if tc.expectedPrefix == "" {
				if route != nil {
					t.Errorf("expected no match, but got route with prefix %q", route.config.Prefix)
				}
			} else {
				if route == nil {
					t.Errorf("expected route with prefix %q, but got nil", tc.expectedPrefix)
				} else if route.config.Prefix != tc.expectedPrefix {
					t.Errorf("expected route with prefix %q, but got %q", tc.expectedPrefix, route.config.Prefix)
				}
			}
		})
	}
}

func TestMultiTargetProxy_TenantResolutionAndConflictValidation(t *testing.T) {
	var receivedTenantID, receivedServiceID string

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTenantID = r.Header.Get("X-Tenant-ID")
		receivedServiceID = r.Header.Get("X-Service-ID")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backendServer.Close)

	routes := []*RouteConfig{
		{Prefix: "/llm", Target: backendServer.URL, Scope: "llm:*"},
	}
	proxy, repo := setupTestMultiProxy(t, routes, nil)

	// テナント固定キー
	tenantKey := "tlge-live-tenantkey1234567890abcdef"
	repo.keys[usecase.HashKey(tenantKey)] = &entity.APIKey{
		KeyID:        "key-tenant-1",
		KeyPrefix:    "tlge-live-tena",
		TenantID:     "tenant-corp-a",
		ServiceID:    "service-corp",
		IsActive:     true,
		Status:       entity.StatusActive,
		Scopes:       []string{"llm:*"},
		RateLimitRPM: 100,
	}

	// サービスキー (TenantID なし)
	serviceKey := "tlge-live-servicekey1234567890abcdef"
	repo.keys[usecase.HashKey(serviceKey)] = &entity.APIKey{
		KeyID:        "key-service-1",
		KeyPrefix:    "tlge-live-serv",
		TenantID:     "",
		ServiceID:    "multi-tenant-saas",
		IsActive:     true,
		Status:       entity.StatusActive,
		Scopes:       []string{"llm:*"},
		RateLimitRPM: 100,
	}

	t.Run("TenantKey without X-Tenant-ID header succeeds and injects key's TenantID", func(t *testing.T) {
		receivedTenantID = ""
		receivedServiceID = ""

		req := httptest.NewRequest("POST", "/llm/v1/chat", nil)
		req.Header.Set("Authorization", "Bearer "+tenantKey)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		if receivedTenantID != "tenant-corp-a" {
			t.Errorf("expected TenantID 'tenant-corp-a', got %q", receivedTenantID)
		}
		if receivedServiceID != "service-corp" {
			t.Errorf("expected ServiceID 'service-corp', got %q", receivedServiceID)
		}
	})

	t.Run("TenantKey with matching X-Tenant-ID header succeeds", func(t *testing.T) {
		receivedTenantID = ""

		req := httptest.NewRequest("POST", "/llm/v1/chat", nil)
		req.Header.Set("Authorization", "Bearer "+tenantKey)
		req.Header.Set("X-Tenant-ID", "tenant-corp-a")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if receivedTenantID != "tenant-corp-a" {
			t.Errorf("expected TenantID 'tenant-corp-a', got %q", receivedTenantID)
		}
	})

	t.Run("TenantKey with conflicting X-Tenant-ID header returns 403 Forbidden", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/llm/v1/chat", nil)
		req.Header.Set("Authorization", "Bearer "+tenantKey)
		req.Header.Set("X-Tenant-ID", "tenant-corp-b")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403 Forbidden, got %d (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("ServiceKey with X-Tenant-ID header forwards dynamic tenant and injects ServiceID", func(t *testing.T) {
		receivedTenantID = ""
		receivedServiceID = ""

		req := httptest.NewRequest("POST", "/llm/v1/chat", nil)
		req.Header.Set("Authorization", "Bearer "+serviceKey)
		req.Header.Set("X-Tenant-ID", "customer-dynamic-999")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		if receivedTenantID != "customer-dynamic-999" {
			t.Errorf("expected TenantID 'customer-dynamic-999', got %q", receivedTenantID)
		}
		if receivedServiceID != "multi-tenant-saas" {
			t.Errorf("expected ServiceID 'multi-tenant-saas', got %q", receivedServiceID)
		}
	})

	t.Run("ServiceKey without X-Tenant-ID header returns 400 Bad Request", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/llm/v1/chat", nil)
		req.Header.Set("Authorization", "Bearer "+serviceKey)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 Bad Request, got %d (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("Conflicting X-Service-ID header returns 403 Forbidden", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/llm/v1/chat", nil)
		req.Header.Set("Authorization", "Bearer "+tenantKey)
		req.Header.Set("X-Service-ID", "different-service")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403 Forbidden, got %d (body: %s)", rec.Code, rec.Body.String())
		}
	})
}
