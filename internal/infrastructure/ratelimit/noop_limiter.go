package ratelimit

import (
	"context"
	"time"

	"github.com/northfieldzz/tollgate/internal/domain/repository"
)

// NoopRateLimiter はレートリミットをバイパス（無効化）する実装。
// RATE_LIMIT_BACKEND=none 時に使用され、常にリクエストを許可する。
type NoopRateLimiter struct{}

var _ repository.RateLimiter = (*NoopRateLimiter)(nil)

// NewNoopRateLimiter はレートリミット無効化用のリミッターを生成する。
func NewNoopRateLimiter() *NoopRateLimiter {
	return &NoopRateLimiter{}
}

// Allow は常に allowed=true を返す。
func (n *NoopRateLimiter) Allow(_ context.Context, _ string, limitRPM int) (bool, int, time.Duration, error) {
	return true, limitRPM, 0, nil
}
