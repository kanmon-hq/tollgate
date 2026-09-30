package usecase

import (
	"context"
	"sync"
	"time"

	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/metrics"
)

type updateLastUsedJob struct {
	keyHash string
	now     time.Time
}

type VerifyUsecase struct {
	repo        repository.KeyRepository
	limiter     repository.RateLimiter
	lastUsedMap sync.Map // keyHash -> time.Time (1分以内の重複更新をスロットリング)
	updateChan  chan updateLastUsedJob
}

func NewVerifyUsecase(repo repository.KeyRepository, limiter repository.RateLimiter) *VerifyUsecase {
	u := &VerifyUsecase{
		repo:       repo,
		limiter:    limiter,
		updateChan: make(chan updateLastUsedJob, 4096),
	}

	// ワーカプールを起動 (例えば10並列)
	for i := 0; i < 10; i++ {
		go u.lastUsedWorker()
	}

	return u
}

func (u *VerifyUsecase) lastUsedWorker() {
	for job := range u.updateChan {
		_ = u.repo.UpdateLastUsedAt(context.Background(), job.keyHash, job.now)
	}
}

// matchScope は要求スコープが許可スコープリストに合致するか判定する (ワイルドカード対応)
func matchScope(required string, allowedScopes []string) bool {
	if required == "" {
		return true
	}
	for _, allowed := range allowedScopes {
		if allowed == "*" || allowed == required {
			return true
		}
		l := len(allowed)
		if l >= 2 && allowed[l-2] == ':' && allowed[l-1] == '*' {
			prefixLen := l - 2
			if len(required) >= prefixLen && required[:prefixLen] == allowed[:prefixLen] {
				if len(required) == prefixLen || required[prefixLen] == ':' {
					return true
				}
			}
		}
	}
	return false
}

// VerifyKey はキーの正当性、有効期限、スコープ、RPM レート、月間クォータを判定し、カウントを更新する
func (u *VerifyUsecase) VerifyKey(ctx context.Context, input entity.VerifyKeyInput) (*entity.VerifyKeyOutput, error) {
	start := time.Now()
	defer func() {
		// レイテンシ記録
		metrics.VerificationDuration.WithLabelValues("global").Observe(time.Since(start).Seconds())
	}()

	keyHash := HashKey(input.RawKey)
	key, err := u.repo.GetKeyByHash(ctx, keyHash)
	if err != nil {
		metrics.VerificationsTotal.WithLabelValues("unknown", "rejected", "db_error").Inc()
		return nil, err
	}
	if key == nil {
		metrics.VerificationsTotal.WithLabelValues("unknown", "rejected", "invalid_key").Inc()
		return &entity.VerifyKeyOutput{
			Valid:  false,
			Reason: "invalid_key",
		}, nil
	}

	tenant := key.TenantID
	tenantLabel := tenant
	if tenantLabel == "" {
		if key.ServiceID != "" {
			tenantLabel = "service:" + key.ServiceID
		} else {
			tenantLabel = "unknown"
		}
	}

	// 1. 有効期限 (TTL) チェック
	nowUnix := time.Now().Unix()
	if key.ExpiresAt != nil && *key.ExpiresAt < nowUnix {
		metrics.VerificationsTotal.WithLabelValues(tenantLabel, "rejected", "expired").Inc()
		return &entity.VerifyKeyOutput{
			Valid:     false,
			Reason:    "expired",
			KeyID:     key.KeyID,
			KeyPrefix: key.KeyPrefix,
			TenantID:  tenant,
		}, nil
	}

	// 2. ローテーション猶予期間チェック
	if key.Status == entity.StatusRotating && key.Rotation != nil {
		if time.Now().After(key.Rotation.GracePeriodExpiresAt) {
			metrics.VerificationsTotal.WithLabelValues(tenantLabel, "rejected", "rotation_expired").Inc()
			return &entity.VerifyKeyOutput{
				Valid:     false,
				Reason:    "rotation_expired",
				KeyID:     key.KeyID,
				KeyPrefix: key.KeyPrefix,
				TenantID:  tenant,
			}, nil
		}
	}

	// 3. 有効フラグ (一時停止 / 失効) チェック
	if !key.IsActive || key.Status == entity.StatusSuspended || key.Status == entity.StatusRevoked {
		reason := "suspended"
		if key.Status == entity.StatusRevoked {
			reason = "revoked"
		}
		metrics.VerificationsTotal.WithLabelValues(tenantLabel, "rejected", reason).Inc()
		return &entity.VerifyKeyOutput{
			Valid:     false,
			Reason:    reason,
			KeyID:     key.KeyID,
			KeyPrefix: key.KeyPrefix,
			TenantID:  tenant,
		}, nil
	}

	// 4. スコープ認可判定
	if !matchScope(input.RequiredScope, key.Scopes) {
		metrics.VerificationsTotal.WithLabelValues(tenantLabel, "rejected", "scope_mismatch").Inc()
		return &entity.VerifyKeyOutput{
			Valid:     false,
			Reason:    "scope_mismatch",
			KeyID:     key.KeyID,
			KeyPrefix: key.KeyPrefix,
			TenantID:  tenant,
			Scopes:    key.Scopes,
		}, nil
	}

	// 5. RPM レート判定 (バックエンドは DI で切り替え可能)
	allowed, remainingRPM, _, limErr := u.limiter.Allow(ctx, key.KeyID, key.RateLimitRPM)
	if limErr != nil {
		// フェイルオープン: バックエンドエラー時はレートリミットをスキップ
		// (DynamoDBRateLimiter はエラー時に allowed=true を返すため通常ここには到達しないが念のため)
		metrics.VerificationsTotal.WithLabelValues(tenantLabel, "allowed", "rate_limit_backend_error").Inc()
	}
	if !allowed {
		metrics.VerificationsTotal.WithLabelValues(tenantLabel, "rejected", "rate_limit_exceeded").Inc()
		metrics.RateLimitExceededTotal.WithLabelValues(tenantLabel, key.KeyPrefix).Inc()
		return &entity.VerifyKeyOutput{
			Valid:        false,
			Reason:       "rate_limit_exceeded",
			KeyID:        key.KeyID,
			KeyPrefix:    key.KeyPrefix,
			TenantID:     tenant,
			RemainingRPM: 0,
			LimitRPM:     key.RateLimitRPM,
		}, nil
	}

	// 6. 月間クォータ判定 & Atomic カウントインクリメント
	currentMonth := time.Now().UTC().Format("2006-01")
	var remainingQuota int64 = -1 // -1 は無制限
	if key.MonthlyQuota > 0 {
		if key.CurrentMonth == currentMonth && key.CurrentMonthUsage >= key.MonthlyQuota {
			metrics.VerificationsTotal.WithLabelValues(tenantLabel, "rejected", "quota_exceeded").Inc()
			metrics.RateLimitExceededTotal.WithLabelValues(tenantLabel, key.KeyPrefix).Inc()
			return &entity.VerifyKeyOutput{
				Valid:          false,
				Reason:         "quota_exceeded",
				KeyID:          key.KeyID,
				KeyPrefix:      key.KeyPrefix,
				TenantID:       tenant,
				RemainingRPM:   remainingRPM,
				RemainingQuota: 0,
				LimitRPM:       key.RateLimitRPM,
				MonthlyQuota:   key.MonthlyQuota,
			}, nil
		}

		// カウント加算
		newUsage, err := u.repo.IncrementMonthlyUsage(ctx, keyHash, currentMonth, 1)
		if err == nil {
			remainingQuota = key.MonthlyQuota - newUsage
			if remainingQuota < 0 {
				remainingQuota = 0
			}
		}
	}

	// 7. 最終利用日時の非同期更新 (1分以内の連続リクエストはスロットリング)
	u.recordLastUsed(keyHash)

	metrics.VerificationsTotal.WithLabelValues(tenantLabel, "allowed", "success").Inc()

	return &entity.VerifyKeyOutput{
		Valid:          true,
		KeyID:          key.KeyID,
		KeyPrefix:      key.KeyPrefix,
		TenantID:       tenant,
		ServiceID:      key.ServiceID,
		Scopes:         key.Scopes,
		RemainingRPM:   remainingRPM,
		RemainingQuota: remainingQuota,
		LimitRPM:       key.RateLimitRPM,
		MonthlyQuota:   key.MonthlyQuota,
	}, nil
}

// recordLastUsed は同一キーに対する最終利用時刻の更新頻度を1分間に最大1回へスロットリングする
func (u *VerifyUsecase) recordLastUsed(keyHash string) {
	now := time.Now().UTC()
	if val, ok := u.lastUsedMap.Load(keyHash); ok {
		if lastTime, ok := val.(time.Time); ok && now.Sub(lastTime) < time.Minute {
			return // 直近1分以内に更新済み
		}
	}
	u.lastUsedMap.Store(keyHash, now)

	select {
	case u.updateChan <- updateLastUsedJob{keyHash: keyHash, now: now}:
		// 成功
	default:
		// バッファフル時はスキップ（Analyticsは重要度が低く、Goroutineのスパイクを防ぐため）
	}
}
