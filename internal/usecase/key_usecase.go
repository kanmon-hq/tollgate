package usecase

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
)

type KeyUsecase struct {
	repo repository.KeyRepository
}

func NewKeyUsecase(repo repository.KeyRepository) *KeyUsecase {
	return &KeyUsecase{repo: repo}
}

const (
	RawKeyPrefix    = "tlge-live-"
	KeyPrefixLength = len(RawKeyPrefix) + 4 // 例: "tlge-live-8f9c" (14文字)
)

// ExtractKeyPrefix は安全にプレフィックス部分を抽出する
func ExtractKeyPrefix(rawKey string) string {
	if len(rawKey) < KeyPrefixLength {
		return rawKey
	}
	return rawKey[:KeyPrefixLength]
}

// GenerateRawKey は安全な暗号乱数を用いて "tlge-live-<32文字hex>" 形式の平文キーを生成する
func GenerateRawKey() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("crypto rand read error: %w", err)
	}
	return fmt.Sprintf("%s%s", RawKeyPrefix, hex.EncodeToString(bytes)), nil
}

// HashKey は平文キーの HMAC-SHA256 ダイジェスト文字列を生成する
func HashKey(rawKey string) string {
	secret := os.Getenv("API_KEY_HASH_SECRET")
	if secret == "" {
		// フォールバック: 環境変数未設定時でも挙動を維持（本番では必ず設定すること）
		secret = "default-api-key-hash-secret-change-me"
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(rawKey))
	return hex.EncodeToString(mac.Sum(nil))
}

// CreateKey は新しい API キーを発行し、DynamoDB にハッシュを保存した上で平文キーを1度だけ返却する
func (u *KeyUsecase) CreateKey(ctx context.Context, input entity.CreateKeyInput) (*entity.CreateKeyOutput, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}

	rawKey, err := GenerateRawKey()
	if err != nil {
		return nil, err
	}

	keyHash := HashKey(rawKey)
	keyID := uuid.New().String()
	keyPrefix := ExtractKeyPrefix(rawKey)

	rpm := input.RateLimitRPM
	if rpm <= 0 {
		rpm = 600
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	monthStr := now.Format("2006-01")

	var expiresAt *int64
	if input.ExpiresIn > 0 {
		exp := now.Add(time.Duration(input.ExpiresIn) * time.Second).Unix()
		expiresAt = &exp
	}

	apiKey := &entity.APIKey{
		PK:                "KEY#" + keyHash,
		KeyID:             keyID,
		KeyPrefix:         keyPrefix,
		Name:              input.Name,
		TenantID:          input.TenantID,
		ServiceID:         input.ServiceID,
		Scopes:            input.Scopes,
		RateLimitRPM:      rpm,
		MonthlyQuota:      input.MonthlyQuota,
		CurrentMonthUsage: 0,
		CurrentMonth:      monthStr,
		Status:            entity.StatusActive,
		IsActive:          true,
		ExpiresAt:         expiresAt,
		ItemType:          "KEY",
		CreatedAt:         nowStr,
		UpdatedAt:         nowStr,
	}

	if err := u.repo.PutKey(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("failed to save api key: %w", err)
	}

	return &entity.CreateKeyOutput{
		APIKey: *apiKey,
		RawKey: rawKey,
	}, nil
}

// ListKeys は指定テナントのキー一覧を取得する (後方互換)
func (u *KeyUsecase) ListKeys(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	return u.repo.ListKeysByTenant(ctx, tenantID)
}

// ListKeysPaged はページネーションに対応したキー一覧を取得する
func (u *KeyUsecase) ListKeysPaged(ctx context.Context, params entity.ListKeysParams) (*entity.ListKeysResult, error) {
	return u.repo.ListKeys(ctx, params)
}

// GetStats は指定テナント・サービス (空文字の場合は全件) の統計サマリーを取得する
func (u *KeyUsecase) GetStats(ctx context.Context, tenantID, serviceID string) (*entity.KeyStats, error) {
	return u.repo.GetStats(ctx, tenantID, serviceID)
}

// GetKey は KeyID でキー詳細を取得する
func (u *KeyUsecase) GetKey(ctx context.Context, keyID string) (*entity.APIKey, error) {
	key, err := u.repo.GetKeyByID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, fmt.Errorf("key not found: %s", keyID)
	}
	return key, nil
}

// UpdateKey はキー設定値 (名前、スコープ、RPM、月間クォータ) を更新する
func (u *KeyUsecase) UpdateKey(ctx context.Context, keyID string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	key, err := u.GetKey(ctx, keyID)
	if err != nil {
		return nil, err
	}

	keyHash := key.GetHash() // "KEY#" プレフィックスを除去
	return u.repo.UpdateKeySettings(ctx, keyHash, input)
}

// SuspendKey はキーを一時停止する (通信を即座に遮断)
func (u *KeyUsecase) SuspendKey(ctx context.Context, keyID string) (*entity.APIKey, error) {
	key, err := u.GetKey(ctx, keyID)
	if err != nil {
		return nil, err
	}

	keyHash := key.GetHash()
	if err := u.repo.UpdateKeyStatus(ctx, keyHash, entity.StatusSuspended, false); err != nil {
		return nil, err
	}
	key.Status = entity.StatusSuspended
	key.IsActive = false
	return key, nil
}

// ResumeKey は一時停止中のキーを再開する
func (u *KeyUsecase) ResumeKey(ctx context.Context, keyID string) (*entity.APIKey, error) {
	key, err := u.GetKey(ctx, keyID)
	if err != nil {
		return nil, err
	}

	keyHash := key.GetHash()
	if err := u.repo.UpdateKeyStatus(ctx, keyHash, entity.StatusActive, true); err != nil {
		return nil, err
	}
	key.Status = entity.StatusActive
	key.IsActive = true
	return key, nil
}

// RotateKey はゼロダウンタイム・キーローテーションを実行する
func (u *KeyUsecase) RotateKey(ctx context.Context, keyID string, input entity.RotateKeyInput) (*entity.RotateKeyOutput, error) {
	key, err := u.GetKey(ctx, keyID)
	if err != nil {
		return nil, err
	}

	oldKeyHash := key.GetHash()
	newRawKey, err := GenerateRawKey()
	if err != nil {
		return nil, err
	}
	newKeyHash := HashKey(newRawKey)
	newPrefix := ExtractKeyPrefix(newRawKey)

	hours := input.GracePeriodHours
	if hours <= 0 {
		hours = 24
	}
	graceExpires := time.Now().UTC().Add(time.Duration(hours) * time.Hour)

	_, err = u.repo.RotateKey(ctx, entity.RotateKeyParams{
		OldKeyHash:           oldKeyHash,
		NewKeyHash:           newKeyHash,
		NewKeyPrefix:         newPrefix,
		GracePeriodExpiresAt: graceExpires,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to rotate key: %w", err)
	}

	return &entity.RotateKeyOutput{
		KeyID:                keyID,
		KeyPrefix:            newPrefix,
		NewRawKey:            newRawKey,
		GracePeriodExpiresAt: graceExpires,
	}, nil
}

// DeleteKey はキーを完全削除する
func (u *KeyUsecase) DeleteKey(ctx context.Context, keyID string) error {
	key, err := u.GetKey(ctx, keyID)
	if err != nil {
		return err
	}
	keyHash := key.GetHash()
	return u.repo.DeleteKey(ctx, keyHash)
}

// RevokeTenantKeys はテナントに紐づくすべてのキーを一括失効する (要件 T-4, C-4: 冪等性保証)
func (u *KeyUsecase) RevokeTenantKeys(ctx context.Context, tenantID string) (*entity.RevokeTenantKeysOutput, error) {
	trimmed := strings.TrimSpace(tenantID)
	if trimmed == "" {
		return nil, errors.New("tenant_id is required")
	}

	count, err := u.repo.RevokeKeysByTenantID(ctx, trimmed)
	if err != nil {
		return nil, fmt.Errorf("failed to revoke keys for tenant %s: %w", trimmed, err)
	}

	return &entity.RevokeTenantKeysOutput{
		TenantID:     trimmed,
		RevokedCount: count,
		Status:       "revoked",
	}, nil
}


