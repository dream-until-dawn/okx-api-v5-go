package okx

import (
	"context"
	"sync"
	"time"
)

// tokenBucket 是一个不依赖第三方库的令牌桶限流器。
//
// 采用「预约」而非「轮询」：即使当前没有可用令牌，也直接把令牌数减为负数并据此
// 算出等待时长。这样并发调用会按到达顺序自然排队，不会出现多个 goroutine 同时
// 醒来抢同一个令牌的惊群，也不会有人被反复饿死。
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64 // 每秒补充的令牌数
	burst  float64 // 桶容量，决定可以攒多少突发额度
	tokens float64
	last   time.Time
}

// NewLimiter 创建一个令牌桶限流器，用于 [WithRateLimit] 或 [WithLimiter]。
//
// ratePerSec 是稳态速率（每秒允许的请求数），burst 是突发容量：
// 空闲一段时间后最多可以连续发出 burst 个请求，之后回落到 ratePerSec。
// burst 传 0 或负数时按 1 处理。
//
// OKX 的限频是按接口分组的，不同接口额度不同（例如历史 K 线是 20 次 / 2 秒）。
// 一个 Client 共用一个限流器时，请按你调用最频繁的那个接口的额度来设，
// 或者给不同用途创建多个 Client。
func NewLimiter(ratePerSec float64, burst int) Limiter {
	if ratePerSec <= 0 {
		ratePerSec = 1
	}
	b := float64(burst)
	if b < 1 {
		b = 1
	}
	return &tokenBucket{rate: ratePerSec, burst: b, tokens: b}
}

// Wait 阻塞直到取得一个令牌，或 ctx 结束。
func (b *tokenBucket) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	now := time.Now()
	if b.last.IsZero() {
		b.last = now
	}
	// 按经过的时间补充令牌，上限为桶容量。
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now

	// 无条件预约一个令牌；不够就欠着，欠多少决定等多久。
	b.tokens--
	var wait time.Duration
	if b.tokens < 0 {
		wait = time.Duration(-b.tokens / b.rate * float64(time.Second))
	}
	b.mu.Unlock()

	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		// 请求被取消，把预约还回去，否则这份配额会被白白占用。
		b.mu.Lock()
		b.tokens++
		b.mu.Unlock()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
