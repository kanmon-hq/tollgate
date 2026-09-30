package http

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
)

type mockHealthRepo struct {
	pingErr error
}

func (m *mockHealthRepo) PutKey(ctx context.Context, key *entity.APIKey) error { return nil }
func (m *mockHealthRepo) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	return nil, nil
}
func (m *mockHealthRepo) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	return nil, nil
}
func (m *mockHealthRepo) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	return nil, nil
}
func (m *mockHealthRepo) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	return nil
}
func (m *mockHealthRepo) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	return nil, nil
}
func (m *mockHealthRepo) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	return nil, nil
}
func (m *mockHealthRepo) DeleteKey(ctx context.Context, keyHash string) error { return nil }
func (m *mockHealthRepo) IncrementMonthlyUsage(ctx context.Context, keyHash string, month string, increment int64) (int64, error) {
	return 0, nil
}
func (m *mockHealthRepo) UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error {
	return nil
}
func (m *mockHealthRepo) Ping(ctx context.Context) error { return m.pingErr }

func TestHealthHandler_HealthCheck(t *testing.T) {
	_, api := humatest.New(t)
	repo := &mockHealthRepo{}
	RegisterHealthHandler(api, repo)

	t.Run("OK - /healthz", func(t *testing.T) {
		repo.pingErr = nil
		resp := api.Get("/healthz")
		if resp.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.Code)
		}
	})

	t.Run("ServiceUnavailable - /healthz", func(t *testing.T) {
		repo.pingErr = errors.New("db down")
		resp := api.Get("/healthz")
		if resp.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503 Service Unavailable, got %d", resp.Code)
		}
	})

	t.Run("Old /health returns 404", func(t *testing.T) {
		resp := api.Get("/health")
		if resp.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", resp.Code)
		}
	})
}

func TestHealthHandler_Liveness(t *testing.T) {
	_, api := humatest.New(t)
	repo := &mockHealthRepo{}
	RegisterHealthHandler(api, repo)

	// Liveness is not dependent on repo, but we can set it to error to make sure it doesn't fail
	repo.pingErr = errors.New("db down")

	t.Run("/livez", func(t *testing.T) {
		resp := api.Get("/livez")
		if resp.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.Code)
		}
	})

	t.Run("Old /health/live returns 404", func(t *testing.T) {
		resp := api.Get("/health/live")
		if resp.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", resp.Code)
		}
	})
}

func TestHealthHandler_Readiness(t *testing.T) {
	_, api := humatest.New(t)
	repo := &mockHealthRepo{}
	RegisterHealthHandler(api, repo)

	t.Run("OK - /readyz", func(t *testing.T) {
		repo.pingErr = nil
		resp := api.Get("/readyz")
		if resp.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.Code)
		}
	})

	t.Run("ServiceUnavailable - /readyz", func(t *testing.T) {
		repo.pingErr = errors.New("db down")
		resp := api.Get("/readyz")
		if resp.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503 Service Unavailable, got %d", resp.Code)
		}
	})

	t.Run("Old /health/ready returns 404", func(t *testing.T) {
		resp := api.Get("/health/ready")
		if resp.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", resp.Code)
		}
	})
}

func TestMetricsHandler(t *testing.T) {
	_, api := humatest.New(t)
	RegisterMetricsHandler(api)

	resp := api.Get("/metrics")
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200 OK from /metrics, got %d", resp.Code)
	}
}
