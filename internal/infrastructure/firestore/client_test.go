package firestore

import (
	"context"
	"testing"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
)

func TestFirestoreDocMapping(t *testing.T) {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	lastUsed := time.Now().UTC()
	exp := int64(1700000000)

	key := &entity.APIKey{
		PK:                "KEY#hash123",
		KeyID:             "key_uuid_1",
		KeyPrefix:         "tlge-live-1",
		Name:              "Test Key",
		TenantID:          "tenant_1",
		ServiceID:         "service_1",
		Scopes:            []string{"read:users", "write:users"},
		RateLimitRPM:      60,
		MonthlyQuota:      10000,
		CurrentMonthUsage: 50,
		CurrentMonth:      "2026-09",
		Status:            entity.StatusActive,
		IsActive:          true,
		LastUsedAt:        &lastUsed,
		Rotation: &entity.RotationMeta{
			OldKeyHash:           "oldhash",
			GracePeriodExpiresAt: time.Now().Add(24 * time.Hour),
		},
		ExpiresAt: &exp,
		CreatedAt: nowStr,
		UpdatedAt: nowStr,
	}

	doc := toDoc(key)
	if doc.PK != key.PK || doc.KeyID != key.KeyID || doc.TenantID != key.TenantID || len(doc.Scopes) != 2 {
		t.Fatalf("unexpected firestore doc mapping: %+v", doc)
	}

	converted := fromDoc(doc)
	if converted.PK != key.PK || converted.KeyID != key.KeyID || converted.TenantID != key.TenantID {
		t.Fatalf("unexpected converted key: %+v", converted)
	}
	if len(converted.Scopes) != 2 || converted.Scopes[0] != "read:users" {
		t.Fatalf("unexpected converted scopes: %v", converted.Scopes)
	}
}

func TestNewFirestoreRepository_Validation(t *testing.T) {
	_, err := NewFirestoreRepository(context.Background(), "", "(default)", "api_keys")
	if err == nil {
		t.Fatalf("expected error for empty projectID")
	}
}
