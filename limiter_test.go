package okx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenThrottles(t *testing.T) {
	// 10 次/秒、桶容量 5：前 5 次应几乎不等待，第 6 次起每次约 100ms。
	lim := NewLimiter(10, 5)
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := lim.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 30*time.Millisecond {
		t.Fatalf("突发额度内不应等待，实际耗时 %s", d)
	}

	start = time.Now()
	for i := 0; i < 3; i++ {
		if err := lim.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// 3 次 @10/s ≈ 300ms，留出调度余量。
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Fatalf("超出突发额度后耗时 %s，短于理论下限 300ms", d)
	}
}

func TestLimiterSteadyRate(t *testing.T) {
	// 桶容量 1 时没有突发额度，速率应严格贴近设定值。
	lim := NewLimiter(20, 1)
	ctx := context.Background()

	start := time.Now()
	const n = 10
	for i := 0; i < n; i++ {
		if err := lim.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	// 首个令牌是现成的，其余 9 个各等 50ms ≈ 450ms。
	if elapsed < 400*time.Millisecond {
		t.Fatalf("%d 次 @20/s 只用了 %s，限速没有生效", n, elapsed)
	}
	if elapsed > 900*time.Millisecond {
		t.Fatalf("%d 次 @20/s 用了 %s，限速过于保守", n, elapsed)
	}
}

// 并发调用必须排队而不是互相抢令牌：总耗时应接近串行的理论值，且没有请求被饿死。
func TestLimiterFairUnderConcurrency(t *testing.T) {
	lim := NewLimiter(50, 1)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	var done int64
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := lim.Wait(ctx); err == nil {
				atomic.AddInt64(&done, 1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if got := atomic.LoadInt64(&done); got != n {
		t.Fatalf("只有 %d/%d 个请求拿到令牌", got, n)
	}
	// 19 个请求各等 20ms ≈ 380ms；若发生惊群互抢会明显短于此。
	if elapsed < 300*time.Millisecond {
		t.Fatalf("%d 个并发请求只用了 %s，说明令牌被重复发放", n, elapsed)
	}
}

func TestLimiterRespectsContext(t *testing.T) {
	lim := NewLimiter(1, 1)
	ctx := context.Background()
	if err := lim.Wait(ctx); err != nil { // 用掉唯一的令牌
		t.Fatal(err)
	}

	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := lim.Wait(cctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v，期望 DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("等了 %s 才响应取消", d)
	}

	// 取消的请求应把预约还回去，否则后续调用会被白白拖慢。
	start = time.Now()
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("取消后的配额没有归还，下一次等了 %s", d)
	}
}

func TestLimiterHandlesDegenerateParams(t *testing.T) {
	// 非法参数不应 panic 或永久阻塞。
	for _, lim := range []Limiter{NewLimiter(0, 0), NewLimiter(-5, -1)} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := lim.Wait(ctx); err != nil {
			t.Errorf("退化参数下首次 Wait 失败: %v", err)
		}
		cancel()
	}
}

func TestWithRateLimitThrottlesRequests(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		_, _ = w.Write([]byte(`{"code":"0","msg":"","data":[{"ts":"1"}]}`))
	}))
	defer srv.Close()

	c, err := NewClient(WithRESTURL(srv.URL), WithRateLimit(20, 1))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 8; i++ {
		if _, err := c.Public.ServerTime(ctx); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)

	if atomic.LoadInt64(&calls) != 8 {
		t.Fatalf("服务端收到 %d 次请求，期望 8 次", calls)
	}
	// 本地 httptest 的往返可以忽略，7 个等待 @20/s ≈ 350ms。
	if elapsed < 300*time.Millisecond {
		t.Fatalf("8 次请求只用了 %s，限流没有接到请求链路上", elapsed)
	}
}

func TestNoLimiterByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"0","msg":"","data":[{"ts":"1"}]}`))
	}))
	defer srv.Close()

	c, err := NewClient(WithRESTURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		if _, err := c.Public.ServerTime(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// 默认不限流，20 次本地请求应当很快跑完。
	if d := time.Since(start); d > time.Second {
		t.Fatalf("默认不该限流，20 次本地请求却用了 %s", d)
	}
}
