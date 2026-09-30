package repository

import (
	"context"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
)

// KeyRepository は API キー永続化層のインターフェース
type KeyRepository interface {
	// PutKey は新しい API キーを保存する
	PutKey(ctx context.Context, key *entity.APIKey) error

	// GetKeyByHash は SHA-256 ハッシュ (PK: KEY#<hash>) でキーを高速取得する
	GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error)

	// GetKeyByID は KeyID (UUID) でキーを検索する
	GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error)

	// ListKeysByTenant は GSI (GSI_TenantKeys) を使用して指定テナントのキー一覧を取得する
	ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error)

	// UpdateKeyStatus はキーのステータスおよび有効フラグを更新する (suspend / resume / revoke)
	UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error

	// UpdateKeySettings はキーの設定値 (名前、スコープ、RPM、月間クォータ) を更新する
	UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error)

	// RotateKey はローテーションを実行し、旧キーのメタデータを引き継いで新キーを保存する
	RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error)

	// DeleteKey はキーを物理削除する
	DeleteKey(ctx context.Context, keyHash string) error

	// IncrementMonthlyUsage は当月消費カウントを Atomic にインクリメントする
	IncrementMonthlyUsage(ctx context.Context, keyHash string, month string, increment int64) (int64, error)

	// UpdateLastUsedAt は最終利用日時を記録する
	UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error

	// Ping は DynamoDB への疎通確認を行う
	Ping(ctx context.Context) error
}
