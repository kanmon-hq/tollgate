package ratelimit

import (
	"context"
	"time"

	"github.com/northfieldzz/tollgate/internal/domain/repository"
)

// TwoTierRateLimiter は L1 (プロセス内 In-Memory) と L2 (分散 Redis / Valkey) を組み合わせた
// 2段キャッシュ構成のレートリミッター。
//
// 1. L1 (ローカル In-Memory) で即座にローカルカウンターをチェック。
//    すでにノード単体で RPM 上限を超過している場合は、L2 へのアクセスを行わずに即座に 429 を返却し、
//    Redis / Valkey への高負荷やネットワーク I/O を抑止する。
// 2. L1 を通過した場合、L2 (Redis / Valkey) でクラスタ全体のアトミックな判定を実行する。
// 3. L2 が超過と判定した場合、L1 側でもリミット到達状態として記録する。
// 4. L2 で障害が発生した場合はフェイルオープンし、L1 のみで判定を継続する。
type TwoTierRateLimiter struct {
	l1 *InMemoryRateLimiter
	l2 repository.RateLimiter
}

var _ repository.RateLimiter = (*TwoTierRateLimiter)(nil)

// NewTwoTierRateLimiter は 2段レートリミッターを生成する。
func NewTwoTierRateLimiter(l1 *InMemoryRateLimiter, l2 repository.RateLimiter) *TwoTierRateLimiter {
	return &TwoTierRateLimiter{
		l1: l1,
		l2: l2,
	}
}

// Allow は L1 (In-Memory) -> L2 (Redis / Valkey) の順でレートリミットを判定する。
func (t *TwoTierRateLimiter) Allow(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
	if limitRPM <= 0 {
		return true, 999999, 0, nil
	}

	// 1. L1 (ローカルメモリ) で高速判定
	l1Allowed, l1Remaining, l1ResetIn, _ := t.l1.Allow(ctx, id, limitRPM)
	if !l1Allowed {
		// ローカルで既に上限超過している場合は L2 (Redis) に問い合わせずに即座に遮断
		return false, l1Remaining, l1ResetIn, nil
	}

	// 2. L2 (分散 Redis / Valkey) でクラスタ全体のアトミック判定
	l2Allowed, l2Remaining, l2ResetIn, err := t.l2.Allow(ctx, id, limitRPM)
	if err != nil {
		// L2 障害時はフェイルオープン（L1 の判定結果を採用）
		return l1Allowed, l1Remaining, l1ResetIn, err
	}

	if !l2Allowed {
		// クラスタ全体で上限超過した場合は L2 の結果（429）を返却
		return false, l2Remaining, l2ResetIn, nil
	}

	// 最小の remaining を返却
	remaining := l2Remaining
	if l1Remaining < remaining {
		remaining = l1Remaining
	}

	return true, remaining, l2ResetIn, nil
}

// Stop は L1 のバックグラウンド GC ルーチンを停止する。
func (t *TwoTierRateLimiter) Stop() {
	if t.l1 != nil {
		t.l1.Stop()
	}
}
