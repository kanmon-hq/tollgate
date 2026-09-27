package metrics

import (
	"testing"
)

func TestMetrics_RegistrationAndIncrement(t *testing.T) {
	// 各メトリクスオブジェクトが正しく初期化され、ラベル指定・記録できることを確認
	VerificationsTotal.WithLabelValues("tenant-test", "allowed", "success").Inc()
	RateLimitExceededTotal.WithLabelValues("tenant-test", "tlge-live-test").Inc()
	VerificationDuration.WithLabelValues("tenant-test").Observe(0.001)
}
