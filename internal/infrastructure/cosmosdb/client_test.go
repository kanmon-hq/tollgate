package cosmosdb

import (
	"testing"
	"time"

	"github.com/northfieldzz/tollgate/internal/domain/entity"
)

func TestCosmosItemMapping(t *testing.T) {
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

	item := toCosmosItem(key)
	if item.ID != key.PK || item.PK != key.PK {
		t.Fatalf("expected ID and PK to match key.PK, got ID=%s, PK=%s", item.ID, item.PK)
	}
	if item.KeyID != key.KeyID || item.TenantID != key.TenantID || len(item.Scopes) != 2 {
		t.Fatalf("unexpected cosmos item mapping: %+v", item)
	}

	converted := fromCosmosItem(item)
	if converted.PK != key.PK || converted.KeyID != key.KeyID || converted.TenantID != key.TenantID {
		t.Fatalf("unexpected converted key: %+v", converted)
	}
	if len(converted.Scopes) != 2 || converted.Scopes[0] != "read:users" {
		t.Fatalf("unexpected converted scopes: %v", converted.Scopes)
	}
}

func TestNewCosmosDBRepository_Validation(t *testing.T) {
	_, err := NewCosmosDBRepository("", "key", "db", "container")
	if err == nil {
		t.Fatalf("expected error for empty endpoint")
	}

	_, err = NewCosmosDBRepository("https://example.documents.azure.com:443/", "", "db", "container")
	if err == nil {
		t.Fatalf("expected error for empty key")
	}
}
