package ratelimit

import (
	"hash/fnv"
	"sync"
	"time"
)

const numShards = 64

type shard struct {
	mu      sync.Mutex
	windows map[string][]time.Time
}

// SlidingWindowLimiter は高並行・Goroutine セーフなインメモリ・スライディングウィンドウ式レートリミッター
// 64 個のシャードに分散された Mutex により高負荷時のロック競合を最小化する
type SlidingWindowLimiter struct {
	shards   [numShards]*shard
	window   time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewSlidingWindowLimiter(window time.Duration) *SlidingWindowLimiter {
	if window <= 0 {
		window = time.Minute
	}
	limiter := &SlidingWindowLimiter{
		window: window,
		stopCh: make(chan struct{}),
	}
	for i := 0; i < numShards; i++ {
		limiter.shards[i] = &shard{
			windows: make(map[string][]time.Time),
		}
	}

	// 定期的な古いキーのガベージコレクション (5分おき)
	limiter.wg.Add(1)
	go limiter.cleanupLoop(5 * time.Minute)

	return limiter
}

func (l *SlidingWindowLimiter) getShard(id string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return l.shards[h.Sum32()%numShards]
}

// Allow は指定された識別子 (KeyID 等) のリクエストを 1 件消費し、許可可否、残り件数、リセット所要時間を返す
func (l *SlidingWindowLimiter) Allow(id string, limitRPM int) (bool, int, time.Duration) {
	if limitRPM <= 0 {
		// 上限が 0 以下の場合は無制限
		return true, 999999, 0
	}

	s := l.getShard(id)
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	timestamps := s.windows[id]

	// 期限切れタイムスタンプをインプレースで切り詰め (スライスのヒープ再確保を排除)
	validStart := 0
	for validStart < len(timestamps) && !timestamps[validStart].After(cutoff) {
		validStart++
	}
	if validStart > 0 {
		copy(timestamps, timestamps[validStart:])
		timestamps = timestamps[:len(timestamps)-validStart]
	}

	if len(timestamps) >= limitRPM {
		// 上限到達
		oldest := timestamps[0]
		resetIn := oldest.Add(l.window).Sub(now)
		if resetIn < 0 {
			resetIn = time.Millisecond
		}
		s.windows[id] = timestamps
		return false, 0, resetIn
	}

	// 許可: 現在時刻を追加
	timestamps = append(timestamps, now)
	s.windows[id] = timestamps

	remaining := limitRPM - len(timestamps)
	return true, remaining, l.window
}

// Stop はバックグラウンドのクリーンアップ Goroutine を安全に停止する
func (l *SlidingWindowLimiter) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopCh)
		l.wg.Wait()
	})
}

func (l *SlidingWindowLimiter) cleanupLoop(interval time.Duration) {
	defer l.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			cutoff := now.Add(-l.window)
			for _, s := range l.shards {
				s.mu.Lock()
				keys := make([]string, 0, len(s.windows))
				for id := range s.windows {
					keys = append(keys, id)
				}
				s.mu.Unlock()

				for _, id := range keys {
					s.mu.Lock()
					if timestamps, exists := s.windows[id]; exists {
						validStart := 0
						for validStart < len(timestamps) && !timestamps[validStart].After(cutoff) {
							validStart++
						}
						if validStart >= len(timestamps) {
							delete(s.windows, id)
						} else if validStart > 0 {
							copy(timestamps, timestamps[validStart:])
							s.windows[id] = timestamps[:len(timestamps)-validStart]
						}
					}
					s.mu.Unlock()
				}
			}
		}
	}
}
