package security

import (
	"fmt"
	"sync"
	"time"

	"github.com/puoxiu/cogame/internal/logger"
	"golang.org/x/time/rate"
)

// RateLimitManager 限流管理器：按key管理令牌桶限流器
type RateLimitManager struct {
	limiters      map[string]*RateLimiter // key -> 令牌桶限流器
	mutex         sync.RWMutex            // 并发安全锁（读写锁）
	cleanupTicker *time.Ticker            // 定时清理过期限流器
	stopChan      chan struct{}           // 停止清理协程的信号
}

// RateLimiter 令牌桶限流器：封装rate.Limiter及元数据
type RateLimiter struct {
	limiter     *rate.Limiter
	rate        rate.Limit       // rps：每秒生成多少令牌
	burst       int              // 令牌桶容量--最大突发请求数
	lastRequest time.Time        // 上次请求时间（用于清理过期key）
}

// NewRateLimitManager 创建令牌桶限流管理器
func NewRateLimitManager() *RateLimitManager {
	rlm := &RateLimitManager{
		limiters:      make(map[string]*RateLimiter),
		cleanupTicker: time.NewTicker(5 * time.Minute), // 每5分钟清理一次
		stopChan:      make(chan struct{}),
	}

	// 启动定时清理协程
	go rlm.startCleanupLoop()

	logger.Info("Token bucket rate limit manager initialized")
	return rlm
}

// CheckLimit 检查请求是否允许（纯粹令牌桶逻辑）
// 参数：
//   - key：限流对象标识（如"ip:192.168.1.1"）
//   - rate：令牌生成速率（个/秒，如2表示每秒2个令牌）
//   - burst：桶容量（最大突发请求数，如5表示最多一次性处理5个）
// 返回：true（允许请求）/ false（拒绝请求）
func (rlm *RateLimitManager) CheckLimit(key string, r rate.Limit, burst int) bool {
	rlm.mutex.Lock()
	defer rlm.mutex.Unlock()

	// 1. 查找或创建令牌桶
	bucket, exists := rlm.limiters[key]
	if !exists {
		// 新建令牌桶：速率rate，容量burst
		bucket = &RateLimiter{
			limiter:     rate.NewLimiter(r, burst),
			rate:        r,
			burst:       burst,
			lastRequest: time.Now(),
		}
		rlm.limiters[key] = bucket
		logger.Debug(fmt.Sprintf("Created token bucket for key: %s (rate: %.2f rps, burst: %d)",
			key, r, burst))

		allowed := bucket.limiter.Allow()
		if allowed {
			bucket.lastRequest = time.Now() // 只有允许时才更新最后请求时间
		}
		return allowed
	}

	// 2. 若速率或容量变化，动态更新令牌桶
	if bucket.rate != r || bucket.burst != burst {
		bucket.rate = r
		bucket.burst = burst
		bucket.limiter.SetLimit(r) 
		bucket.limiter.SetBurst(burst) 
		logger.Debug(fmt.Sprintf("Updated token bucket for key: %s (new rate: %.2f rps, burst: %d)",
			key, r, burst))
	}

	// 3. 尝试获取令牌（允许请求则消耗1个令牌）
	allowed := bucket.limiter.Allow()
	if allowed {
		bucket.lastRequest = time.Now()
		logger.Debug(fmt.Sprintf("Request allowed for key: %s (remaining tokens: %.1f)",
			key, bucket.limiter.Tokens()))
	} else {
		logger.Warn(fmt.Sprintf("Rate limit exceeded for key: %s (remaining tokens: %.1f)",
			key, bucket.limiter.Tokens()))
	}

	return allowed
}


// startCleanupLoop 启动定时清理协程
func (rlm *RateLimitManager) startCleanupLoop() {
	for {
		select {
		case <-rlm.cleanupTicker.C:
			rlm.cleanupExpiredBuckets()
		case <-rlm.stopChan:
			rlm.cleanupTicker.Stop()
			logger.Info("Token bucket cleanup loop stopped")
			return
		}
	}
}

// cleanupExpiredBuckets 清理1小时内无请求的令牌桶
func (rlm *RateLimitManager) cleanupExpiredBuckets() {
	rlm.mutex.Lock()
	defer rlm.mutex.Unlock()

	now := time.Now()
	expiredCount := 0
	expiry := 1 * time.Hour // 1小时无请求则视为过期

	for key, bucket := range rlm.limiters {
		if now.Sub(bucket.lastRequest) > expiry {
			delete(rlm.limiters, key)
			expiredCount++
			logger.Debug(fmt.Sprintf("Cleaned up expired token bucket for key: %s", key))
		}
	}

	if expiredCount > 0 {
		logger.Info(fmt.Sprintf("Cleaned up %d expired token buckets (remaining: %d)",
			expiredCount, len(rlm.limiters)))
	}
}

// StopCleanup 停止清理协程
func (rlm *RateLimitManager) StopCleanup() {
	close(rlm.stopChan)
}
