package cache

import (
	"context"
	"testing"
	"time"

	"github.com/northfieldzz/tollgate/internal/domain/entity"
	"github.com/northfieldzz/tollgate/internal/domain/repository"
)

type mockRepo struct {
	repository.KeyRepository
	getCallCount int
	putCallCount int
	putErr       error
	keys         map[string]*entity.APIKey
}

func (m *mockRepo) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	m.getCallCount++
	key, ok := m.keys[keyHash]
	if !ok {
		return nil, nil
	}
	return key, nil
}

func (m *mockRepo) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	if key, ok := m.keys[keyHash]; ok {
		key.Status = status
		key.IsActive = isActive
	}
	return nil
}

func (m *mockRepo) PutKey(ctx context.Context, key *entity.APIKey) error {
	m.putCallCount++
	if m.putErr != nil {
		return m.putErr
	}
	m.keys[key.GetHash()] = key
	return nil
}

func TestCachedKeyRepository(t *testing.T) {
	mock := &mockRepo{
		keys: map[string]*entity.APIKey{
			"hash123": {
				PK:       "KEY#hash123",
				KeyID:    "key-1",
				IsActive: true,
				Status:   entity.StatusActive,
			},
		},
	}

	cachedRepo := NewCachedKeyRepository(mock, 100*time.Millisecond)

	ctx := context.Background()

	// 1回目: キャッシュミス -> underlying 呼び出し
	k1, err := cachedRepo.GetKeyByHash(ctx, "hash123")
	if err != nil || k1 == nil {
		t.Fatalf("expected key, got %v, err: %v", k1, err)
	}
	if mock.getCallCount != 1 {
		t.Errorf("expected 1 call, got %d", mock.getCallCount)
	}

	// 2回目: キャッシュヒット -> underlying は呼ばれない
	k2, err := cachedRepo.GetKeyByHash(ctx, "hash123")
	if err != nil || k2 == nil {
		t.Fatalf("expected key, got %v, err: %v", k2, err)
	}
	if mock.getCallCount != 1 {
		t.Errorf("expected still 1 call, got %d", mock.getCallCount)
	}

	// ステータス更新でキャッシュ無効化されることを検証
	_ = cachedRepo.UpdateKeyStatus(ctx, "hash123", entity.StatusSuspended, false)

	// 3回目: キャッシュ無効化されているため再度 underlying 呼び出し
	k3, err := cachedRepo.GetKeyByHash(ctx, "hash123")
	if err != nil || k3 == nil {
		t.Fatalf("expected key, got %v, err: %v", k3, err)
	}
	if mock.getCallCount != 2 {
		t.Errorf("expected 2 calls after invalidation, got %d", mock.getCallCount)
	}
	if k3.Status != entity.StatusSuspended {
		t.Errorf("expected status suspended, got %s", k3.Status)
	}
}

func TestCachedKeyRepository_PutKey(t *testing.T) {
	mock := &mockRepo{
		keys: map[string]*entity.APIKey{
			"hash123": {
				PK:       "KEY#hash123",
				KeyID:    "key-1",
				IsActive: true,
				Status:   entity.StatusActive,
			},
		},
	}

	cachedRepo := NewCachedKeyRepository(mock, 100*time.Millisecond)
	ctx := context.Background()

	// 1. Fetch key to warm up the cache
	_, err := cachedRepo.GetKeyByHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("unexpected error warming cache: %v", err)
	}
	if mock.getCallCount != 1 {
		t.Errorf("expected 1 get call, got %d", mock.getCallCount)
	}

	// 2. Modify key and PutKey
	newKey := &entity.APIKey{
		PK:       "KEY#hash123",
		KeyID:    "key-1",
		IsActive: false,                  // Changed
		Status:   entity.StatusSuspended, // Changed
	}
	err = cachedRepo.PutKey(ctx, newKey)
	if err != nil {
		t.Fatalf("unexpected error from PutKey: %v", err)
	}
	if mock.putCallCount != 1 {
		t.Errorf("expected 1 put call, got %d", mock.putCallCount)
	}

	// 3. Fetch again to verify cache was invalidated and new data is retrieved
	k2, err := cachedRepo.GetKeyByHash(ctx, "hash123")
	if err != nil || k2 == nil {
		t.Fatalf("expected key, got %v, err: %v", k2, err)
	}
	if mock.getCallCount != 2 {
		t.Errorf("expected 2 get calls after invalidation, got %d", mock.getCallCount)
	}
	if k2.IsActive != false || k2.Status != entity.StatusSuspended {
		t.Errorf("expected updated key status")
	}
}

func TestCachedKeyRepository_PutKey_Error(t *testing.T) {
	importErr := context.DeadlineExceeded // arbitrary error
	mock := &mockRepo{
		putErr: importErr,
		keys: map[string]*entity.APIKey{
			"hash123": {
				PK:       "KEY#hash123",
				KeyID:    "key-1",
				IsActive: true,
				Status:   entity.StatusActive,
			},
		},
	}

	cachedRepo := NewCachedKeyRepository(mock, 100*time.Millisecond)
	ctx := context.Background()

	// 1. Fetch key to warm up the cache
	_, err := cachedRepo.GetKeyByHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("unexpected error warming cache: %v", err)
	}

	// 2. PutKey should fail and return error
	newKey := &entity.APIKey{
		PK:       "KEY#hash123",
		KeyID:    "key-1",
		IsActive: false,                  // Changed
		Status:   entity.StatusSuspended, // Changed
	}
	err = cachedRepo.PutKey(ctx, newKey)
	if err != importErr {
		t.Fatalf("expected error %v, got %v", importErr, err)
	}

	// 3. Fetch again to verify cache was NOT invalidated (should be a cache hit)
	k2, err := cachedRepo.GetKeyByHash(ctx, "hash123")
	if err != nil || k2 == nil {
		t.Fatalf("expected key, got %v, err: %v", k2, err)
	}

	// getCallCount should still be 1 (cache hit)
	if mock.getCallCount != 1 {
		t.Errorf("expected 1 get call, got %d", mock.getCallCount)
	}

	// the cached value should still be the old one
	if k2.IsActive != true || k2.Status != entity.StatusActive {
		t.Errorf("expected old key status because cache shouldn't be invalidated")
	}
}

type fullMockRepo struct {
	repository.KeyRepository
	keyByIDCalled     bool
	listTenantCalled  bool
	updateSettingsCalled bool
	rotateCalled      bool
	deleteCalled      bool
	incMonthlyCalled  bool
	lastUsedCalled    bool
	pingCalled        bool
}

func (m *fullMockRepo) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	m.keyByIDCalled = true
	return &entity.APIKey{KeyID: keyID}, nil
}
func (m *fullMockRepo) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	m.listTenantCalled = true
	return []*entity.APIKey{{TenantID: tenantID}}, nil
}
func (m *fullMockRepo) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	m.updateSettingsCalled = true
	return &entity.APIKey{PK: "KEY#" + keyHash}, nil
}
func (m *fullMockRepo) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	m.rotateCalled = true
	return &entity.APIKey{PK: "KEY#" + params.NewKeyHash}, nil
}
func (m *fullMockRepo) DeleteKey(ctx context.Context, keyHash string) error {
	m.deleteCalled = true
	return nil
}
func (m *fullMockRepo) IncrementMonthlyUsage(ctx context.Context, keyHash, month string, delta int64) (int64, error) {
	m.incMonthlyCalled = true
	return delta, nil
}
func (m *fullMockRepo) UpdateLastUsedAt(ctx context.Context, keyHash string, t time.Time) error {
	m.lastUsedCalled = true
	return nil
}
func (m *fullMockRepo) Ping(ctx context.Context) error {
	m.pingCalled = true
	return nil
}

func TestCachedKeyRepository_DelegatedMethods(t *testing.T) {
	ctx := context.Background()
	mock := &fullMockRepo{}
	cached := NewCachedKeyRepository(mock, 100*time.Millisecond)

	_, _ = cached.GetKeyByID(ctx, "kid-1")
	if !mock.keyByIDCalled {
		t.Errorf("expected GetKeyByID to be delegated")
	}

	_, _ = cached.ListKeysByTenant(ctx, "t-1")
	if !mock.listTenantCalled {
		t.Errorf("expected ListKeysByTenant to be delegated")
	}

	_, _ = cached.UpdateKeySettings(ctx, "hash1", entity.UpdateKeyInput{})
	if !mock.updateSettingsCalled {
		t.Errorf("expected UpdateKeySettings to be delegated")
	}

	_, _ = cached.RotateKey(ctx, entity.RotateKeyParams{OldKeyHash: "old", NewKeyHash: "new"})
	if !mock.rotateCalled {
		t.Errorf("expected RotateKey to be delegated")
	}

	_ = cached.DeleteKey(ctx, "hash1")
	if !mock.deleteCalled {
		t.Errorf("expected DeleteKey to be delegated")
	}

	_, _ = cached.IncrementMonthlyUsage(ctx, "hash1", "2026-09", 1)
	if !mock.incMonthlyCalled {
		t.Errorf("expected IncrementMonthlyUsage to be delegated")
	}

	_ = cached.UpdateLastUsedAt(ctx, "hash1", time.Now())
	if !mock.lastUsedCalled {
		t.Errorf("expected UpdateLastUsedAt to be delegated")
	}

	_ = cached.Ping(ctx)
	if !mock.pingCalled {
		t.Errorf("expected Ping to be delegated")
	}
}
