package ratelimit

import (
	"testing"
	"time"
)

func TestSlidingWindowLimiter_Allow(t *testing.T) {
	limiter := NewSlidingWindowLimiter(time.Second) // 1秒ウィンドウでテスト

	id := "test-key-1"
	limit := 3

	// 1〜3回目: 許可
	for i := 1; i <= limit; i++ {
		allowed, rem, _ := limiter.Allow(id, limit)
		if !allowed {
			t.Fatalf("expected request %d to be allowed", i)
		}
		expectedRem := limit - i
		if rem != expectedRem {
			t.Errorf("expected remaining %d, got %d", expectedRem, rem)
		}
	}

	// 4回目: 超過で拒否
	allowed, rem, resetIn := limiter.Allow(id, limit)
	if allowed {
		t.Fatalf("expected request 4 to be rejected")
	}
	if rem != 0 {
		t.Errorf("expected remaining 0, got %d", rem)
	}
	if resetIn <= 0 {
		t.Errorf("expected resetIn > 0, got %v", resetIn)
	}

	// 1.1秒待機後: 再度許可
	time.Sleep(1100 * time.Millisecond)
	allowedAfter, remAfter, _ := limiter.Allow(id, limit)
	if !allowedAfter {
		t.Fatalf("expected request to be allowed after window reset")
	}
	if remAfter != limit-1 {
		t.Errorf("expected remaining %d, got %d", limit-1, remAfter)
	}
	limiter.Stop()
}

func TestSlidingWindowLimiter_Concurrent(t *testing.T) {
	limiter := NewSlidingWindowLimiter(time.Minute)
	defer limiter.Stop()

	// 並行リクエストによる競合テスト
	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func(id string) {
			for j := 0; j < 100; j++ {
				_, _, _ = limiter.Allow(id, 500)
			}
			done <- true
		}("key-" + string(rune('A'+i%10)))
	}

	for i := 0; i < 50; i++ {
		<-done
	}
}

func TestSlidingWindowLimiter_Unlimited(t *testing.T) {
	limiter := NewSlidingWindowLimiter(time.Minute)
	defer limiter.Stop()

	allowed, rem, resetIn := limiter.Allow("unlimited-id", 0)
	if !allowed || rem != 999999 || resetIn != 0 {
		t.Errorf("unexpected unlimited: allowed=%v, rem=%d, resetIn=%v", allowed, rem, resetIn)
	}
}
