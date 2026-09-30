package ratelimit

import (
	"context"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/repository"
)

// InMemoryRateLimiter は SlidingWindowLimiter を repository.RateLimiter に適合させるアダプタ。
// シングルインスタンス構成向け。スケールアウト時は DynamoDBRateLimiter に切り替えること。
type InMemoryRateLimiter struct {
	limiter *SlidingWindowLimiter
}

var _ repository.RateLimiter = (*InMemoryRateLimiter)(nil)

// NewInMemoryRateLimiter は SlidingWindowLimiter をラップしたインメモリ実装を返す。
func NewInMemoryRateLimiter(window time.Duration) *InMemoryRateLimiter {
	return &InMemoryRateLimiter{
		limiter: NewSlidingWindowLimiter(window),
	}
}

// Allow は SlidingWindowLimiter に委譲する。バックエンドエラーは発生しない (err は常に nil)。
func (r *InMemoryRateLimiter) Allow(_ context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
	allowed, remaining, resetIn := r.limiter.Allow(id, limitRPM)
	return allowed, remaining, resetIn, nil
}

// Stop はバックグラウンドの GC Goroutine を安全に停止する。
func (r *InMemoryRateLimiter) Stop() {
	r.limiter.Stop()
}
