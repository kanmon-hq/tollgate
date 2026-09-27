package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestRedisRateLimiter_UnlimitedAndToInt64(t *testing.T) {
	limiter := NewRedisRateLimiter(nil, time.Minute)
	if limiter == nil {
		t.Fatalf("expected non-nil limiter")
	}

	// limitRPM <= 0
	allowed, remaining, resetIn, err := limiter.Allow(context.Background(), "id-1", 0)
	if err != nil || !allowed || remaining != 999999 || resetIn != 0 {
		t.Errorf("unexpected unlimited response: allowed=%v, remaining=%d, resetIn=%v, err=%v", allowed, remaining, resetIn, err)
	}

	// toInt64 helper
	if toInt64(int64(42)) != 42 {
		t.Errorf("expected 42")
	}
	if toInt64(int(42)) != 42 {
		t.Errorf("expected 42")
	}
	if toInt64("string") != 0 {
		t.Errorf("expected 0 for unsupported type")
	}
}
