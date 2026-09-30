package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/northfieldzz/tollgate/internal/config"
	"github.com/northfieldzz/tollgate/internal/domain/entity"
	"github.com/northfieldzz/tollgate/internal/domain/repository"
	"github.com/northfieldzz/tollgate/internal/infrastructure/ratelimit"
	"github.com/northfieldzz/tollgate/internal/usecase"
)

type fullMockKeyRepo struct {
	repository.KeyRepository
	keysByHash map[string]*entity.APIKey
	keysByID   map[string]*entity.APIKey
}

func newFullMockKeyRepo() *fullMockKeyRepo {
	return &fullMockKeyRepo{
		keysByHash: make(map[string]*entity.APIKey),
		keysByID:   make(map[string]*entity.APIKey),
	}
}

func (m *fullMockKeyRepo) PutKey(ctx context.Context, key *entity.APIKey) error {
	m.keysByHash[key.GetHash()] = key
	m.keysByID[key.KeyID] = key
	return nil
}

func (m *fullMockKeyRepo) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	return m.keysByHash[keyHash], nil
}

func (m *fullMockKeyRepo) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	return m.keysByID[keyID], nil
}

func (m *fullMockKeyRepo) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	var list []*entity.APIKey
	for _, k := range m.keysByHash {
		if k.TenantID == tenantID {
			list = append(list, k)
		}
	}
	return list, nil
}

func (m *fullMockKeyRepo) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	if k, ok := m.keysByHash[keyHash]; ok {
		k.Status = status
		k.IsActive = isActive
	}
	return nil
}

func (m *fullMockKeyRepo) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	k, ok := m.keysByHash[keyHash]
	if !ok {
		return nil, nil
	}
	if input.Name != nil {
		k.Name = *input.Name
	}
	return k, nil
}

func (m *fullMockKeyRepo) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	oldK, ok := m.keysByHash[params.OldKeyHash]
	if !ok {
		return nil, nil
	}
	oldK.Status = entity.StatusRotating
	newK := *oldK
	newK.PK = "KEY#" + params.NewKeyHash
	newK.KeyPrefix = params.NewKeyPrefix
	newK.Status = entity.StatusActive
	m.keysByHash[params.NewKeyHash] = &newK
	return &newK, nil
}

func (m *fullMockKeyRepo) DeleteKey(ctx context.Context, keyHash string) error {
	if k, ok := m.keysByHash[keyHash]; ok {
		delete(m.keysByID, k.KeyID)
		delete(m.keysByHash, keyHash)
	}
	return nil
}

func (m *fullMockKeyRepo) IncrementMonthlyUsage(ctx context.Context, keyHash string, month string, increment int64) (int64, error) {
	return increment, nil
}

func (m *fullMockKeyRepo) UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error {
	return nil
}

func (m *fullMockKeyRepo) Ping(ctx context.Context) error {
	return nil
}

func TestAdminAPI_FullOperations(t *testing.T) {
	repo := newFullMockKeyRepo()
	keyUsecase := usecase.NewKeyUsecase(repo)
	inMemLimiter := ratelimit.NewInMemoryRateLimiter(time.Minute)
	defer inMemLimiter.Stop()
	verifyUsecase := usecase.NewVerifyUsecase(repo, inMemLimiter)

	adminKey := "test-secret-123"
	cfg := &config.Config{
		AdminAPIKey: adminKey,
		OpenAPIPath: "/openapi.json",
		DocsPath:    "/docs",
	}
	router := NewRouter(cfg, keyUsecase, verifyUsecase, repo, nil)

	// 1. POST /v1/admin/keys (Create Key)
	createReqBody, _ := json.Marshal(entity.CreateKeyInput{
		Name:      "Test App Key",
		TenantID:  "tenant-alpha",
		ServiceID: "svc-alpha",
		Scopes:    []string{"llm:*"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/keys", bytes.NewReader(createReqBody))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("CreateKey expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp entity.CreateKeyOutput
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	keyID := createResp.KeyID
	rawKey := createResp.RawKey

	// 2. GET /v1/admin/keys?tenant_id=tenant-alpha (List Keys)
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/keys?tenant_id=tenant-alpha", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListKeys expected 200, got %d", rec.Code)
	}

	// 3. GET /v1/admin/keys/{key_id} (Get Key)
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/keys/"+keyID, nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetKey expected 200, got %d", rec.Code)
	}

	// 4. PATCH /v1/admin/keys/{key_id} (Update Key)
	newName := "Renamed Key"
	updateReqBody, _ := json.Marshal(entity.UpdateKeyInput{Name: &newName})
	req = httptest.NewRequest(http.MethodPatch, "/v1/admin/keys/"+keyID, bytes.NewReader(updateReqBody))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateKey expected 200, got %d", rec.Code)
	}

	// 5. POST /v1/admin/keys/{key_id}/suspend
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/keys/"+keyID+"/suspend", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("SuspendKey expected 200, got %d", rec.Code)
	}

	// 6. POST /v1/admin/keys/{key_id}/resume
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/keys/"+keyID+"/resume", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ResumeKey expected 200, got %d", rec.Code)
	}

	// 7. POST /v1/admin/keys/{key_id}/rotate
	rotateBody, _ := json.Marshal(entity.RotateKeyInput{GracePeriodHours: 24})
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/keys/"+keyID+"/rotate", bytes.NewReader(rotateBody))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("RotateKey expected 200, got %d", rec.Code)
	}

	// 8. POST /v1/admin/verify (Verify Key)
	verifyBody, _ := json.Marshal(entity.VerifyKeyInput{
		RawKey:        rawKey,
		RequiredScope: "llm:generate",
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/verify", bytes.NewReader(verifyBody))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("VerifyKey expected 200, got %d", rec.Code)
	}

	// 9. DELETE /v1/admin/keys/{key_id} (Delete Key)
	req = httptest.NewRequest(http.MethodDelete, "/v1/admin/keys/"+keyID, nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DeleteKey expected 200, got %d", rec.Code)
	}

	// 10. GET /openapi.json & /docs & /metrics
	for _, path := range []string{"/openapi.json", "/docs", "/metrics"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Path %s expected 200, got %d", path, rec.Code)
		}
	}

	// 11. Error Paths for Key Handlers
	t.Run("CreateKey validation error (missing service_id)", func(t *testing.T) {
		badBody, _ := json.Marshal(entity.CreateKeyInput{Name: "Missing Service"})
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/keys", bytes.NewReader(badBody))
		req.Header.Set("Authorization", "Bearer "+adminKey)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", rec.Code)
		}
	})

	t.Run("GetKey 404 not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/keys/non-existent-uuid", nil)
		req.Header.Set("Authorization", "Bearer "+adminKey)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", rec.Code)
		}
	})

	t.Run("UpdateKey 500 error on missing key", func(t *testing.T) {
		newName := "abc"
		body, _ := json.Marshal(entity.UpdateKeyInput{Name: &newName})
		req := httptest.NewRequest(http.MethodPatch, "/v1/admin/keys/non-existent-uuid", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminKey)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 on update non-existent, got %d", rec.Code)
		}
	})

	t.Run("SuspendKey 500 error on missing key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/keys/non-existent-uuid/suspend", nil)
		req.Header.Set("Authorization", "Bearer "+adminKey)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 on suspend non-existent, got %d", rec.Code)
		}
	})

	t.Run("ResumeKey 500 error on missing key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/keys/non-existent-uuid/resume", nil)
		req.Header.Set("Authorization", "Bearer "+adminKey)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 on resume non-existent, got %d", rec.Code)
		}
	})

	t.Run("RotateKey 500 error on missing key", func(t *testing.T) {
		rotateBody, _ := json.Marshal(entity.RotateKeyInput{GracePeriodHours: 24})
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/keys/non-existent-uuid/rotate", bytes.NewReader(rotateBody))
		req.Header.Set("Authorization", "Bearer "+adminKey)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 on rotate non-existent, got %d", rec.Code)
		}
	})

	t.Run("DeleteKey 500 error on missing key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/admin/keys/non-existent-uuid", nil)
		req.Header.Set("Authorization", "Bearer "+adminKey)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 on delete non-existent, got %d", rec.Code)
		}
	})
}
