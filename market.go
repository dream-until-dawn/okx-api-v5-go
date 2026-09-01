package okx

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// MarketService 封装 /api/v5/market/* 行情接口，均为公开接口，无需 API Key。
type MarketService struct{ c *Client }

// Ticker 是单个产品的行情快照。
type Ticker struct {
	InstType  string `json:"instType"`
	InstID    string `json:"instId"`
	Last      Num    `json:"last"`      // 最新成交价
	LastSz    Num    `json:"lastSz"`    // 最新成交数量
	AskPx     Num    `json:"askPx"`     // 卖一价
	AskSz     Num    `json:"askSz"`     // 卖一量
	BidPx     Num    `json:"bidPx"`     // 买一价
	BidSz     Num    `json:"bidSz"`     // 买一量
	Open24h   Num    `json:"open24h"`   // 24 小时开盘价
	High24h   Num    `json:"high24h"`   // 24 小时最高价
	Low24h    Num    `json:"low24h"`    // 24 小时最低价
	VolCcy24h Num    `json:"volCcy24h"` // 24 小时成交额（计价货币）
	Vol24h    Num    `json:"vol24h"`    // 24 小时成交量（张 / 基础货币）
	SodUtc0   Num    `json:"sodUtc0"`   // UTC 0 时开盘价
	SodUtc8   Num    `json:"sodUtc8"`   // UTC 8 时开盘价
	Ts        Num    `json:"ts"`        // 数据产生时间，毫秒
}

// Tickers 获取某类产品全部行情。instType 取值 SPOT / SWAP / FUTURES / OPTION。
// uly 与 instFamily 为可选过滤条件，不需要时传空串。
func (s *MarketService) Tickers(ctx context.Context, instType, uly, instFamily string) ([]Ticker, error) {
	q := newParams().set("instType", instType).set("uly", uly).set("instFamily", instFamily)
	return request[Ticker](ctx, s.c, http.MethodGet, "/api/v5/market/tickers", q.values(), nil, false)
}

// Ticker 获取单个产品行情，如 instId = "ETH-USDT-SWAP"。
func (s *MarketService) Ticker(ctx context.Context, instID string) (Ticker, error) {
	q := newParams().set("instId", instID)
	return requestOne[Ticker](ctx, s.c, http.MethodGet, "/api/v5/market/ticker", q.values(), nil, false)
}

// Candle 是一根 K 线。OKX 原始返回是字符串数组，这里解析成结构体。
type Candle struct {
	Ts    int64   `json:"ts"`    // 开始时间，毫秒
	Open  float64 `json:"open"`  // 开盘价
	High  float64 `json:"high"`  // 最高价
	Low   float64 `json:"low"`   // 最低价
	Close float64 `json:"close"` // 收盘价
	// 以下三个成交量字段只有成交价 K 线有；标记价、指数 K 线恒为 0。
	Vol         float64 `json:"vol"`         // 成交量（张 / 基础货币）
	VolCcy      float64 `json:"volCcy"`      // 成交量（基础货币）
	VolCcyQuote float64 `json:"volCcyQuote"` // 成交额（计价货币）
	Confirm     bool    `json:"confirm"`     // K 线是否已完结；false 表示仍在更新
}

// Time 返回该 K 线的开始时间。
func (c Candle) Time() time.Time { return time.UnixMilli(c.Ts) }

// rawCandle 对应 OKX 返回的字符串数组形式。
//
// 有两种长度：成交价 K 线是 9 段（ts,o,h,l,c,vol,volCcy,volCcyQuote,confirm），
// 标记价与指数 K 线是 6 段（ts,o,h,l,c,confirm）——它们没有成交量。
// 两种格式的 confirm 都在最后一段，按下标 8 硬取会把 6 段格式的成交量读成 confirm。
type rawCandle []string

func (r rawCandle) toCandle() Candle {
	get := func(i int) string {
		if i >= 0 && i < len(r) {
			return r[i]
		}
		return ""
	}
	f := func(i int) float64 {
		v, _ := strconv.ParseFloat(get(i), 64)
		return v
	}
	ts, _ := strconv.ParseInt(get(0), 10, 64)
	c := Candle{Ts: ts, Open: f(1), High: f(2), Low: f(3), Close: f(4)}
	if len(r) >= 6 {
		c.Confirm = get(len(r)-1) == "1"
	}
	if len(r) >= 9 {
		c.Vol, c.VolCcy, c.VolCcyQuote = f(5), f(6), f(7)
	}
	return c
}

// CandlesRequest 是 K 线查询参数。
//
// 注意 OKX 的分页语义与直觉相反：After 表示"请求此时间戳之前（更旧）的数据"，
// Before 表示"请求此时间戳之后（更新）的数据"，单位均为毫秒。
type CandlesRequest struct {
	InstID string // 必填，如 "ETH-USDT-SWAP"
	Bar    string // K 线周期，如 1m/3m/5m/15m/30m/1H/4H/1D，默认 1m
	After  int64  // 可选，返回早于该毫秒时间戳的数据
	Before int64  // 可选，返回晚于该毫秒时间戳的数据
	Limit  int    // 可选，单次条数，最大 300，默认 100
}

func (r CandlesRequest) params() params {
	return newParams().
		set("instId", r.InstID).
		set("bar", r.Bar).
		setInt64("after", r.After).
		setInt64("before", r.Before).
		setInt("limit", r.Limit)
}

// Candles 获取最近的 K 线数据（含未完结的当前一根），最多约 300 条。
// 返回按时间倒序排列（第 0 条最新），与 OKX 原始顺序一致。
func (s *MarketService) Candles(ctx context.Context, req CandlesRequest) ([]Candle, error) {
	return s.candles(ctx, "/api/v5/market/candles", req)
}

// HistoryCandles 获取更久远的历史 K 线，适合回测拉取长周期数据。
func (s *MarketService) HistoryCandles(ctx context.Context, req CandlesRequest) ([]Candle, error) {
	return s.candles(ctx, "/api/v5/market/history-candles", req)
}

func (s *MarketService) candles(ctx context.Context, path string, req CandlesRequest) ([]Candle, error) {
	raws, err := request[rawCandle](ctx, s.c, http.MethodGet, path, req.params().values(), nil, false)
	if err != nil {
		return nil, err
	}
	candles := make([]Candle, 0, len(raws))
	for _, r := range raws {
		candles = append(candles, r.toCandle())
	}
	return candles, nil
}

// OrderBook 是深度数据。Asks / Bids 中每一项为 [价格, 数量, 已弃用字段, 该价位订单数]。
type OrderBook struct {
	Asks []BookLevel `json:"asks"`
	Bids []BookLevel `json:"bids"`
	Ts   Num         `json:"ts"`
}

// BookLevel 是深度中的一档。
type BookLevel struct {
	Px     Num // 价格
	Sz     Num // 数量
	Orders Num // 该价位的订单数量
}

// UnmarshalJSON 把 OKX 的 ["px","sz","0","count"] 数组解析为结构体。
func (b *BookLevel) UnmarshalJSON(data []byte) error {
	var arr []Num
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	if len(arr) > 0 {
		b.Px = arr[0]
	}
	if len(arr) > 1 {
		b.Sz = arr[1]
	}
	if len(arr) > 3 {
		b.Orders = arr[3]
	}
	return nil
}

// Books 获取深度数据，depth 为档位数（最大 400），传 0 使用默认 1 档。
func (s *MarketService) Books(ctx context.Context, instID string, depth int) (OrderBook, error) {
	q := newParams().set("instId", instID).setInt("sz", depth)
	return requestOne[OrderBook](ctx, s.c, http.MethodGet, "/api/v5/market/books", q.values(), nil, false)
}

// Trade 是一笔公开成交记录。
type Trade struct {
	InstID  string `json:"instId"`
	TradeID string `json:"tradeId"`
	Px      Num    `json:"px"`
	Sz      Num    `json:"sz"`
	Side    string `json:"side"` // buy / sell，表示吃单方向
	Ts      Num    `json:"ts"`
}

// Trades 获取最近的公开成交，limit 最大 500，传 0 使用默认 100。
func (s *MarketService) Trades(ctx context.Context, instID string, limit int) ([]Trade, error) {
	q := newParams().set("instId", instID).setInt("limit", limit)
	return request[Trade](ctx, s.c, http.MethodGet, "/api/v5/market/trades", q.values(), nil, false)
}

// MarkPriceCandles 获取标记价 K 线。
//
// OKX 的强平按标记价判定，不是成交价。回测里要建模爆仓就必须用这条序列，
// 用成交价 K 线会在插针行情下得出偏乐观的结果。返回的 Candle 没有成交量。
func (s *MarketService) MarkPriceCandles(ctx context.Context, req CandlesRequest) ([]Candle, error) {
	return s.candles(ctx, "/api/v5/market/mark-price-candles", req)
}

// HistoryMarkPriceCandles 获取更久远的标记价历史 K 线。
func (s *MarketService) HistoryMarkPriceCandles(ctx context.Context, req CandlesRequest) ([]Candle, error) {
	return s.candles(ctx, "/api/v5/market/history-mark-price-candles", req)
}

// IndexCandles 获取指数 K 线。instID 用现货形式，如 "ETH-USDT"。返回的 Candle 没有成交量。
func (s *MarketService) IndexCandles(ctx context.Context, req CandlesRequest) ([]Candle, error) {
	return s.candles(ctx, "/api/v5/market/index-candles", req)
}

// HistoryIndexCandles 获取更久远的指数历史 K 线。
func (s *MarketService) HistoryIndexCandles(ctx context.Context, req CandlesRequest) ([]Candle, error) {
	return s.candles(ctx, "/api/v5/market/history-index-candles", req)
}

// IndexTicker 是指数行情。
type IndexTicker struct {
	InstID  string `json:"instId"`
	IdxPx   Num    `json:"idxPx"` // 最新指数价格
	High24h Num    `json:"high24h"`
	Low24h  Num    `json:"low24h"`
	Open24h Num    `json:"open24h"`
	SodUtc0 Num    `json:"sodUtc0"`
	SodUtc8 Num    `json:"sodUtc8"`
	Ts      Num    `json:"ts"`
}

// IndexTickers 获取指数行情。instID 与 quoteCcy 二选一，instID 形如 "ETH-USDT"。
func (s *MarketService) IndexTickers(ctx context.Context, quoteCcy, instID string) ([]IndexTicker, error) {
	q := newParams().set("quoteCcy", quoteCcy).set("instId", instID)
	return request[IndexTicker](ctx, s.c, http.MethodGet, "/api/v5/market/index-tickers", q.values(), nil, false)
}

// IndexTicker 获取单个指数行情。
func (s *MarketService) IndexTicker(ctx context.Context, instID string) (IndexTicker, error) {
	q := newParams().set("instId", instID)
	return requestOne[IndexTicker](ctx, s.c, http.MethodGet, "/api/v5/market/index-tickers", q.values(), nil, false)
}

// HistoryTrades 获取历史逐笔成交，用于 tick 级回测。
//
// 分页有两种方式，由 method 选择："1" 按 tradeId、"2" 按时间戳（默认）；
// after / before 的含义随之变化，after 取更旧的数据。limit 最大 100。
func (s *MarketService) HistoryTrades(ctx context.Context, instID, after, before, method string, limit int) ([]Trade, error) {
	q := newParams().
		set("instId", instID).
		set("after", after).
		set("before", before).
		set("type", method).
		setInt("limit", limit)
	return request[Trade](ctx, s.c, http.MethodGet, "/api/v5/market/history-trades", q.values(), nil, false)
}
