package dynamodb

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
)

func TestAPIKey_DynamoDBMarshal_EmptyTenantID(t *testing.T) {
	key := &entity.APIKey{
		PK:        "KEY#testhash",
		KeyID:     "kid-123",
		Name:      "Service Key",
		TenantID:  "", // サービスキーのため空
		ServiceID: "llm-service",
		Status:    entity.StatusActive,
		IsActive:  true,
	}

	avMap, err := attributevalue.MarshalMap(key)
	if err != nil {
		t.Fatalf("MarshalMap failed: %v", err)
	}

	if _, exists := avMap["tenant_id"]; exists {
		t.Errorf("tenant_id must be omitted when empty to prevent DynamoDB GSI ValidationException, but found in map: %v", avMap["tenant_id"])
	}

	if _, exists := avMap["service_id"]; !exists {
		t.Errorf("service_id must be present in map, but was not found")
	}
}

func TestAPIKey_DynamoDBMarshal_WithTenantID(t *testing.T) {
	key := &entity.APIKey{
		PK:       "KEY#testhash",
		KeyID:    "kid-456",
		Name:     "Tenant Key",
		TenantID: "tenant-abc",
		Status:   entity.StatusActive,
		IsActive: true,
	}

	avMap, err := attributevalue.MarshalMap(key)
	if err != nil {
		t.Fatalf("MarshalMap failed: %v", err)
	}

	if _, exists := avMap["tenant_id"]; !exists {
		t.Errorf("tenant_id must be present in map when specified, but was omitted")
	}
}

func TestNewDynamoDBRepository(t *testing.T) {
	repo := NewDynamoDBRepository(nil, "")
	if repo == nil {
		t.Fatalf("expected non-nil repository")
	}
	dRepo, ok := repo.(*DynamoDBRepository)
	if !ok || dRepo.tableName != "TollgateAPIKeys" {
		t.Errorf("expected default tableName TollgateAPIKeys, got %v", dRepo.tableName)
	}
}
