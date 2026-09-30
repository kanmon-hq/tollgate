package cache

import (
	"context"
	"sync"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
)

type cacheItem struct {
	key       *entity.APIKey
	expiresAt time.Time
}

// CachedKeyRepository は KeyRepository をラップし、ホットパスである GetKeyByHash をインメモリキャッシュするデコレータ
type CachedKeyRepository struct {
	underlying repository.KeyRepository
	ttl        time.Duration
	mu         sync.RWMutex
	items      map[string]*cacheItem
}

// NewCachedKeyRepository は指定された TTL でインメモリキャッシュデコレータを初期化する
func NewCachedKeyRepository(underlying repository.KeyRepository, ttl time.Duration) repository.KeyRepository {
	if ttl <= 0 {
		// キャッシュ無効化時はそのまま underlying リポジトリを返却
		return underlying
	}
	return &CachedKeyRepository{
		underlying: underlying,
		ttl:        ttl,
		items:      make(map[string]*cacheItem),
	}
}

func (c *CachedKeyRepository) PutKey(ctx context.Context, key *entity.APIKey) error {
	err := c.underlying.PutKey(ctx, key)
	if err == nil {
		c.invalidate(key.GetHash()) // "KEY#" プレフィックスを除去したハッシュ
	}
	return err
}

func (c *CachedKeyRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	c.mu.RLock()
	item, ok := c.items[keyHash]
	if ok && time.Now().Before(item.expiresAt) {
		keyCopy := *item.key
		c.mu.RUnlock()
		return &keyCopy, nil
	}
	c.mu.RUnlock()

	// キャッシュミス: 下流リポジトリ (DynamoDB) から取得
	key, err := c.underlying.GetKeyByHash(ctx, keyHash)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, nil
	}

	c.mu.Lock()
	c.items[keyHash] = &cacheItem{
		key:       key,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	keyCopy := *key
	return &keyCopy, nil
}

func (c *CachedKeyRepository) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	return c.underlying.GetKeyByID(ctx, keyID)
}

func (c *CachedKeyRepository) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	return c.underlying.ListKeysByTenant(ctx, tenantID)
}

func (c *CachedKeyRepository) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	key, err := c.underlying.UpdateKeySettings(ctx, keyHash, input)
	if err == nil {
		c.invalidate(keyHash)
	}
	return key, err
}

func (c *CachedKeyRepository) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	err := c.underlying.UpdateKeyStatus(ctx, keyHash, status, isActive)
	if err == nil {
		c.invalidate(keyHash)
	}
	return err
}

func (c *CachedKeyRepository) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	key, err := c.underlying.RotateKey(ctx, params)
	if err == nil {
		c.invalidate(params.OldKeyHash)
		c.invalidate(params.NewKeyHash)
	}
	return key, err
}

func (c *CachedKeyRepository) DeleteKey(ctx context.Context, keyHash string) error {
	err := c.underlying.DeleteKey(ctx, keyHash)
	if err == nil {
		c.invalidate(keyHash)
	}
	return err
}

func (c *CachedKeyRepository) IncrementMonthlyUsage(ctx context.Context, keyHash, currentMonth string, delta int64) (int64, error) {
	return c.underlying.IncrementMonthlyUsage(ctx, keyHash, currentMonth, delta)
}

func (c *CachedKeyRepository) UpdateLastUsedAt(ctx context.Context, keyHash string, t time.Time) error {
	return c.underlying.UpdateLastUsedAt(ctx, keyHash, t)
}

func (c *CachedKeyRepository) Ping(ctx context.Context) error {
	return c.underlying.Ping(ctx)
}

func (c *CachedKeyRepository) invalidate(keyHash string) {
	c.mu.Lock()
	delete(c.items, keyHash)
	c.mu.Unlock()
}
