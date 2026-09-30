package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
)

func TestKeyUsecase_CRUD_and_Lifecycle(t *testing.T) {
	ctx := context.Background()

	keysByHash := make(map[string]*entity.APIKey)
	keysByID := make(map[string]*entity.APIKey)

	mockRepo := &MockKeyRepository{
		PutKeyFunc: func(ctx context.Context, key *entity.APIKey) error {
			keysByHash[key.GetHash()] = key
			keysByID[key.KeyID] = key
			return nil
		},
		GetKeyByIDFunc: func(ctx context.Context, keyID string) (*entity.APIKey, error) {
			return keysByID[keyID], nil
		},
		ListKeysByTenantFunc: func(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
			var list []*entity.APIKey
			for _, k := range keysByHash {
				if k.TenantID == tenantID {
					list = append(list, k)
				}
			}
			return list, nil
		},
		UpdateKeySettingsFunc: func(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
			k := keysByHash[keyHash]
			if k != nil && input.Name != nil {
				k.Name = *input.Name
			}
			if k != nil && input.RateLimitRPM != nil {
				k.RateLimitRPM = *input.RateLimitRPM
			}
			return k, nil
		},
		UpdateKeyStatusFunc: func(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
			k := keysByHash[keyHash]
			if k != nil {
				k.Status = status
				k.IsActive = isActive
			}
			return nil
		},
		RotateKeyFunc: func(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
			oldK := keysByHash[params.OldKeyHash]
			if oldK == nil {
				return nil, errors.New("old key not found")
			}
			newK := *oldK
			newK.PK = "KEY#" + params.NewKeyHash
			newK.KeyPrefix = params.NewKeyPrefix
			keysByHash[params.NewKeyHash] = &newK
			return &newK, nil
		},
		DeleteKeyFunc: func(ctx context.Context, keyHash string) error {
			if k := keysByHash[keyHash]; k != nil {
				delete(keysByID, k.KeyID)
				delete(keysByHash, keyHash)
			}
			return nil
		},
	}
	usecase := NewKeyUsecase(mockRepo)

	// 1. CreateKey (Success)
	createOut, err := usecase.CreateKey(ctx, entity.CreateKeyInput{
		Name:         "Service Alpha",
		TenantID:     "tenant-100",
		ServiceID:    "svc-alpha",
		Scopes:       []string{"users:read", "users:write"},
		RateLimitRPM: 120,
		MonthlyQuota: 5000,
		ExpiresIn:    3600,
	})
	if err != nil {
		t.Fatalf("CreateKey failed: %v", err)
	}
	if createOut.RawKey == "" || createOut.KeyID == "" || createOut.RateLimitRPM != 120 {
		t.Fatalf("unexpected create output: %+v", createOut)
	}
	if createOut.ExpiresAt == nil {
		t.Fatalf("expected ExpiresAt to be set")
	}

	keyID := createOut.KeyID

	// 2. GetKey (Success)
	gotKey, err := usecase.GetKey(ctx, keyID)
	if err != nil {
		t.Fatalf("GetKey failed: %v", err)
	}
	if gotKey.Name != "Service Alpha" || gotKey.TenantID != "tenant-100" {
		t.Fatalf("unexpected key returned: %+v", gotKey)
	}

	// 3. ListKeys
	listKeys, err := usecase.ListKeys(ctx, "tenant-100")
	if err != nil {
		t.Fatalf("ListKeys failed: %v", err)
	}
	if len(listKeys) != 1 || listKeys[0].KeyID != keyID {
		t.Fatalf("unexpected list keys: %+v", listKeys)
	}

	// 4. UpdateKey
	newName := "Updated Service Alpha"
	newRPM := 300
	newScopes := []string{"admin:*"}
	newQuota := int64(10000)
	updatedKey, err := usecase.UpdateKey(ctx, keyID, entity.UpdateKeyInput{
		Name:         &newName,
		RateLimitRPM: &newRPM,
		Scopes:       &newScopes,
		MonthlyQuota: &newQuota,
	})
	if err != nil {
		t.Fatalf("UpdateKey failed: %v", err)
	}
	if updatedKey.Name != newName || updatedKey.RateLimitRPM != 300 {
		t.Fatalf("unexpected updated key: %+v", updatedKey)
	}

	// 5. SuspendKey
	suspended, err := usecase.SuspendKey(ctx, keyID)
	if err != nil {
		t.Fatalf("SuspendKey failed: %v", err)
	}
	if suspended.Status != entity.StatusSuspended || suspended.IsActive {
		t.Fatalf("key not suspended: %+v", suspended)
	}

	// 6. ResumeKey
	resumed, err := usecase.ResumeKey(ctx, keyID)
	if err != nil {
		t.Fatalf("ResumeKey failed: %v", err)
	}
	if resumed.Status != entity.StatusActive || !resumed.IsActive {
		t.Fatalf("key not resumed: %+v", resumed)
	}

	// 7. RotateKey
	rotateOut, err := usecase.RotateKey(ctx, keyID, entity.RotateKeyInput{
		GracePeriodHours: 48,
	})
	if err != nil {
		t.Fatalf("RotateKey failed: %v", err)
	}
	if rotateOut.NewRawKey == "" || rotateOut.KeyPrefix == "" {
		t.Fatalf("unexpected rotate output: %+v", rotateOut)
	}

	// 8. DeleteKey
	if err := usecase.DeleteKey(ctx, keyID); err != nil {
		t.Fatalf("DeleteKey failed: %v", err)
	}

	// 9. GetKey (Not Found)
	_, err = usecase.GetKey(ctx, keyID)
	if err == nil {
		t.Fatalf("expected error for deleted key")
	}
}

func TestKeyUsecase_ValidationAndErrors(t *testing.T) {
	ctx := context.Background()
	mockRepo := &MockKeyRepository{}
	usecase := NewKeyUsecase(mockRepo)

	t.Run("CreateKey missing service_id", func(t *testing.T) {
		_, err := usecase.CreateKey(ctx, entity.CreateKeyInput{
			Name:      "Invalid Key",
			ServiceID: "",
		})
		if err == nil {
			t.Fatalf("expected validation error")
		}
	})

	t.Run("CreateKey with repo failure", func(t *testing.T) {
		errRepo := &MockKeyRepository{
			PutKeyFunc: func(ctx context.Context, key *entity.APIKey) error {
				return errors.New("db error")
			},
		}
		errUsecase := NewKeyUsecase(errRepo)
		_, err := errUsecase.CreateKey(ctx, entity.CreateKeyInput{
			Name:      "Test",
			ServiceID: "svc",
		})
		if err == nil {
			t.Fatalf("expected error on repo PutKey failure")
		}
	})

	t.Run("GetKey with repo error", func(t *testing.T) {
		errRepo := &MockKeyRepository{
			GetKeyByIDFunc: func(ctx context.Context, keyID string) (*entity.APIKey, error) {
				return nil, errors.New("db find error")
			},
		}
		errUsecase := NewKeyUsecase(errRepo)
		_, err := errUsecase.GetKey(ctx, "nonexistent")
		if err == nil {
			t.Fatalf("expected db error")
		}
	})

	t.Run("SuspendKey on non-existent key", func(t *testing.T) {
		_, err := usecase.SuspendKey(ctx, "invalid-key-id")
		if err == nil {
			t.Fatalf("expected not found error")
		}
	})

	t.Run("ResumeKey on non-existent key", func(t *testing.T) {
		_, err := usecase.ResumeKey(ctx, "invalid-key-id")
		if err == nil {
			t.Fatalf("expected not found error")
		}
	})

	t.Run("RotateKey on non-existent key", func(t *testing.T) {
		_, err := usecase.RotateKey(ctx, "invalid-key-id", entity.RotateKeyInput{})
		if err == nil {
			t.Fatalf("expected not found error")
		}
	})

	t.Run("DeleteKey on non-existent key", func(t *testing.T) {
		err := usecase.DeleteKey(ctx, "invalid-key-id")
		if err == nil {
			t.Fatalf("expected not found error")
		}
	})
}
