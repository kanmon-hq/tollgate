package cosmosdb

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/northfieldzz/tollgate/internal/domain/entity"
	"github.com/northfieldzz/tollgate/internal/domain/repository"
)

// cosmosItem は Azure Cosmos DB for NoSQL に格納するドキュメント構造。
// Cosmos DB の必須プロパティ id を PK と同一値 ("KEY#<hash>") にマッピングする。
type cosmosItem struct {
	ID                string               `json:"id"` // Cosmos DB 必須フィールド ("KEY#<hash>")
	PK                string               `json:"pk"` // パーティションキー ("KEY#<hash>")
	KeyID             string               `json:"key_id"`
	KeyPrefix         string               `json:"key_prefix"`
	Name              string               `json:"name"`
	TenantID          string               `json:"tenant_id,omitempty"`
	ServiceID         string               `json:"service_id,omitempty"`
	Scopes            []string             `json:"scopes"`
	RateLimitRPM      int                  `json:"rate_limit_rpm"`
	MonthlyQuota      int64                `json:"monthly_quota"`
	CurrentMonthUsage int64                `json:"current_month_usage"`
	CurrentMonth      string               `json:"current_month"`
	Status            entity.KeyStatus     `json:"status"`
	IsActive          bool                 `json:"is_active"`
	LastUsedAt        *time.Time           `json:"last_used_at,omitempty"`
	Rotation          *entity.RotationMeta `json:"rotation_meta,omitempty"`
	ExpiresAt         *int64               `json:"expires_at,omitempty"`
	CreatedAt         string               `json:"created_at"`
	UpdatedAt         string               `json:"updated_at"`
}

func toCosmosItem(k *entity.APIKey) *cosmosItem {
	return &cosmosItem{
		ID:                k.PK,
		PK:                k.PK,
		KeyID:             k.KeyID,
		KeyPrefix:         k.KeyPrefix,
		Name:              k.Name,
		TenantID:          k.TenantID,
		ServiceID:         k.ServiceID,
		Scopes:            k.Scopes,
		RateLimitRPM:      k.RateLimitRPM,
		MonthlyQuota:      k.MonthlyQuota,
		CurrentMonthUsage: k.CurrentMonthUsage,
		CurrentMonth:      k.CurrentMonth,
		Status:            k.Status,
		IsActive:          k.IsActive,
		LastUsedAt:        k.LastUsedAt,
		Rotation:          k.Rotation,
		ExpiresAt:         k.ExpiresAt,
		CreatedAt:         k.CreatedAt,
		UpdatedAt:         k.UpdatedAt,
	}
}

func fromCosmosItem(item *cosmosItem) *entity.APIKey {
	scopes := item.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return &entity.APIKey{
		PK:                item.PK,
		KeyID:             item.KeyID,
		KeyPrefix:         item.KeyPrefix,
		Name:              item.Name,
		TenantID:          item.TenantID,
		ServiceID:         item.ServiceID,
		Scopes:            scopes,
		RateLimitRPM:      item.RateLimitRPM,
		MonthlyQuota:      item.MonthlyQuota,
		CurrentMonthUsage: item.CurrentMonthUsage,
		CurrentMonth:      item.CurrentMonth,
		Status:            item.Status,
		IsActive:          item.IsActive,
		LastUsedAt:        item.LastUsedAt,
		Rotation:          item.Rotation,
		ExpiresAt:         item.ExpiresAt,
		CreatedAt:         item.CreatedAt,
		UpdatedAt:         item.UpdatedAt,
	}
}

// CosmosDBRepository は Azure Cosmos DB for NoSQL を用いた KeyRepository 実装。
type CosmosDBRepository struct {
	client        *azcosmos.Client
	databaseName  string
	containerName string
	container     *azcosmos.ContainerClient
}

var _ repository.KeyRepository = (*CosmosDBRepository)(nil)

// NewCosmosDBRepository は Azure Cosmos DB バックエンドの KeyRepository を初期化する。
func NewCosmosDBRepository(endpoint, key, database, container string) (*CosmosDBRepository, error) {
	if endpoint == "" {
		return nil, errors.New("cosmosdb endpoint is required")
	}
	if key == "" {
		return nil, errors.New("cosmosdb key is required")
	}

	cred, err := azcosmos.NewKeyCredential(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cosmos credential: %w", err)
	}

	client, err := azcosmos.NewClientWithKey(endpoint, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cosmos client: %w", err)
	}

	dbName := cmp.Or(database, "tollgate")
	containerName_ := cmp.Or(container, "api_keys")

	containerClient, err := client.NewContainer(dbName, containerName_)
	if err != nil {
		return nil, fmt.Errorf("failed to create cosmos container client: %w", err)
	}

	return &CosmosDBRepository{
		client:        client,
		databaseName:  dbName,
		containerName: containerName_,
		container:     containerClient,
	}, nil
}

// PutKey は API キーを Cosmos DB に保存（または Upsert）する。
func (r *CosmosDBRepository) PutKey(ctx context.Context, key *entity.APIKey) error {
	item := toCosmosItem(key)
	b, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("cosmosdb marshal key: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(item.PK)
	_, err = r.container.UpsertItem(ctx, pk, b, nil)
	if err != nil {
		return fmt.Errorf("cosmosdb UpsertItem: %w", err)
	}
	return nil
}

// GetKeyByHash は SHA-256 ハッシュから Point Read (1 RU) で高速取得する。
func (r *CosmosDBRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	pkVal := "KEY#" + keyHash
	pk := azcosmos.NewPartitionKeyString(pkVal)

	resp, err := r.container.ReadItem(ctx, pk, pkVal, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("cosmosdb ReadItem: %w", err)
	}

	var item cosmosItem
	if err := json.Unmarshal(resp.Value, &item); err != nil {
		return nil, fmt.Errorf("cosmosdb unmarshal item: %w", err)
	}
	return fromCosmosItem(&item), nil
}

// GetKeyByID は KeyID でクエリ検索する。
func (r *CosmosDBRepository) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	query := "SELECT * FROM c WHERE c.key_id = @keyID"
	opt := &azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{
			{Name: "@keyID", Value: keyID},
		},
	}

	pager := r.container.NewQueryItemsPager(query, azcosmos.PartitionKey{}, opt)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("cosmosdb query by key_id: %w", err)
		}
		for _, raw := range page.Items {
			var item cosmosItem
			if err := json.Unmarshal(raw, &item); err == nil {
				return fromCosmosItem(&item), nil
			}
		}
	}
	return nil, nil
}

// ListKeysByTenant は指定テナントのキー一覧を取得する。
func (r *CosmosDBRepository) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	query := "SELECT * FROM c WHERE c.tenant_id = @tenantID ORDER BY c.created_at DESC"
	opt := &azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{
			{Name: "@tenantID", Value: tenantID},
		},
	}

	var results []*entity.APIKey
	pager := r.container.NewQueryItemsPager(query, azcosmos.PartitionKey{}, opt)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("cosmosdb query list keys: %w", err)
		}
		for _, raw := range page.Items {
			var item cosmosItem
			if err := json.Unmarshal(raw, &item); err == nil {
				results = append(results, fromCosmosItem(&item))
			}
		}
	}
	return results, nil
}

// UpdateKeyStatus はキーの状態および有効フラグを更新する。
func (r *CosmosDBRepository) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	pkVal := "KEY#" + keyHash
	pk := azcosmos.NewPartitionKeyString(pkVal)
	nowStr := time.Now().UTC().Format(time.RFC3339)

	patch := azcosmos.PatchOperations{}
	patch.AppendSet("/status", string(status))
	patch.AppendSet("/is_active", isActive)
	patch.AppendSet("/updated_at", nowStr)

	_, err := r.container.PatchItem(ctx, pk, pkVal, patch, nil)
	if err != nil {
		return fmt.Errorf("cosmosdb UpdateKeyStatus patch: %w", err)
	}
	return nil
}

// UpdateKeySettings はキーの設定値を更新する。
func (r *CosmosDBRepository) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	pkVal := "KEY#" + keyHash
	pk := azcosmos.NewPartitionKeyString(pkVal)
	nowStr := time.Now().UTC().Format(time.RFC3339)

	patch := azcosmos.PatchOperations{}
	patch.AppendSet("/updated_at", nowStr)

	if input.Name != nil {
		patch.AppendSet("/name", *input.Name)
	}
	if input.Scopes != nil {
		patch.AppendSet("/scopes", *input.Scopes)
	}
	if input.RateLimitRPM != nil {
		patch.AppendSet("/rate_limit_rpm", *input.RateLimitRPM)
	}
	if input.MonthlyQuota != nil {
		patch.AppendSet("/monthly_quota", *input.MonthlyQuota)
	}

	resp, err := r.container.PatchItem(ctx, pk, pkVal, patch, nil)
	if err != nil {
		return nil, fmt.Errorf("cosmosdb UpdateKeySettings patch: %w", err)
	}

	var item cosmosItem
	if err := json.Unmarshal(resp.Value, &item); err != nil {
		return r.GetKeyByHash(ctx, keyHash)
	}
	return fromCosmosItem(&item), nil
}

// RotateKey はローテーションを実行し、旧キーのステータスを rotating にし新キーを挿入する。
func (r *CosmosDBRepository) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	oldKey, err := r.GetKeyByHash(ctx, params.OldKeyHash)
	if err != nil {
		return nil, fmt.Errorf("cosmosdb RotateKey get old key: %w", err)
	}
	if oldKey == nil {
		return nil, errors.New("old key not found")
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// 1. 旧キーを rotating に更新
	oldKey.Status = entity.StatusRotating
	oldKey.Rotation = &entity.RotationMeta{
		OldKeyHash:           params.OldKeyHash,
		GracePeriodExpiresAt: params.GracePeriodExpiresAt,
	}
	oldKey.UpdatedAt = nowStr
	if err := r.PutKey(ctx, oldKey); err != nil {
		return nil, fmt.Errorf("cosmosdb RotateKey update old key: %w", err)
	}

	// 2. 新キーを作成して保存
	newKey := *oldKey
	newKey.PK = "KEY#" + params.NewKeyHash
	newKey.KeyPrefix = params.NewKeyPrefix
	newKey.Status = entity.StatusActive
	newKey.IsActive = true
	newKey.Rotation = nil
	newKey.UpdatedAt = nowStr

	if err := r.PutKey(ctx, &newKey); err != nil {
		return nil, fmt.Errorf("cosmosdb RotateKey put new key: %w", err)
	}

	return &newKey, nil
}

// DeleteKey はキーを物理削除する。
func (r *CosmosDBRepository) DeleteKey(ctx context.Context, keyHash string) error {
	pkVal := "KEY#" + keyHash
	pk := azcosmos.NewPartitionKeyString(pkVal)
	_, err := r.container.DeleteItem(ctx, pk, pkVal, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("cosmosdb DeleteItem: %w", err)
	}
	return nil
}

// IncrementMonthlyUsage は当月消費カウントをインクリメントする。
func (r *CosmosDBRepository) IncrementMonthlyUsage(ctx context.Context, keyHash, currentMonth string, delta int64) (int64, error) {
	key, err := r.GetKeyByHash(ctx, keyHash)
	if err != nil {
		return 0, fmt.Errorf("cosmosdb IncrementMonthlyUsage get: %w", err)
	}
	if key == nil {
		return 0, errors.New("key not found")
	}

	if key.CurrentMonth == currentMonth {
		key.CurrentMonthUsage += delta
	} else {
		key.CurrentMonth = currentMonth
		key.CurrentMonthUsage = delta
	}
	key.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := r.PutKey(ctx, key); err != nil {
		return 0, fmt.Errorf("cosmosdb IncrementMonthlyUsage put: %w", err)
	}
	return key.CurrentMonthUsage, nil
}

// UpdateLastUsedAt は最終利用日時を記録する。
func (r *CosmosDBRepository) UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error {
	pkVal := "KEY#" + keyHash
	pk := azcosmos.NewPartitionKeyString(pkVal)

	patch := azcosmos.PatchOperations{}
	patch.AppendSet("/last_used_at", lastUsed.UTC().Format(time.RFC3339))
	_, err := r.container.PatchItem(ctx, pk, pkVal, patch, nil)
	if err != nil {
		return fmt.Errorf("cosmosdb UpdateLastUsedAt patch: %w", err)
	}
	return nil
}

// Ping は Cosmos DB への疎通確認を行う。
func (r *CosmosDBRepository) Ping(ctx context.Context) error {
	_, err := r.container.Read(ctx, nil)
	if err != nil {
		return fmt.Errorf("cosmosdb ping container: %w", err)
	}
	return nil
}
