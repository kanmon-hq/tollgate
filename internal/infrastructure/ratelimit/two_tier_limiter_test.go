package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)


type mockRateLimiter struct {
	allowFunc func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error)
}

func (m *mockRateLimiter) Allow(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
	return m.allowFunc(ctx, id, limitRPM)
}

func TestTwoTierRateLimiter(t *testing.T) {
	ctx := context.Background()

	t.Run("L1 allows and L2 allows", func(t *testing.T) {
		l1 := NewInMemoryRateLimiter(time.Minute)
		defer l1.Stop()

		l2Called := false
		l2 := &mockRateLimiter{
			allowFunc: func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
				l2Called = true
				return true, 4, time.Minute, nil
			},
		}

		twoTier := NewTwoTierRateLimiter(l1, l2)
		defer twoTier.Stop()

		allowed, remaining, _, err := twoTier.Allow(ctx, "key-1", 5)
		if err != nil || !allowed || !l2Called {
			t.Fatalf("expected allow without error, l2Called=%v, err=%v", l2Called, err)
		}
		if remaining != 4 {
			t.Errorf("expected remaining=4, got %d", remaining)
		}
	})

	t.Run("L1 blocks without calling L2", func(t *testing.T) {
		l1 := NewInMemoryRateLimiter(time.Minute)
		defer l1.Stop()

		l2Called := false
		l2 := &mockRateLimiter{
			allowFunc: func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
				l2Called = true
				return true, 10, time.Minute, nil
			},
		}

		twoTier := NewTwoTierRateLimiter(l1, l2)
		defer twoTier.Stop()

		// 消費 limit 1
		twoTier.Allow(ctx, "key-exhaust", 1)

		// 2回目は L1 で遮断されるため、L2 は呼ばれない
		l2Called = false
		allowed, _, _, err := twoTier.Allow(ctx, "key-exhaust", 1)
		if allowed || err != nil {
			t.Fatalf("expected blocked by L1, got allowed=%v, err=%v", allowed, err)
		}
		if l2Called {
			t.Fatalf("expected L2 not to be called, but was called")
		}
	})

	t.Run("L2 fails open on error", func(t *testing.T) {
		l1 := NewInMemoryRateLimiter(time.Minute)
		defer l1.Stop()

		l2 := &mockRateLimiter{
			allowFunc: func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
				return true, 0, 0, errors.New("redis connection down")
			},
		}

		twoTier := NewTwoTierRateLimiter(l1, l2)
		defer twoTier.Stop()

		allowed, _, _, err := twoTier.Allow(ctx, "key-failopen", 5)
		if !allowed || err == nil {
			t.Fatalf("expected allowed=true with fail-open error, got allowed=%v, err=%v", allowed, err)
		}
	})

	t.Run("L2 blocks request (allowed=false)", func(t *testing.T) {
		l1 := NewInMemoryRateLimiter(time.Minute)
		defer l1.Stop()

		l2 := &mockRateLimiter{
			allowFunc: func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
				return false, 0, 30 * time.Second, nil
			},
		}

		twoTier := NewTwoTierRateLimiter(l1, l2)
		defer twoTier.Stop()

		allowed, remaining, resetIn, err := twoTier.Allow(ctx, "key-l2-block", 5)
		if allowed || remaining != 0 || resetIn != 30*time.Second || err != nil {
			t.Fatalf("expected blocked by L2, got allowed=%v, remaining=%d, resetIn=%v, err=%v", allowed, remaining, resetIn, err)
		}
	})

	t.Run("Unlimited (limitRPM <= 0)", func(t *testing.T) {
		l1 := NewInMemoryRateLimiter(time.Minute)
		defer l1.Stop()

		twoTier := NewTwoTierRateLimiter(l1, nil)
		defer twoTier.Stop()

		allowed, remaining, resetIn, err := twoTier.Allow(ctx, "key-unlimited", 0)
		if !allowed || remaining != 999999 || resetIn != 0 || err != nil {
			t.Fatalf("expected unlimited allow, got %v, %d, %v, %v", allowed, remaining, resetIn, err)
		}
	})

	t.Run("L1 remaining < L2 remaining", func(t *testing.T) {
		l1 := NewInMemoryRateLimiter(time.Minute)
		defer l1.Stop()

		l2 := &mockRateLimiter{
			allowFunc: func(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
				return true, 10, time.Minute, nil
			},
		}

		twoTier := NewTwoTierRateLimiter(l1, l2)
		defer twoTier.Stop()

		allowed, remaining, _, err := twoTier.Allow(ctx, "key-rem-test", 2)
		if !allowed || remaining != 1 || err != nil {
			t.Fatalf("expected smaller remaining 1, got remaining=%d, allowed=%v, err=%v", remaining, allowed, err)
		}
	})
}
