package okx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fakeBaseTs = int64(1700000000000) // 最新一根 K 线的开始时间
	fakeStep   = int64(60000)         // 1m
	fakeTotal  = 1000                 // 交易所侧一共有这么多根
)

// fakeCandleServer 模拟 OKX 的历史 K 线接口：按时间倒序返回，
// after 表示「返回早于该时间戳的数据」，数据取完就返回空数组。
type fakeCandleServer struct {
	*httptest.Server
	calls   int64
	lastURI atomic.Value
	// unclosedNewest 为 true 时把最新一根标记为未收线。
	unclosedNewest bool
	// stuckCursor 为 true 时每页都返回同样的数据，用来验证防死循环。
	stuckCursor bool
}

func newFakeCandleServer(t *testing.T) *fakeCandleServer {
	t.Helper()
	f := &fakeCandleServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&f.calls, 1)
		f.lastURI.Store(r.URL.RequestURI())

		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)

		newest := fakeBaseTs
		if after > 0 && !f.stuckCursor {
			newest = after - fakeStep // 严格早于 after
		}

		oldestAvailable := fakeBaseTs - int64(fakeTotal-1)*fakeStep
		rows := make([]rawCandle, 0, limit)
		for ts := newest; ts >= oldestAvailable && len(rows) < limit; ts -= fakeStep {
			confirm := "1"
			if ts == fakeBaseTs && f.unclosedNewest {
				confirm = "0"
			}
			px := strconv.FormatInt(ts/fakeStep%1000, 10)
			rows = append(rows, rawCandle{
				strconv.FormatInt(ts, 10), px, px, px, px, "1", "2", "3", confirm,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": rows})
	}))
	t.Cleanup(f.Close)
	return f
}

func newHistoryClient(t *testing.T, f *fakeCandleServer) *Client {
	t.Helper()
	c, err := NewClient(WithRESTURL(f.URL), WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// assertSeries 校验序列是正序、无重复、按固定步长连续的。
func assertSeries(t *testing.T, ks []Candle, wantLen int) {
	t.Helper()
	if len(ks) != wantLen {
		t.Fatalf("拿到 %d 根，期望 %d 根", len(ks), wantLen)
	}
	seen := map[int64]bool{}
	for i, k := range ks {
		if seen[k.Ts] {
			t.Fatalf("第 %d 根时间戳重复: %d", i, k.Ts)
		}
		seen[k.Ts] = true
		if i > 0 {
			if k.Ts <= ks[i-1].Ts {
				t.Fatalf("第 %d 根不是正序: %d <= %d", i, k.Ts, ks[i-1].Ts)
			}
			if d := k.Ts - ks[i-1].Ts; d != fakeStep {
				t.Fatalf("第 %d 根出现 %dms 的跳变", i, d)
			}
		}
	}
}

func TestCandleHistoryPaginatesAndOrders(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)

	// 取 500 根，每页 100 —— 必须翻 5 页并拼成一条连续正序的序列。
	end := fakeBaseTs + fakeStep // 不含，保证最新一根也在区间内
	begin := fakeBaseTs - 499*fakeStep

	ks, err := c.Market.CandleHistory(context.Background(), HistoryRequest{
		InstID: "ETH-USDT-SWAP", Bar: "1m",
		Begin: begin, End: end,
		PageLimit: 100, PageDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSeries(t, ks, 500)
	if ks[0].Ts != begin {
		t.Errorf("首根 = %d，期望 %d", ks[0].Ts, begin)
	}
	if ks[len(ks)-1].Ts != fakeBaseTs {
		t.Errorf("末根 = %d，期望 %d", ks[len(ks)-1].Ts, fakeBaseTs)
	}
	if n := atomic.LoadInt64(&f.calls); n < 5 {
		t.Errorf("只发了 %d 次请求，500 根 / 每页 100 应至少 5 次", n)
	}
}

func TestCandleHistoryRespectsBoundaries(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)

	// End 是开区间：等于 End 的那一根必须被排除。
	end := fakeBaseTs - 10*fakeStep
	begin := end - 20*fakeStep

	ks, err := c.Market.CandleHistory(context.Background(), HistoryRequest{
		InstID: "X", Begin: begin, End: end, PageLimit: 50, PageDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSeries(t, ks, 20) // [begin, end) 共 20 根
	for _, k := range ks {
		if k.Ts < begin || k.Ts >= end {
			t.Fatalf("时间戳 %d 越出了 [%d,%d)", k.Ts, begin, end)
		}
	}
}

func TestCandleHistoryStopsWhenExchangeRunsOut(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)

	// Begin 为 0 表示一直往前翻；服务端只有 fakeTotal 根，翻完必须自己停下。
	ks, err := c.Market.CandleHistory(context.Background(), HistoryRequest{
		InstID: "X", PageLimit: 300, PageDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSeries(t, ks, fakeTotal)
}

func TestCandleHistoryExcludesUnclosedByDefault(t *testing.T) {
	f := newFakeCandleServer(t)
	f.unclosedNewest = true
	c := newHistoryClient(t, f)

	req := HistoryRequest{
		InstID: "X", Begin: fakeBaseTs - 9*fakeStep, End: fakeBaseTs + fakeStep,
		PageLimit: 50, PageDelay: time.Millisecond,
	}

	// 默认剔除未收线的那一根：回测里留着它会让最后一根数据是残缺的。
	ks, err := c.Market.CandleHistory(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(ks) != 9 {
		t.Fatalf("默认应剔除未收线的 K 线，拿到 %d 根，期望 9 根", len(ks))
	}
	for _, k := range ks {
		if !k.Confirm {
			t.Fatal("默认不应返回未收线的 K 线")
		}
	}

	// 显式要求时才保留。
	req.IncludeUnclosed = true
	ks, err = c.Market.CandleHistory(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(ks) != 10 {
		t.Fatalf("IncludeUnclosed 时应有 10 根，实际 %d 根", len(ks))
	}
}

func TestCandleHistoryGuardsAgainstStuckCursor(t *testing.T) {
	f := newFakeCandleServer(t)
	f.stuckCursor = true // 服务端每页都返回同样的数据
	c := newHistoryClient(t, f)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Market.CandleHistory(context.Background(), HistoryRequest{
			InstID: "X", PageLimit: 50, PageDelay: time.Millisecond,
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("游标不前进时没有停下，说明缺少死循环保护")
	}
	if n := atomic.LoadInt64(&f.calls); n > 3 {
		t.Errorf("发了 %d 次请求，游标不前进时应立刻停止", n)
	}
}

func TestCandleHistoryMaxPages(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)

	ks, err := c.Market.CandleHistory(context.Background(), HistoryRequest{
		InstID: "X", PageLimit: 50, MaxPages: 3, PageDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSeries(t, ks, 150)
	if n := atomic.LoadInt64(&f.calls); n != 3 {
		t.Errorf("请求了 %d 次，MaxPages=3 应恰好 3 次", n)
	}
}

func TestCandleHistoryHonoursContextCancel(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	var pages int
	err := c.Market.EachCandlePage(ctx, HistoryRequest{
		InstID: "X", PageLimit: 10, PageDelay: 10 * time.Millisecond,
	}, func([]Candle) bool {
		pages++
		if pages == 2 {
			cancel()
		}
		return true
	})
	if err == nil {
		t.Fatal("ctx 取消后应返回错误")
	}
	if pages > 3 {
		t.Errorf("取消后仍翻了 %d 页", pages)
	}
}

func TestEachCandlePageStopsOnFalse(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)

	var pages int
	err := c.Market.EachCandlePage(context.Background(), HistoryRequest{
		InstID: "X", PageLimit: 10, PageDelay: time.Millisecond,
	}, func([]Candle) bool {
		pages++
		return pages < 2 // 第二页返回 false
	})
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 {
		t.Fatalf("回调返回 false 后应停止，实际翻了 %d 页", pages)
	}
}

func TestHistoryRequestValidation(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)
	ctx := context.Background()

	if _, err := c.Market.CandleHistory(ctx, HistoryRequest{}); err == nil {
		t.Error("InstID 为空时应报错")
	}
	if _, err := c.Market.CandleHistory(ctx, HistoryRequest{InstID: "X", Begin: 200, End: 100}); err == nil {
		t.Error("Begin 晚于 End 时应报错")
	}

	// PageLimit 超过交易所上限时应被夹到 300，而不是原样发出去。
	_, err := c.Market.CandleHistory(ctx, HistoryRequest{
		InstID: "X", PageLimit: 5000, MaxPages: 1, PageDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if uri, _ := f.lastURI.Load().(string); uri == "" || !strings.Contains(uri, "limit=300") {
		t.Errorf("PageLimit 未被夹到 300: %s", uri)
	}
}

func TestCandleSourceRoutesToCorrectEndpoint(t *testing.T) {
	f := newFakeCandleServer(t)
	c := newHistoryClient(t, f)

	cases := map[CandleSource]string{
		CandleSourceTrade: "/api/v5/market/history-candles",
		CandleSourceMark:  "/api/v5/market/history-mark-price-candles",
		CandleSourceIndex: "/api/v5/market/history-index-candles",
		"":                "/api/v5/market/history-candles", // 留空默认走成交价
	}
	for src, wantPath := range cases {
		_, err := c.Market.CandleHistory(context.Background(), HistoryRequest{
			InstID: "X", Source: src, MaxPages: 1, PageLimit: 10, PageDelay: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		uri, _ := f.lastURI.Load().(string)
		if !strings.Contains(uri, wantPath) {
			t.Errorf("Source=%q 打到了 %s，期望 %s", src, uri, wantPath)
		}
	}
}

// 标记价 / 指数 K 线是 6 段格式，套 9 段的解析会把 confirm 读成成交量。
func TestRawCandleHandlesBothLengths(t *testing.T) {
	full := rawCandle{"1700000000000", "100", "110", "99", "105", "12", "13", "14", "1"}
	c := full.toCandle()
	if c.Vol != 12 || c.VolCcy != 13 || c.VolCcyQuote != 14 || !c.Confirm {
		t.Fatalf("9 段格式解析错误: %+v", c)
	}

	// 标记价 / 指数：ts,o,h,l,c,confirm —— 没有成交量，confirm 在第 6 段。
	short := rawCandle{"1700000000000", "2463.83", "2481.98", "2462.26", "2477.51", "1"}
	c = short.toCandle()
	if c.Open != 2463.83 || c.High != 2481.98 || c.Low != 2462.26 || c.Close != 2477.51 {
		t.Fatalf("6 段格式的 OHLC 解析错误: %+v", c)
	}
	if c.Vol != 0 || c.VolCcy != 0 || c.VolCcyQuote != 0 {
		t.Fatalf("6 段格式不应有成交量，实际 vol=%v volCcy=%v", c.Vol, c.VolCcy)
	}
	if !c.Confirm {
		t.Fatal("6 段格式的 confirm 在最后一段，应解析为 true")
	}

	// 未收线的 6 段样本
	if short2 := (rawCandle{"1700000000000", "1", "1", "1", "1", "0"}).toCandle(); short2.Confirm {
		t.Fatal("confirm=0 时不应为 true")
	}
	// 长度不足时不应 panic
	_ = rawCandle{"1700000000000"}.toCandle()
	_ = rawCandle{}.toCandle()
}

func TestTradeFeeRatesPicksSettlementSpecificFields(t *testing.T) {
	// 实测：按 instFamily 查 USDT 永续时，maker/taker 为空，费率在 makerU/takerU。
	f := TradeFee{Maker: "", Taker: "", MakerU: "-0.0002", TakerU: "-0.0005"}
	m, tk := f.Rates("USDT")
	if m != "-0.0002" || tk != "-0.0005" {
		t.Fatalf("USDT 本位应取 makerU/takerU，实际 maker=%q taker=%q", m, tk)
	}

	f2 := TradeFee{MakerUSDC: "-0.0001", TakerUSDC: "-0.0004", Maker: "-0.0008", Taker: "-0.001"}
	if m, tk = f2.Rates("USDC"); m != "-0.0001" || tk != "-0.0004" {
		t.Fatalf("USDC 本位取值错误: %q %q", m, tk)
	}

	// 现货 / 币本位走通用字段。
	f3 := TradeFee{Maker: "-0.0008", Taker: "-0.001"}
	if m, tk = f3.Rates("BTC"); m != "-0.0008" || tk != "-0.001" {
		t.Fatalf("币本位取值错误: %q %q", m, tk)
	}

	// 专用字段为空时回退到通用字段，而不是返回空值。
	f4 := TradeFee{Maker: "-0.0008", Taker: "-0.001"}
	if m, tk = f4.Rates("USDT"); m != "-0.0008" || tk != "-0.001" {
		t.Fatalf("makerU 为空时应回退到 maker，实际 %q %q", m, tk)
	}
}

func TestTierForBoundaries(t *testing.T) {
	// 取自实测数据：档位区间是左开右闭。
	tiers := []PositionTier{
		{Tier: "1", MinSz: "0", MaxSz: "5000", MMR: "0.004", MaxLever: "100"},
		{Tier: "2", MinSz: "5000.01", MaxSz: "10000", MMR: "0.005", MaxLever: "66.66"},
		{Tier: "3", MinSz: "10000.01", MaxSz: "20000", MMR: "0.0075", MaxLever: "50"},
	}
	for _, tc := range []struct {
		sz       float64
		wantTier string
		wantOK   bool
	}{
		{10, "1", true},
		{5000, "1", true},   // 上界含
		{5000.5, "2", true}, // 落在 5000 与 5000.01 之间，归入下一档
		{10000, "2", true},
		{20000, "3", true},
		{20001, "", false}, // 超出最高档
		{0, "", false},     // 下界不含
	} {
		got, ok := TierFor(tiers, tc.sz)
		if ok != tc.wantOK || (ok && got.Tier.String() != tc.wantTier) {
			t.Errorf("TierFor(%v) = (%q,%v)，期望 (%q,%v)", tc.sz, got.Tier, ok, tc.wantTier, tc.wantOK)
		}
	}

	// MMR 随档位跳变，这正是回测不能用固定 MMR 的原因。
	t1, _ := TierFor(tiers, 100)
	t3, _ := TierFor(tiers, 15000)
	if t3.MMR.Float64() <= t1.MMR.Float64() {
		t.Fatal("高档位的维持保证金率应更高")
	}
}

func TestFundingRateHistoryAllPaginates(t *testing.T) {
	const step = int64(8 * 3600 * 1000) // 每 8 小时一次
	const total = 250
	base := fakeBaseTs

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		newest := base
		if after > 0 {
			newest = after - step
		}
		oldest := base - int64(total-1)*step
		rows := []map[string]string{}
		for ts := newest; ts >= oldest && len(rows) < 100; ts -= step {
			rows = append(rows, map[string]string{
				"instId": "X", "fundingRate": "0.0001",
				"fundingTime": strconv.FormatInt(ts, 10),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": rows})
	}))
	defer srv.Close()

	c, err := NewClient(WithRESTURL(srv.URL), WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.Public.FundingRateHistoryAll(context.Background(), "X", 0, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != total {
		t.Fatalf("拿到 %d 条，期望 %d 条", len(items), total)
	}
	for i := 1; i < len(items); i++ {
		if items[i].FundingTime.Int64() <= items[i-1].FundingTime.Int64() {
			t.Fatalf("第 %d 条不是正序", i)
		}
	}
	if n := atomic.LoadInt64(&calls); n < 3 {
		t.Errorf("只发了 %d 次请求，250 条 / 每页 100 应至少 3 次", n)
	}
}
