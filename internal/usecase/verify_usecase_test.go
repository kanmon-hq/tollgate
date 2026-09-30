package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
)

// MockRateLimiter implements repository.RateLimiter for testing.
type MockRateLimiter struct {
	AllowFunc func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error)
}

func (m *MockRateLimiter) Allow(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
	if m.AllowFunc != nil {
		return m.AllowFunc(ctx, id, limitRPM)
	}
	// デフォルト: 常に許可
	return true, limitRPM, time.Minute, nil
}

// MockKeyRepository implements repository.KeyRepository for testing.
type MockKeyRepository struct {
	PutKeyFunc                func(ctx context.Context, key *entity.APIKey) error
	GetKeyByHashFunc          func(ctx context.Context, keyHash string) (*entity.APIKey, error)
	GetKeyByIDFunc            func(ctx context.Context, keyID string) (*entity.APIKey, error)
	ListKeysByTenantFunc      func(ctx context.Context, tenantID string) ([]*entity.APIKey, error)
	UpdateKeyStatusFunc       func(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error
	UpdateKeySettingsFunc     func(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error)
	RotateKeyFunc             func(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error)
	DeleteKeyFunc             func(ctx context.Context, keyHash string) error
	IncrementMonthlyUsageFunc func(ctx context.Context, keyHash string, month string, increment int64) (int64, error)
	UpdateLastUsedAtFunc      func(ctx context.Context, keyHash string, lastUsed time.Time) error
	PingFunc                  func(ctx context.Context) error
}

func (m *MockKeyRepository) PutKey(ctx context.Context, key *entity.APIKey) error {
	if m.PutKeyFunc != nil {
		return m.PutKeyFunc(ctx, key)
	}
	return nil
}

func (m *MockKeyRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	if m.GetKeyByHashFunc != nil {
		return m.GetKeyByHashFunc(ctx, keyHash)
	}
	return nil, nil
}

func (m *MockKeyRepository) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	if m.GetKeyByIDFunc != nil {
		return m.GetKeyByIDFunc(ctx, keyID)
	}
	return nil, nil
}

func (m *MockKeyRepository) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	if m.ListKeysByTenantFunc != nil {
		return m.ListKeysByTenantFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *MockKeyRepository) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	if m.UpdateKeyStatusFunc != nil {
		return m.UpdateKeyStatusFunc(ctx, keyHash, status, isActive)
	}
	return nil
}

func (m *MockKeyRepository) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	if m.UpdateKeySettingsFunc != nil {
		return m.UpdateKeySettingsFunc(ctx, keyHash, input)
	}
	return nil, nil
}

func (m *MockKeyRepository) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	if m.RotateKeyFunc != nil {
		return m.RotateKeyFunc(ctx, params)
	}
	return nil, nil
}

func (m *MockKeyRepository) DeleteKey(ctx context.Context, keyHash string) error {
	if m.DeleteKeyFunc != nil {
		return m.DeleteKeyFunc(ctx, keyHash)
	}
	return nil
}

func (m *MockKeyRepository) IncrementMonthlyUsage(ctx context.Context, keyHash string, month string, increment int64) (int64, error) {
	if m.IncrementMonthlyUsageFunc != nil {
		return m.IncrementMonthlyUsageFunc(ctx, keyHash, month, increment)
	}
	return 0, nil
}

func (m *MockKeyRepository) UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error {
	if m.UpdateLastUsedAtFunc != nil {
		return m.UpdateLastUsedAtFunc(ctx, keyHash, lastUsed)
	}
	return nil
}

func (m *MockKeyRepository) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return nil
}

func TestVerifyUsecase_VerifyKey(t *testing.T) {
	// デフォルトのモック: 常に許可
	defaultLimiter := &MockRateLimiter{}

	// RPM 超過テスト用のモック: 常に拒否
	rateLimitExceededLimiter := &MockRateLimiter{
		AllowFunc: func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
			return false, 0, time.Second * 30, nil
		},
	}

	now := time.Now()
	pastUnix := now.Add(-time.Hour).Unix()

	currentMonth := now.UTC().Format("2006-01")

	tests := []struct {
		name           string
		inputRawKey    string
		requiredScope  string
		mockGetKey     func(ctx context.Context, keyHash string) (*entity.APIKey, error)
		mockIncUsage   func(ctx context.Context, keyHash string, month string, increment int64) (int64, error)
		expectedValid  bool
		expectedReason string
	}{
		{
			name:        "Happy path",
			inputRawKey: "tlge-live-valid",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:        "key-1",
					IsActive:     true,
					Status:       entity.StatusActive,
					RateLimitRPM: 100,
					MonthlyQuota: 0,
					Scopes:       []string{"*"},
				}, nil
			},
			expectedValid: true,
		},
		{
			name:        "Key not found",
			inputRawKey: "tlge-live-notfound",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return nil, nil // not found
			},
			expectedValid:  false,
			expectedReason: "invalid_key",
		},
		{
			name:        "DB error",
			inputRawKey: "tlge-live-error",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return nil, errors.New("db connection failed")
			},
			expectedValid: false,
		},
		{
			name:        "Expired (TTL)",
			inputRawKey: "tlge-live-expired",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:     "key-expired",
					ExpiresAt: &pastUnix,
					IsActive:  true,
					Status:    entity.StatusActive,
				}, nil
			},
			expectedValid:  false,
			expectedReason: "expired",
		},
		{
			name:        "Rotation Expired",
			inputRawKey: "tlge-live-rotation-expired",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:    "key-rot-expired",
					IsActive: true,
					Status:   entity.StatusRotating,
					Rotation: &entity.RotationMeta{
						GracePeriodExpiresAt: now.Add(-time.Hour),
					},
				}, nil
			},
			expectedValid:  false,
			expectedReason: "rotation_expired",
		},
		{
			name:        "Inactive / Suspended",
			inputRawKey: "tlge-live-suspended",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:    "key-susp",
					IsActive: true, // Even if true, status overrides
					Status:   entity.StatusSuspended,
				}, nil
			},
			expectedValid:  false,
			expectedReason: "suspended",
		},
		{
			name:        "Revoked",
			inputRawKey: "tlge-live-revoked",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:    "key-rev",
					IsActive: false,
					Status:   entity.StatusRevoked,
				}, nil
			},
			expectedValid:  false,
			expectedReason: "revoked",
		},
		{
			name:          "Scope Mismatch",
			inputRawKey:   "tlge-live-scope",
			requiredScope: "ai:workflows:execute",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:    "key-scope",
					IsActive: true,
					Status:   entity.StatusActive,
					Scopes:   []string{"mcp:tools:execute"}, // missing ai scope
				}, nil
			},
			expectedValid:  false,
			expectedReason: "scope_mismatch",
		},
		{
			name:        "Rate Limit Exceeded",
			inputRawKey: "tlge-live-rpm-limit",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:        "exhausted-rpm",
					IsActive:     true,
					Status:       entity.StatusActive,
					Scopes:       []string{"*"},
					RateLimitRPM: 1,
				}, nil
			},
			expectedValid:  false,
			expectedReason: "rate_limit_exceeded",
		},
		{
			name:        "Monthly Quota Exceeded (current usage >= quota)",
			inputRawKey: "tlge-live-quota-limit",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:             "key-quota",
					IsActive:          true,
					Status:            entity.StatusActive,
					Scopes:            []string{"*"},
					RateLimitRPM:      100,
					MonthlyQuota:      1000,
					CurrentMonth:      currentMonth,
					CurrentMonthUsage: 1000, // already reached
				}, nil
			},
			expectedValid:  false,
			expectedReason: "quota_exceeded",
		},
		{
			name:        "Monthly Quota Increment",
			inputRawKey: "tlge-live-quota-inc",
			mockGetKey: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					KeyID:             "key-quota-inc",
					IsActive:          true,
					Status:            entity.StatusActive,
					Scopes:            []string{"*"},
					RateLimitRPM:      100,
					MonthlyQuota:      1000,
					CurrentMonth:      currentMonth,
					CurrentMonthUsage: 999, // 1 left before request
				}, nil
			},
			mockIncUsage: func(ctx context.Context, keyHash string, month string, increment int64) (int64, error) {
				return 1000, nil // new usage
			},
			expectedValid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &MockKeyRepository{
				GetKeyByHashFunc:          tc.mockGetKey,
				IncrementMonthlyUsageFunc: tc.mockIncUsage,
			}
			// Rate Limit Exceeded テストのみ常に拒否する limiter を使用する
			var limiter repository.RateLimiter = defaultLimiter
			if tc.name == "Rate Limit Exceeded" {
				limiter = rateLimitExceededLimiter
			}
			uc := NewVerifyUsecase(repo, limiter)

			input := entity.VerifyKeyInput{
				RawKey:        tc.inputRawKey,
				RequiredScope: tc.requiredScope,
			}

			output, err := uc.VerifyKey(context.Background(), input)
			if tc.name == "DB error" {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if output == nil {
				t.Fatalf("expected output, got nil")
			}

			if output.Valid != tc.expectedValid {
				t.Errorf("expected valid=%v, got %v", tc.expectedValid, output.Valid)
			}

			if tc.expectedReason != "" && output.Reason != tc.expectedReason {
				t.Errorf("expected reason=%q, got %q", tc.expectedReason, output.Reason)
			}
		})
	}

	t.Run("Rotation Active (Grace Period Valid)", func(t *testing.T) {
		validGrace := time.Now().Add(1 * time.Hour)
		repo := &MockKeyRepository{
			GetKeyByHashFunc: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					PK:           "KEY#" + HashKey("tlge-live-rotating-valid"),
					KeyID:        "kid-rot-valid",
					Status:       entity.StatusRotating,
					IsActive:     true,
					RateLimitRPM: 600,
					Scopes:       []string{"*"},
					Rotation: &entity.RotationMeta{
						OldKeyHash:           "old-hash",
						GracePeriodExpiresAt: validGrace,
					},
				}, nil
			},
		}
		uc := NewVerifyUsecase(repo, defaultLimiter)
		res, err := uc.VerifyKey(context.Background(), entity.VerifyKeyInput{
			RawKey: "tlge-live-rotating-valid",
		})
		if err != nil || !res.Valid {
			t.Fatalf("expected valid for active rotating key within grace period, got %v, err=%v", res, err)
		}
	})

	t.Run("IncrementMonthlyUsage failure fails open", func(t *testing.T) {
		currentMonth := time.Now().Format("2006-01")
		repo := &MockKeyRepository{
			GetKeyByHashFunc: func(ctx context.Context, keyHash string) (*entity.APIKey, error) {
				return &entity.APIKey{
					PK:                "KEY#" + HashKey("tlge-live-inc-err"),
					KeyID:             "kid-inc-err",
					Status:            entity.StatusActive,
					IsActive:          true,
					RateLimitRPM:      600,
					MonthlyQuota:      1000,
					CurrentMonth:      currentMonth,
					CurrentMonthUsage: 10,
					Scopes:            []string{"*"},
				}, nil
			},
			IncrementMonthlyUsageFunc: func(ctx context.Context, keyHash string, month string, increment int64) (int64, error) {
				return 0, errors.New("dynamo increment error")
			},
		}
		uc := NewVerifyUsecase(repo, defaultLimiter)
		res, err := uc.VerifyKey(context.Background(), entity.VerifyKeyInput{
			RawKey: "tlge-live-inc-err",
		})
		if err != nil || !res.Valid {
			t.Fatalf("expected allow on quota increment failure (fail-open), got %v", res)
		}
	})

	t.Run("matchScope variations", func(t *testing.T) {
		cases := []struct {
			required string
			granted  []string
			expected bool
		}{
			{"users:read", []string{"users:read"}, true},
			{"users:read", []string{"users:*"}, true},
			{"users:read", []string{"*"}, true},
			{"users:write", []string{"users:read"}, false},
			{"billing:invoices:read", []string{"billing:*"}, true},
			{"billing:invoices:read", []string{"billing:invoices:*"}, true},
			{"billing:invoices:read", []string{"billing:invoices:write"}, false},
			{"users:read", []string{}, false},
			{"", []string{"users:read"}, true},
		}

		for _, c := range cases {
			if got := matchScope(c.required, c.granted); got != c.expected {
				t.Errorf("matchScope(%q, %v) = %v, want %v", c.required, c.granted, got, c.expected)
			}
		}
	})
}
