package firestore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// firestoreKeyDoc は Google Cloud Firestore に格納・復元するためのドキュメント構造体。
type firestoreKeyDoc struct {
	PK                string               `firestore:"pk"`
	KeyID             string               `firestore:"key_id"`
	KeyPrefix         string               `firestore:"key_prefix"`
	Name              string               `firestore:"name"`
	TenantID          string               `firestore:"tenant_id,omitempty"`
	ServiceID         string               `firestore:"service_id,omitempty"`
	Scopes            []string             `firestore:"scopes"`
	RateLimitRPM      int                  `firestore:"rate_limit_rpm"`
	MonthlyQuota      int64                `firestore:"monthly_quota"`
	CurrentMonthUsage int64                `firestore:"current_month_usage"`
	CurrentMonth      string               `firestore:"current_month"`
	Status            string               `firestore:"status"`
	IsActive          bool                 `firestore:"is_active"`
	LastUsedAt        *time.Time           `firestore:"last_used_at,omitempty"`
	Rotation          *entity.RotationMeta `firestore:"rotation_meta,omitempty"`
	ExpiresAt         *int64               `firestore:"expires_at,omitempty"`
	CreatedAt         string               `firestore:"created_at"`
	UpdatedAt         string               `firestore:"updated_at"`
}

func toDoc(k *entity.APIKey) *firestoreKeyDoc {
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return &firestoreKeyDoc{
		PK:                k.PK,
		KeyID:             k.KeyID,
		KeyPrefix:         k.KeyPrefix,
		Name:              k.Name,
		TenantID:          k.TenantID,
		ServiceID:         k.ServiceID,
		Scopes:            scopes,
		RateLimitRPM:      k.RateLimitRPM,
		MonthlyQuota:      k.MonthlyQuota,
		CurrentMonthUsage: k.CurrentMonthUsage,
		CurrentMonth:      k.CurrentMonth,
		Status:            string(k.Status),
		IsActive:          k.IsActive,
		LastUsedAt:        k.LastUsedAt,
		Rotation:          k.Rotation,
		ExpiresAt:         k.ExpiresAt,
		CreatedAt:         k.CreatedAt,
		UpdatedAt:         k.UpdatedAt,
	}
}

func fromDoc(doc *firestoreKeyDoc) *entity.APIKey {
	scopes := doc.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return &entity.APIKey{
		PK:                doc.PK,
		KeyID:             doc.KeyID,
		KeyPrefix:         doc.KeyPrefix,
		Name:              doc.Name,
		TenantID:          doc.TenantID,
		ServiceID:         doc.ServiceID,
		Scopes:            scopes,
		RateLimitRPM:      doc.RateLimitRPM,
		MonthlyQuota:      doc.MonthlyQuota,
		CurrentMonthUsage: doc.CurrentMonthUsage,
		CurrentMonth:      doc.CurrentMonth,
		Status:            entity.KeyStatus(doc.Status),
		IsActive:          doc.IsActive,
		LastUsedAt:        doc.LastUsedAt,
		Rotation:          doc.Rotation,
		ExpiresAt:         doc.ExpiresAt,
		CreatedAt:         doc.CreatedAt,
		UpdatedAt:         doc.UpdatedAt,
	}
}

// FirestoreRepository は Google Cloud Firestore を用いた KeyRepository 実装。
type FirestoreRepository struct {
	client     *firestore.Client
	collection string
}

var _ repository.KeyRepository = (*FirestoreRepository)(nil)

// NewFirestoreRepository は Firestore クライアントを用いて KeyRepository を初期化する。
func NewFirestoreRepository(ctx context.Context, projectID, databaseID, collection string) (*FirestoreRepository, error) {
	if projectID == "" {
		return nil, errors.New("firestore project_id is required")
	}

	dbID := cmp.Or(databaseID, "(default)")
	client, err := firestore.NewClientWithDatabase(ctx, projectID, dbID)
	if err != nil {
		return nil, fmt.Errorf("failed to create firestore client: %w", err)
	}

	coll := cmp.Or(collection, "api_keys")

	return &FirestoreRepository{
		client:     client,
		collection: coll,
	}, nil
}

// Close は Firestore クライアントを安全に閉じる。
func (r *FirestoreRepository) Close() error {
	return r.client.Close()
}

// PutKey は API キーを Firestore に保存（または Upsert）する。
func (r *FirestoreRepository) PutKey(ctx context.Context, key *entity.APIKey) error {
	docRef := r.client.Collection(r.collection).Doc(key.GetHash())
	doc := toDoc(key)
	if _, err := docRef.Set(ctx, doc); err != nil {
		return fmt.Errorf("firestore PutKey: %w", err)
	}
	return nil
}

// GetKeyByHash は SHA-256 ハッシュからドキュメントを取得する。
func (r *FirestoreRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	docSnap, err := r.client.Collection(r.collection).Doc(keyHash).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("firestore GetKeyByHash: %w", err)
	}

	var doc firestoreKeyDoc
	if err := docSnap.DataTo(&doc); err != nil {
		return nil, fmt.Errorf("firestore unmarshal key doc: %w", err)
	}
	return fromDoc(&doc), nil
}

// GetKeyByID は key_id フィールドでドキュメントを検索する。
func (r *FirestoreRepository) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	iter := r.client.Collection(r.collection).Where("key_id", "==", keyID).Limit(1).Documents(ctx)
	defer iter.Stop()

	docSnap, err := iter.Next()
	if err == iterator.Done {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("firestore GetKeyByID: %w", err)
	}

	var doc firestoreKeyDoc
	if err := docSnap.DataTo(&doc); err != nil {
		return nil, fmt.Errorf("firestore unmarshal key doc: %w", err)
	}
	return fromDoc(&doc), nil
}

// ListKeysByTenant は指定テナントのキー一覧を取得する。
func (r *FirestoreRepository) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	iter := r.client.Collection(r.collection).Where("tenant_id", "==", tenantID).OrderBy("created_at", firestore.Desc).Documents(ctx)
	defer iter.Stop()

	var keys []*entity.APIKey
	for {
		docSnap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore ListKeysByTenant: %w", err)
		}

		var doc firestoreKeyDoc
		if err := docSnap.DataTo(&doc); err != nil {
			return nil, fmt.Errorf("firestore unmarshal key doc: %w", err)
		}
		keys = append(keys, fromDoc(&doc))
	}
	return keys, nil
}

// UpdateKeyStatus はキーの状態および有効フラグを更新する。
func (r *FirestoreRepository) UpdateKeyStatus(ctx context.Context, keyHash string, keyStatus entity.KeyStatus, isActive bool) error {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	updates := []firestore.Update{
		{Path: "status", Value: string(keyStatus)},
		{Path: "is_active", Value: isActive},
		{Path: "updated_at", Value: nowStr},
	}
	_, err := r.client.Collection(r.collection).Doc(keyHash).Update(ctx, updates)
	if err != nil {
		return fmt.Errorf("firestore UpdateKeyStatus: %w", err)
	}
	return nil
}

// UpdateKeySettings はキーの設定値を更新する。
func (r *FirestoreRepository) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	updates := []firestore.Update{
		{Path: "updated_at", Value: nowStr},
	}

	if input.Name != nil {
		updates = append(updates, firestore.Update{Path: "name", Value: *input.Name})
	}
	if input.Scopes != nil {
		updates = append(updates, firestore.Update{Path: "scopes", Value: *input.Scopes})
	}
	if input.RateLimitRPM != nil {
		updates = append(updates, firestore.Update{Path: "rate_limit_rpm", Value: *input.RateLimitRPM})
	}
	if input.MonthlyQuota != nil {
		updates = append(updates, firestore.Update{Path: "monthly_quota", Value: *input.MonthlyQuota})
	}

	_, err := r.client.Collection(r.collection).Doc(keyHash).Update(ctx, updates)
	if err != nil {
		return nil, fmt.Errorf("firestore UpdateKeySettings: %w", err)
	}
	return r.GetKeyByHash(ctx, keyHash)
}

// RotateKey はローテーションを実行し、トランザクション内で旧キーの更新と新キーの作成を行う。
func (r *FirestoreRepository) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	var newKey entity.APIKey

	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		oldDocRef := r.client.Collection(r.collection).Doc(params.OldKeyHash)
		oldDocSnap, err := tx.Get(oldDocRef)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return errors.New("old key not found")
			}
			return fmt.Errorf("get old key: %w", err)
		}

		var oldDoc firestoreKeyDoc
		if err := oldDocSnap.DataTo(&oldDoc); err != nil {
			return fmt.Errorf("unmarshal old key: %w", err)
		}

		nowStr := time.Now().UTC().Format(time.RFC3339)

		// 1. 旧キーを rotating に更新
		oldDocUpdates := []firestore.Update{
			{Path: "status", Value: string(entity.StatusRotating)},
			{Path: "rotation_meta", Value: entity.RotationMeta{
				OldKeyHash:           params.OldKeyHash,
				GracePeriodExpiresAt: params.GracePeriodExpiresAt,
			}},
			{Path: "updated_at", Value: nowStr},
		}
		if err := tx.Update(oldDocRef, oldDocUpdates); err != nil {
			return fmt.Errorf("update old key: %w", err)
		}

		// 2. 新キーを作成して保存
		baseKey := fromDoc(&oldDoc)
		newKey = *baseKey
		newKey.PK = "KEY#" + params.NewKeyHash
		newKey.KeyPrefix = params.NewKeyPrefix
		newKey.Status = entity.StatusActive
		newKey.IsActive = true
		newKey.Rotation = nil
		newKey.UpdatedAt = nowStr

		newDocRef := r.client.Collection(r.collection).Doc(params.NewKeyHash)
		if err := tx.Set(newDocRef, toDoc(&newKey)); err != nil {
			return fmt.Errorf("set new key: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("firestore RotateKey transaction: %w", err)
	}

	return &newKey, nil
}

// DeleteKey はキーを物理削除する。
func (r *FirestoreRepository) DeleteKey(ctx context.Context, keyHash string) error {
	_, err := r.client.Collection(r.collection).Doc(keyHash).Delete(ctx)
	if err != nil {
		return fmt.Errorf("firestore DeleteKey: %w", err)
	}
	return nil
}

// IncrementMonthlyUsage は当月消費カウントをアトミックにインクリメントする。
func (r *FirestoreRepository) IncrementMonthlyUsage(ctx context.Context, keyHash, currentMonth string, delta int64) (int64, error) {
	var finalUsage int64

	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		docRef := r.client.Collection(r.collection).Doc(keyHash)
		docSnap, err := tx.Get(docRef)
		if err != nil {
			return fmt.Errorf("get key for usage: %w", err)
		}

		var doc firestoreKeyDoc
		if err := docSnap.DataTo(&doc); err != nil {
			return fmt.Errorf("unmarshal key doc: %w", err)
		}

		nowStr := time.Now().UTC().Format(time.RFC3339)
		if doc.CurrentMonth == currentMonth {
			finalUsage = doc.CurrentMonthUsage + delta
		} else {
			doc.CurrentMonth = currentMonth
			finalUsage = delta
		}

		updates := []firestore.Update{
			{Path: "current_month", Value: doc.CurrentMonth},
			{Path: "current_month_usage", Value: finalUsage},
			{Path: "updated_at", Value: nowStr},
		}

		return tx.Update(docRef, updates)
	})

	if err != nil {
		return 0, fmt.Errorf("firestore IncrementMonthlyUsage transaction: %w", err)
	}

	return finalUsage, nil
}

// UpdateLastUsedAt は最終利用日時を記録する。
func (r *FirestoreRepository) UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error {
	updates := []firestore.Update{
		{Path: "last_used_at", Value: lastUsed.UTC()},
	}
	_, err := r.client.Collection(r.collection).Doc(keyHash).Update(ctx, updates)
	if err != nil {
		return fmt.Errorf("firestore UpdateLastUsedAt: %w", err)
	}
	return nil
}

// Ping は Firestore への疎通確認を行う。
func (r *FirestoreRepository) Ping(ctx context.Context) error {
	iter := r.client.Collection(r.collection).Limit(1).Documents(ctx)
	defer iter.Stop()
	_, err := iter.Next()
	if err != nil && err != iterator.Done {
		return fmt.Errorf("firestore ping: %w", err)
	}
	return nil
}
