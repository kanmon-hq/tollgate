package sqlrepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/sqlrepo"
)

func setupTestSQLite(t *testing.T) *sqlrepo.SQLRepository {
	t.Helper()
	repo, err := sqlrepo.NewSQLiteRepository(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite repo: %v", err)
	}
	return repo
}

func TestSQLRepository_CRUD(t *testing.T) {
	ctx := context.Background()
	repo := setupTestSQLite(t)

	nowStr := time.Now().UTC().Format(time.RFC3339)
	key := &entity.APIKey{
		PK:                "KEY#hash123",
		KeyID:             "key_abc",
		KeyPrefix:         "tg_live_",
		Name:              "Test Key",
		TenantID:          "tenant_1",
		ServiceID:         "svc_1",
		Scopes:            []string{"read:users", "write:users"},
		Status:            entity.StatusActive,
		IsActive:          true,
		RateLimitRPM:      60,
		MonthlyQuota:      1000,
		CurrentMonth:      "2026-09",
		CurrentMonthUsage: 0,
		ExpiresAt:         nil,
		Rotation:          nil,
		LastUsedAt:        nil,
		CreatedAt:         nowStr,
		UpdatedAt:         nowStr,
	}

	// 1. PutKey
	if err := repo.PutKey(ctx, key); err != nil {
		t.Fatalf("PutKey failed: %v", err)
	}

	// 2. GetKeyByHash
	gotByHash, err := repo.GetKeyByHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("GetKeyByHash failed: %v", err)
	}
	if gotByHash == nil || gotByHash.KeyID != key.KeyID || gotByHash.TenantID != key.TenantID || len(gotByHash.Scopes) != 2 {
		t.Fatalf("unexpected key data: %+v", gotByHash)
	}

	// 3. GetKeyByID
	gotByID, err := repo.GetKeyByID(ctx, "key_abc")
	if err != nil {
		t.Fatalf("GetKeyByID failed: %v", err)
	}
	if gotByID == nil || gotByID.PK != key.PK {
		t.Fatalf("unexpected key by id: %+v", gotByID)
	}

	// 4. ListKeysByTenant
	keys, err := repo.ListKeysByTenant(ctx, "tenant_1")
	if err != nil {
		t.Fatalf("ListKeysByTenant failed: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got len=%d", len(keys))
	}

	// 5. UpdateKeySettings
	newName := "Updated Key"
	newRPM := 120
	updated, err := repo.UpdateKeySettings(ctx, "hash123", entity.UpdateKeyInput{
		Name:         &newName,
		RateLimitRPM: &newRPM,
	})
	if err != nil {
		t.Fatalf("UpdateKeySettings failed: %v", err)
	}
	if updated.Name != "Updated Key" || updated.RateLimitRPM != 120 {
		t.Fatalf("update not reflected: %+v", updated)
	}

	// 6. UpdateLastUsedAt
	usedTime := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdateLastUsedAt(ctx, "hash123", usedTime); err != nil {
		t.Fatalf("UpdateLastUsedAt failed: %v", err)
	}
	afterUsed, err := repo.GetKeyByHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("GetKeyByHash failed: %v", err)
	}
	if afterUsed.LastUsedAt == nil || !afterUsed.LastUsedAt.Equal(usedTime) {
		t.Fatalf("last_used_at mismatch: got %v, want %v", afterUsed.LastUsedAt, usedTime)
	}

	// 7. IncrementMonthlyUsage
	usage1, err := repo.IncrementMonthlyUsage(ctx, "hash123", "2026-09", 1)
	if err != nil {
		t.Fatalf("IncrementMonthlyUsage failed: %v", err)
	}
	if usage1 != 1 {
		t.Fatalf("expected usage 1, got %d", usage1)
	}

	usage2, err := repo.IncrementMonthlyUsage(ctx, "hash123", "2026-09", 1)
	if err != nil {
		t.Fatalf("IncrementMonthlyUsage 2 failed: %v", err)
	}
	if usage2 != 2 {
		t.Fatalf("expected usage 2, got %d", usage2)
	}

	// 月が変わった時のリセット
	usageMonth2, err := repo.IncrementMonthlyUsage(ctx, "hash123", "2026-10", 1)
	if err != nil {
		t.Fatalf("IncrementMonthlyUsage new month failed: %v", err)
	}
	if usageMonth2 != 1 {
		t.Fatalf("expected reset usage 1, got %d", usageMonth2)
	}

	// 8. UpdateKeyStatus (Revoke)
	if err := repo.UpdateKeyStatus(ctx, "hash123", entity.StatusRevoked, false); err != nil {
		t.Fatalf("UpdateKeyStatus failed: %v", err)
	}
	revoked, err := repo.GetKeyByHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("GetKeyByHash failed: %v", err)
	}
	if revoked.Status != entity.StatusRevoked || revoked.IsActive {
		t.Fatalf("key not revoked properly: %+v", revoked)
	}

	// 9. RotateKey
	rotated, err := repo.RotateKey(ctx, entity.RotateKeyParams{
		OldKeyHash:           "hash123",
		NewKeyHash:           "hash456",
		NewKeyPrefix:         "tg_live_new",
		GracePeriodExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("RotateKey failed: %v", err)
	}
	if rotated.KeyPrefix != "tg_live_new" || rotated.Status != entity.StatusActive {
		t.Fatalf("RotateKey unexpected return: %+v", rotated)
	}

	// 旧キーが rotating になっており rotation_meta があることを確認
	oldKeyAfterRotate, err := repo.GetKeyByHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("GetKeyByHash old key failed: %v", err)
	}
	if oldKeyAfterRotate.Status != entity.StatusRotating || oldKeyAfterRotate.Rotation == nil || oldKeyAfterRotate.Rotation.OldKeyHash != "hash123" {
		t.Fatalf("old key after rotate invalid: %+v", oldKeyAfterRotate)
	}

	// 10. DeleteKey
	if err := repo.DeleteKey(ctx, "hash123"); err != nil {
		t.Fatalf("DeleteKey failed: %v", err)
	}
	delCheck, err := repo.GetKeyByHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("GetKeyByHash after delete failed: %v", err)
	}
	if delCheck != nil {
		t.Fatalf("expected nil after delete, got %+v", delCheck)
	}

	// 11. Ping
	if err := repo.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	// 12. RotateKey on missing key
	_, err = repo.RotateKey(ctx, entity.RotateKeyParams{
		OldKeyHash: "missing",
		NewKeyHash: "new",
	})
	if err == nil {
		t.Fatalf("expected error on rotating missing key")
	}

	// 13. PutKey and GetKey with nil nullable fields
	nilKey := &entity.APIKey{
		PK:           "KEY#nilhash",
		KeyID:        "key_nil",
		Name:         "Nil Fields Key",
		TenantID:     "tenant_2",
		Scopes:       nil, // nil scopes
		Status:       entity.StatusActive,
		IsActive:     true,
		RateLimitRPM: 100,
		MonthlyQuota: 0,
		CurrentMonth: "2026-09",
		ExpiresAt:    nil,
		Rotation:     nil,
		LastUsedAt:   nil,
		CreatedAt:    nowStr,
		UpdatedAt:    nowStr,
	}
	if err := repo.PutKey(ctx, nilKey); err != nil {
		t.Fatalf("PutKey with nil fields failed: %v", err)
	}
	gotNilKey, err := repo.GetKeyByHash(ctx, "nilhash")
	if err != nil || gotNilKey == nil {
		t.Fatalf("GetKeyByHash nilKey failed: %v", err)
	}
	if len(gotNilKey.Scopes) != 0 || gotNilKey.ExpiresAt != nil || gotNilKey.Rotation != nil {
		t.Errorf("expected empty scopes and nil rotation/expiresAt")
	}

	// 14. UpdateKeySettings with Scopes and MonthlyQuota
	scopes := []string{"test:scope"}
	quota := int64(9999)
	updatedSettings, err := repo.UpdateKeySettings(ctx, "nilhash", entity.UpdateKeyInput{
		Scopes:       &scopes,
		MonthlyQuota: &quota,
	})
	if err != nil || updatedSettings.MonthlyQuota != 9999 || len(updatedSettings.Scopes) != 1 {
		t.Errorf("UpdateKeySettings partial update failed: %+v", updatedSettings)
	}
}
