package okx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTradeWS 起一条已登录的私有连接，并把 instIdCode 预置进缓存，
// 避免下单时触发真实的 REST 查询。
func newTradeWS(t *testing.T, f *fakeOKX) (*Client, *WS) {
	t.Helper()
	c := newWSTestClient(t, f)
	c.instCodes.put("ETH-USDT-SWAP", 12345)

	ws := c.NewPrivateWS()
	t.Cleanup(func() { ws.Close() })

	ready := make(chan struct{}, 1)
	ws.OnConnect(func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := ws.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ready, "ws login")
	return c, ws
}

func sampleOrder() OrderRequest {
	return OrderRequest{
		InstID: "ETH-USDT-SWAP", TdMode: TdModeIsolated, Side: SideSell,
		PosSide: PosSideLong, OrdType: OrdTypeLimit, Sz: "0.01", Px: "9999",
	}
}

func TestWSPlaceOrderRoundTrip(t *testing.T) {
	f := newFakeOKX(t)
	_, ws := newTradeWS(t, f)

	res, err := ws.PlaceOrder(context.Background(), sampleOrder())
	if err != nil {
		t.Fatal(err)
	}
	if res.OrdID == "" || res.SCode != "0" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !res.OK() {
		t.Fatal("OK() should be true for sCode 0")
	}

	// 请求体里必须带上 instIdCode，否则交易所会返回 50014。
	raw, _ := f.lastOrder.Load().(string)
	var sent map[string]any
	if err := json.Unmarshal([]byte(raw), &sent); err != nil {
		t.Fatalf("服务端没有收到 order 请求: %q", raw)
	}
	if code, ok := sent["instIdCode"]; !ok || code.(float64) != 12345 {
		t.Fatalf("instIdCode 未自动补齐: %v", sent["instIdCode"])
	}
	if sent["posSide"] != PosSideLong || sent["tdMode"] != TdModeIsolated {
		t.Fatalf("订单参数未正确透传: %v", sent)
	}
}

func TestWSTradeOnlyOnPrivateConnection(t *testing.T) {
	f := newFakeOKX(t)
	c := newWSTestClient(t, f)

	for _, ws := range []*WS{c.NewPublicWS(), c.NewBusinessWS()} {
		defer ws.Close()
		if _, err := ws.PlaceOrder(context.Background(), sampleOrder()); !errors.Is(err, ErrWSNotTrading) {
			t.Errorf("%s 连接上下单应返回 ErrWSNotTrading，实际 %v", ws.endpoint, err)
		}
		if _, err := ws.CancelOrder(context.Background(), CancelOrderRequest{InstID: "X"}); !errors.Is(err, ErrWSNotTrading) {
			t.Errorf("%s 连接上撤单应返回 ErrWSNotTrading，实际 %v", ws.endpoint, err)
		}
	}
}

func TestWSTradeBeforeLoginFails(t *testing.T) {
	f := newFakeOKX(t)
	c := newWSTestClient(t, f)
	ws := c.NewPrivateWS()
	defer ws.Close()

	// 还没 Connect，不应该发出请求，而应立刻报错。
	_, err := ws.PlaceOrder(context.Background(), sampleOrder())
	if err == nil {
		t.Fatal("未连接时下单应报错")
	}
	if atomic.LoadInt64(&f.tradeOps) != 0 {
		t.Fatal("未连接时不应向服务端发出请求")
	}
}

// 并发请求必须各自拿到自己的应答，不能串号——这是 id 关联的核心保证。
func TestWSConcurrentCallsDoNotCrossTalk(t *testing.T) {
	f := newFakeOKX(t)
	// 故意让应答乱序返回：延迟越长的请求越晚回，能覆盖「后发先至」的情况。
	f.tradeDelay = 80 * time.Millisecond
	_, ws := newTradeWS(t, f)

	const n = 12
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := ws.PlaceOrder(context.Background(), sampleOrder())
			ids[i], errs[i] = res.OrdID, err
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, id := range ids {
		if errs[i] != nil {
			t.Fatalf("第 %d 个请求失败: %v", i, errs[i])
		}
		if id == "" {
			t.Fatalf("第 %d 个请求没拿到 ordId", i)
		}
		if seen[id] {
			t.Fatalf("应答串号：ordId %q 被两个请求同时拿到", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("只拿到 %d 个不同的应答，期望 %d 个", len(seen), n)
	}
}

func TestWSTradeSurfacesBusinessError(t *testing.T) {
	f := newFakeOKX(t)
	f.tradeCode = "1"
	f.tradeSCode = "51008" // 余额不足
	_, ws := newTradeWS(t, f)

	_, err := ws.PlaceOrder(context.Background(), sampleOrder())
	if err == nil {
		t.Fatal("交易所拒绝时应返回错误")
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("错误类型不是 *APIError: %T", err)
	}
	if apiErr.Code != "1" || apiErr.SCode != "51008" {
		t.Fatalf("错误详情不对: %+v", apiErr)
	}
	if !IsCode(err, "51008") {
		t.Fatal("IsCode 应能匹配 WS 交易返回的 sCode")
	}
}

func TestWSTradeUnblocksOnDisconnect(t *testing.T) {
	f := newFakeOKX(t)
	f.tradeDelay = 10 * time.Second // 服务端故意不回
	_, ws := newTradeWS(t, f)

	done := make(chan error, 1)
	go func() {
		_, err := ws.PlaceOrder(context.Background(), sampleOrder())
		done <- err
	}()

	// 等请求确实发出去了再掐线。
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt64(&f.tradeOps) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	f.dropConn()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("连接断开时等待中的请求应报错")
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Logf("错误信息: %v", err) // 只要报错即可，措辞不强求
		}
	case <-time.After(5 * time.Second):
		t.Fatal("连接断开后请求仍然挂着，说明没有唤醒等待中的调用")
	}
}

func TestWSTradeHonoursContext(t *testing.T) {
	f := newFakeOKX(t)
	f.tradeDelay = 5 * time.Second
	_, ws := newTradeWS(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := ws.PlaceOrder(ctx, sampleOrder())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v，期望 DeadlineExceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("等了 %s 才返回，ctx 超时没有及时生效", d)
	}
}

func TestWSBatchOrders(t *testing.T) {
	f := newFakeOKX(t)
	_, ws := newTradeWS(t, f)

	reqs := []OrderRequest{sampleOrder(), sampleOrder(), sampleOrder()}
	results, err := ws.PlaceOrders(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("拿到 %d 条结果，期望 3 条", len(results))
	}
	for i, r := range results {
		if !r.OK() || r.OrdID == "" {
			t.Fatalf("第 %d 条结果异常: %+v", i, r)
		}
	}

	cancels := []CancelOrderRequest{
		{InstID: "ETH-USDT-SWAP", OrdID: results[0].OrdID},
		{InstID: "ETH-USDT-SWAP", OrdID: results[1].OrdID},
	}
	cs, err := ws.CancelOrders(context.Background(), cancels)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("撤单结果 %d 条，期望 2 条", len(cs))
	}
}

func TestInferInstType(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"ETH-USDT-SWAP", "SWAP"},
		{"BTC-USD-SWAP", "SWAP"},
		{"ETH-USDT", "SPOT"},
		{"BTC-USD-240329", "FUTURES"},
		{"BTC-USD-240329-50000-C", "OPTION"},
		{"BTC-USD-240329-50000-P", "OPTION"},
		{"garbage", ""},
	} {
		if got := InferInstType(tc.id); got != tc.want {
			t.Errorf("InferInstType(%q) = %q，期望 %q", tc.id, got, tc.want)
		}
	}
}

func TestInstrumentCodeCaching(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		_, _ = w.Write([]byte(`{"code":"0","msg":"","data":[
			{"instId":"ETH-USDT-SWAP","instIdCode":2021032601102994,"instType":"SWAP"},
			{"instId":"BTC-USDT-SWAP","instIdCode":2021032601102995,"instType":"SWAP"}]}`))
	}))
	defer srv.Close()

	c, err := NewClient(WithRESTURL(srv.URL), WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	code, err := c.InstrumentCode(ctx, "ETH-USDT-SWAP")
	if err != nil || code != 2021032601102994 {
		t.Fatalf("code = %d, err = %v", code, err)
	}
	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Fatalf("首次查询发了 %d 次请求，期望 1 次", n)
	}

	// 第二次必须命中缓存，不再走网络。
	if _, err := c.InstrumentCode(ctx, "ETH-USDT-SWAP"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Fatalf("缓存未命中，请求数变成了 %d", n)
	}

	// 预热后其他产品也不该再触发查询。
	if err := c.PreloadInstrumentCodes(ctx, "SWAP"); err != nil {
		t.Fatal(err)
	}
	before := atomic.LoadInt64(&calls)
	if code, err := c.InstrumentCode(ctx, "BTC-USDT-SWAP"); err != nil || code != 2021032601102995 {
		t.Fatalf("预热后取值失败: code=%d err=%v", code, err)
	}
	if n := atomic.LoadInt64(&calls); n != before {
		t.Fatalf("预热后仍然发起了查询: %d -> %d", before, n)
	}

	// 无法推断类型的 instId 应给出明确错误，而不是发一个必然失败的请求。
	if _, err := c.InstrumentCode(ctx, "garbage"); err == nil {
		t.Fatal("无法推断产品类型时应报错")
	}
}

func TestInstrumentCodeNotOverwrittenWhenProvided(t *testing.T) {
	f := newFakeOKX(t)
	_, ws := newTradeWS(t, f)

	req := sampleOrder()
	req.InstIDCode = 999 // 调用方显式指定
	if _, err := ws.PlaceOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	raw, _ := f.lastOrder.Load().(string)
	var sent map[string]any
	_ = json.Unmarshal([]byte(raw), &sent)
	if sent["instIdCode"].(float64) != 999 {
		t.Fatalf("显式指定的 instIdCode 被覆盖了: %v", sent["instIdCode"])
	}
}
