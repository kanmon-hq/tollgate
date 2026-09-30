package sqlrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
)

// SQLRepository は database/sql を使う SQLite 向け KeyRepository 実装。
type SQLRepository struct {
	db *sql.DB
}

var _ repository.KeyRepository = (*SQLRepository)(nil)

// selectFields は SELECT/RETURNING で使うカラム一覧
const selectFields = `
    pk, key_id, key_prefix, name, tenant_id, service_id, scopes,
    status, is_active, rate_limit_rpm, monthly_quota,
    current_month, current_month_usage, expires_at, rotation_meta,
    last_used_at, created_at, updated_at`

// ─── 書き込みヘルパー ─────────────────────────────────────────────────────────

func marshalScopes(scopes []string) string {
	b, _ := json.Marshal(scopes)
	if b == nil {
		return "[]"
	}
	return string(b)
}

func marshalRotationMeta(rm *entity.RotationMeta) sql.NullString {
	if rm == nil {
		return sql.NullString{}
	}
	b, _ := json.Marshal(rm)
	return sql.NullString{String: string(b), Valid: true}
}

func marshalLastUsedAt(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
}

func marshalExpiresAt(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ─── 読み取りヘルパー ─────────────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanKey(row scanner) (*entity.APIKey, error) {
	var (
		pk, keyID, keyPrefix, name, tenantID, serviceID string
		scopesJSON, status                               string
		isActiveInt                                      int64
		rateLimitRPM                                     int
		monthlyQuota, currentMonthUsage                  int64
		currentMonth                                     string
		expiresAt                                        sql.NullInt64
		rotationMetaJSON                                 sql.NullString
		lastUsedAt                                       sql.NullString
		createdAt, updatedAt                             string
	)

	if err := row.Scan(
		&pk, &keyID, &keyPrefix, &name, &tenantID, &serviceID,
		&scopesJSON, &status, &isActiveInt, &rateLimitRPM,
		&monthlyQuota, &currentMonth, &currentMonthUsage,
		&expiresAt, &rotationMetaJSON, &lastUsedAt,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	key := &entity.APIKey{
		PK:                pk,
		KeyID:             keyID,
		KeyPrefix:         keyPrefix,
		Name:              name,
		TenantID:          tenantID,
		ServiceID:         serviceID,
		Status:            entity.KeyStatus(status),
		IsActive:          isActiveInt != 0,
		RateLimitRPM:      rateLimitRPM,
		MonthlyQuota:      monthlyQuota,
		CurrentMonthUsage: currentMonthUsage,
		CurrentMonth:      currentMonth,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}

	_ = json.Unmarshal([]byte(scopesJSON), &key.Scopes)
	if key.Scopes == nil {
		key.Scopes = []string{}
	}
	if expiresAt.Valid {
		v := expiresAt.Int64
		key.ExpiresAt = &v
	}
	if rotationMetaJSON.Valid && rotationMetaJSON.String != "" {
		var rm entity.RotationMeta
		if err := json.Unmarshal([]byte(rotationMetaJSON.String), &rm); err == nil {
			key.Rotation = &rm
		}
	}
	if lastUsedAt.Valid && lastUsedAt.String != "" {
		t, err := time.Parse(time.RFC3339, lastUsedAt.String)
		if err == nil {
			key.LastUsedAt = &t
		}
	}

	return key, nil
}

// ─── KeyRepository 実装 ───────────────────────────────────────────────────────

func (r *SQLRepository) PutKey(ctx context.Context, key *entity.APIKey) error {
	q := `
		INSERT INTO api_keys (
			pk, key_id, key_prefix, name, tenant_id, service_id, scopes,
			status, is_active, rate_limit_rpm, monthly_quota,
			current_month, current_month_usage, expires_at, rotation_meta,
			last_used_at, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(pk) DO UPDATE SET
			key_id=excluded.key_id, key_prefix=excluded.key_prefix, name=excluded.name,
			tenant_id=excluded.tenant_id, service_id=excluded.service_id, scopes=excluded.scopes,
			status=excluded.status, is_active=excluded.is_active, rate_limit_rpm=excluded.rate_limit_rpm,
			monthly_quota=excluded.monthly_quota, current_month=excluded.current_month,
			current_month_usage=excluded.current_month_usage, expires_at=excluded.expires_at,
			rotation_meta=excluded.rotation_meta, last_used_at=excluded.last_used_at,
			created_at=excluded.created_at, updated_at=excluded.updated_at`

	_, err := r.db.ExecContext(ctx, q,
		key.PK, key.KeyID, key.KeyPrefix, key.Name, key.TenantID, key.ServiceID,
		marshalScopes(key.Scopes), string(key.Status), boolToInt(key.IsActive), key.RateLimitRPM,
		key.MonthlyQuota, key.CurrentMonth, key.CurrentMonthUsage,
		marshalExpiresAt(key.ExpiresAt), marshalRotationMeta(key.Rotation),
		marshalLastUsedAt(key.LastUsedAt), key.CreatedAt, key.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("sqlrepo PutKey: %w", err)
	}
	return nil
}

func (r *SQLRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	pk := "KEY#" + keyHash
	q := `SELECT` + selectFields + ` FROM api_keys WHERE pk = ?`
	row := r.db.QueryRowContext(ctx, q, pk)
	key, err := scanKey(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlrepo GetKeyByHash: %w", err)
	}
	return key, nil
}

func (r *SQLRepository) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	q := `SELECT` + selectFields + ` FROM api_keys WHERE key_id = ? LIMIT 1`
	row := r.db.QueryRowContext(ctx, q, keyID)
	key, err := scanKey(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlrepo GetKeyByID: %w", err)
	}
	return key, nil
}

func (r *SQLRepository) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	q := `SELECT` + selectFields + ` FROM api_keys WHERE tenant_id = ? ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("sqlrepo ListKeysByTenant: %w", err)
	}
	defer rows.Close()

	var keys []*entity.APIKey
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlrepo ListKeysByTenant scan: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (r *SQLRepository) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	pk := "KEY#" + keyHash
	now := time.Now().UTC().Format(time.RFC3339)
	q := `UPDATE api_keys SET status = ?, is_active = ?, updated_at = ? WHERE pk = ?`
	_, err := r.db.ExecContext(ctx, q, string(status), boolToInt(isActive), now, pk)
	if err != nil {
		return fmt.Errorf("sqlrepo UpdateKeyStatus: %w", err)
	}
	return nil
}

func (r *SQLRepository) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	pk := "KEY#" + keyHash
	now := time.Now().UTC().Format(time.RFC3339)

	sets := []string{"updated_at = ?"}
	args := []interface{}{now}

	if input.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *input.Name)
	}
	if input.Scopes != nil {
		sets = append(sets, "scopes = ?")
		args = append(args, marshalScopes(*input.Scopes))
	}
	if input.RateLimitRPM != nil {
		sets = append(sets, "rate_limit_rpm = ?")
		args = append(args, *input.RateLimitRPM)
	}
	if input.MonthlyQuota != nil {
		sets = append(sets, "monthly_quota = ?")
		args = append(args, *input.MonthlyQuota)
	}
	args = append(args, pk)

	q := fmt.Sprintf("UPDATE api_keys SET %s WHERE pk = ?", strings.Join(sets, ", "))
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return nil, fmt.Errorf("sqlrepo UpdateKeySettings: %w", err)
	}
	return r.GetKeyByHash(ctx, keyHash)
}

func (r *SQLRepository) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlrepo RotateKey begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// 1. 旧キーを取得
	oldKey, err := scanKey(tx.QueryRowContext(ctx,
		`SELECT`+selectFields+` FROM api_keys WHERE pk = ?`,
		"KEY#"+params.OldKeyHash,
	))
	if err == sql.ErrNoRows || oldKey == nil {
		return nil, fmt.Errorf("old key not found")
	}
	if err != nil {
		return nil, fmt.Errorf("sqlrepo RotateKey get old: %w", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// 2. 旧キーを rotating に更新
	rm := entity.RotationMeta{
		OldKeyHash:           params.OldKeyHash,
		GracePeriodExpiresAt: params.GracePeriodExpiresAt,
	}
	rmJSON, _ := json.Marshal(rm)
	q := `UPDATE api_keys SET status = ?, rotation_meta = ?, updated_at = ? WHERE pk = ?`
	if _, err := tx.ExecContext(ctx, q, string(entity.StatusRotating), string(rmJSON), nowStr, "KEY#"+params.OldKeyHash); err != nil {
		return nil, fmt.Errorf("sqlrepo RotateKey update old: %w", err)
	}

	// 3. 新キーを挿入
	newKey := *oldKey
	newKey.PK = "KEY#" + params.NewKeyHash
	newKey.KeyPrefix = params.NewKeyPrefix
	newKey.Status = entity.StatusActive
	newKey.IsActive = true
	newKey.Rotation = nil
	newKey.UpdatedAt = nowStr

	insertQ := `
		INSERT INTO api_keys (
			pk, key_id, key_prefix, name, tenant_id, service_id, scopes,
			status, is_active, rate_limit_rpm, monthly_quota,
			current_month, current_month_usage, expires_at, rotation_meta,
			last_used_at, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(pk) DO UPDATE SET
			key_id=excluded.key_id, key_prefix=excluded.key_prefix, name=excluded.name,
			status=excluded.status, is_active=excluded.is_active, updated_at=excluded.updated_at`

	if _, err := tx.ExecContext(ctx, insertQ,
		newKey.PK, newKey.KeyID, newKey.KeyPrefix, newKey.Name, newKey.TenantID, newKey.ServiceID,
		marshalScopes(newKey.Scopes), string(newKey.Status), boolToInt(newKey.IsActive), newKey.RateLimitRPM,
		newKey.MonthlyQuota, newKey.CurrentMonth, newKey.CurrentMonthUsage,
		marshalExpiresAt(newKey.ExpiresAt), marshalRotationMeta(newKey.Rotation),
		marshalLastUsedAt(newKey.LastUsedAt), newKey.CreatedAt, newKey.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("sqlrepo RotateKey insert new: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlrepo RotateKey commit: %w", err)
	}
	return &newKey, nil
}

func (r *SQLRepository) DeleteKey(ctx context.Context, keyHash string) error {
	pk := "KEY#" + keyHash
	q := `DELETE FROM api_keys WHERE pk = ?`
	_, err := r.db.ExecContext(ctx, q, pk)
	if err != nil {
		return fmt.Errorf("sqlrepo DeleteKey: %w", err)
	}
	return nil
}

func (r *SQLRepository) IncrementMonthlyUsage(ctx context.Context, keyHash, currentMonth string, delta int64) (int64, error) {
	pk := "KEY#" + keyHash
	// 月が変わっていればカウントをリセット、同月なら加算 (1 クエリでアトミック)
	q := `
		UPDATE api_keys
		SET
			current_month_usage = CASE WHEN current_month = ? THEN current_month_usage + ? ELSE ? END,
			current_month = ?
		WHERE pk = ?`

	if _, err := r.db.ExecContext(ctx, q, currentMonth, delta, delta, currentMonth, pk); err != nil {
		return 0, fmt.Errorf("sqlrepo IncrementMonthlyUsage: %w", err)
	}

	var usage int64
	selQ := `SELECT current_month_usage FROM api_keys WHERE pk = ?`
	if err := r.db.QueryRowContext(ctx, selQ, pk).Scan(&usage); err != nil {
		return 0, fmt.Errorf("sqlrepo IncrementMonthlyUsage select: %w", err)
	}
	return usage, nil
}

func (r *SQLRepository) UpdateLastUsedAt(ctx context.Context, keyHash string, t time.Time) error {
	pk := "KEY#" + keyHash
	q := `UPDATE api_keys SET last_used_at = ? WHERE pk = ?`
	_, err := r.db.ExecContext(ctx, q, t.UTC().Format(time.RFC3339), pk)
	if err != nil {
		return fmt.Errorf("sqlrepo UpdateLastUsedAt: %w", err)
	}
	return nil
}

func (r *SQLRepository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// ─── スキーマ自動作成 ─────────────────────────────────────────────────────────

const createTableSQL = `
CREATE TABLE IF NOT EXISTS api_keys (
    pk                  TEXT PRIMARY KEY,
    key_id              TEXT NOT NULL,
    key_prefix          TEXT NOT NULL DEFAULT '',
    name                TEXT NOT NULL DEFAULT '',
    tenant_id           TEXT NOT NULL DEFAULT '',
    service_id          TEXT NOT NULL DEFAULT '',
    scopes              TEXT NOT NULL DEFAULT '[]',
    status              TEXT NOT NULL DEFAULT 'active',
    is_active           INTEGER NOT NULL DEFAULT 1,
    rate_limit_rpm      INTEGER NOT NULL DEFAULT 0,
    monthly_quota       BIGINT NOT NULL DEFAULT 0,
    current_month       TEXT NOT NULL DEFAULT '',
    current_month_usage BIGINT NOT NULL DEFAULT 0,
    expires_at          BIGINT,
    rotation_meta       TEXT,
    last_used_at        TEXT,
    created_at          TEXT NOT NULL DEFAULT '',
    updated_at          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant_id ON api_keys(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_id    ON api_keys(key_id);
`

// AutoMigrate はテーブルとインデックスを冪等に作成する。
func AutoMigrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("sqlrepo AutoMigrate: %w", err)
	}
	return nil
}
