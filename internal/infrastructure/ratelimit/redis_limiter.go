package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/repository"
	"github.com/redis/go-redis/v9"
)

// slidingWindowLuaScript は Redis Sorted Set を使ったスライディングウィンドウ実装の Lua スクリプト。
// Lua スクリプトは Redis 上でアトミックに実行されるため、分散環境での競合が発生しない。
//
// KEYS[1]: rate limit キー (例: "rate:<keyID>")
// ARGV[1]: ウィンドウサイズ (ミリ秒)
// ARGV[2]: 現在時刻 (ミリ秒)
// ARGV[3]: RPM 上限
//
// 戻り値: [allowed (1|0), remaining, reset_in_ms]
var slidingWindowLuaScript = redis.NewScript(`
local key    = KEYS[1]
local window = tonumber(ARGV[1])
local now    = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
local cutoff = now - window

-- 期限切れエントリを削除
redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)

-- 現在ウィンドウ内のカウント
local count = tonumber(redis.call('ZCARD', key))

if count >= limit then
    -- 最古エントリからリセット所要時間を算出
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local reset_in = window
    if #oldest >= 2 then
        reset_in = math.max(tonumber(oldest[2]) + window - now, 1)
    end
    return {0, 0, reset_in}
end

-- 現在リクエストを追加
-- member に count を付与することで同一ミリ秒の複数リクエストを区別する
-- (Lua スクリプトはアトミックなため count は呼び出しごとに一意)
redis.call('ZADD', key, now, now .. ':' .. count)
-- キーの有効期限をウィンドウサイズ + バッファで更新
redis.call('PEXPIRE', key, window + 1000)

local remaining = limit - count - 1
return {1, remaining, window}
`)

// RedisRateLimiter は Redis Sorted Set + Lua スクリプトによる
// スライディングウィンドウ方式のレートリミッター。
// DynamoDB の Fixed Window より精度が高く、ウィンドウ境界での突き抜けが発生しない。
//
// # 依存サービス
//
//   - Redis 6.0 以上 (LMPOP 等不要のため 6.0 で十分)
//   - ElastiCache / Upstash / Redis OSS いずれも利用可
//
// # フェイルオープン
//
// Redis 障害時はフェイルオープン (allowed=true, err=非nil) とする。
// 可用性優先のため。厳密な制限が必要な場合はフェイルクローズに変更すること。
type RedisRateLimiter struct {
	client *redis.Client
	window time.Duration
}

var _ repository.RateLimiter = (*RedisRateLimiter)(nil)

// NewRedisRateLimiter は Redis バックエンドのスライディングウィンドウ・レートリミッターを生成する。
func NewRedisRateLimiter(client *redis.Client, window time.Duration) *RedisRateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &RedisRateLimiter{
		client: client,
		window: window,
	}
}

// Allow は keyID に対応する Redis キーのスライディングウィンドウカウントを Atomic にインクリメントし、
// limitRPM を超えている場合は false を返す。
// Redis エラー時はフェイルオープン (allowed=true, err=非nil) とする。
func (r *RedisRateLimiter) Allow(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
	if limitRPM <= 0 {
		return true, 999999, 0, nil
	}

	now := time.Now().UnixMilli()
	windowMs := r.window.Milliseconds()
	key := "rate:" + id

	vals, err := slidingWindowLuaScript.Run(ctx, r.client, []string{key},
		windowMs, now, limitRPM,
	).Slice()
	if err != nil {
		// フェイルオープン: Redis 障害時はレートリミットをスキップ
		return true, 0, 0, fmt.Errorf("redis rate limiter error (fail-open): %w", err)
	}

	if len(vals) < 3 {
		return true, 0, 0, fmt.Errorf("redis rate limiter: unexpected response length %d (fail-open)", len(vals))
	}

	allowed := toInt64(vals[0]) == 1
	remaining := int(toInt64(vals[1]))
	resetIn := time.Duration(toInt64(vals[2])) * time.Millisecond

	return allowed, remaining, resetIn, nil
}

func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	default:
		return 0
	}
}
