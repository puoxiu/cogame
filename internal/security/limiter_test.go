package security

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)



func TestCreateAndBurst(t *testing.T) {
	rlm := NewRateLimitManager()
	defer rlm.StopCleanup()

	key := "user_burst"

	r := rate.Limit(0) // never refill
	burst := 2

	// 第1次
	if !rlm.CheckLimit(key, r, burst) {
		t.Fatalf("expected first request allowed")
	}
	// 第2次
	if !rlm.CheckLimit(key, r, burst) {
		t.Fatalf("expected second request allowed")
	}
	// 第3次（必定失败）
	if rlm.CheckLimit(key, r, burst) {
		t.Fatalf("expected third request denied")
	}

	if _, ok := rlm.limiters[key]; !ok {
		t.Fatalf("expected bucket to be created")
	}
}

func TestDynamic(t *testing.T) {
	rlm := NewRateLimitManager()
	defer rlm.StopCleanup()

	key := "user_dynamic"
	if !rlm.CheckLimit(key, rate.Limit(0), 2) {
		t.Fatalf("expected first request allowed")
	}
	if !rlm.CheckLimit(key, rate.Limit(0), 2) {
		t.Fatalf("expected second request denied")
	}

	if _, ok := rlm.limiters[key]; !ok {
		t.Fatalf("expected bucket to be created")
	}

	if rlm.CheckLimit(key, rate.Limit(0), 2) {
		t.Fatalf("expected third request allowed")
	}
}

func TestCleanupExpiredBuckets(t *testing.T) {
	rlm := NewRateLimitManager()
	defer rlm.StopCleanup()

	key := "user_expired"
	if !rlm.CheckLimit(key, rate.Limit(0), 2) {
		t.Fatalf("expected first request allowed")
	}
	if !rlm.CheckLimit(key, rate.Limit(0), 2) {
		t.Fatalf("expected second request denied")
	}
	if _, ok := rlm.limiters[key]; !ok {
		t.Fatalf("expected bucket to be created")
	}

	if rlm.CheckLimit(key, rate.Limit(0), 2) {
		t.Fatalf("expected third request allowed")
	}
}


// 测试并发
func TestConcurrentAccess(t *testing.T) {
	rlm := NewRateLimitManager()
	defer rlm.StopCleanup()

	key := "user_concurrent"
	var wg sync.WaitGroup
	var allowedCount int32
	goroutines := 20
	callsPerG := 50

	rand.Seed(time.Now().UnixNano())

	r := rate.Limit(2000) // high refill so many allowed
	burst := 700

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerG; j++ {
				if rlm.CheckLimit(key, r, burst) {
					atomic.AddInt32(&allowedCount, 1)
				}
				delay := rand.Intn(5)
				time.Sleep(time.Millisecond * time.Duration(delay))
			}
		}()
	}
	wg.Wait()

	expectedMin := int32(goroutines * callsPerG * 90 / 100) // 允许90%以上
	if allowedCount < expectedMin {
		t.Fatalf("expected >= %d allowed, got %d (total requests: %d)", 
			expectedMin, allowedCount, goroutines*callsPerG)
	}
}


func BenchmarkCheckLimit(b *testing.B) {
	rlm := NewRateLimitManager()
	defer rlm.StopCleanup()
	key := "bench_key"
	// use a high refill to avoid blocking
	r := rate.Limit(10000)
	burst := 1000

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rlm.CheckLimit(key, r, burst)
	}
}