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
	Ts          int64   `json:"ts"`          // 开始时间，毫秒
	Open        float64 `json:"open"`        // 开盘价
	High        float64 `json:"high"`        // 最高价
	Low         float64 `json:"low"`         // 最低价
	Close       float64 `json:"close"`       // 收盘价
	Vol         float64 `json:"vol"`         // 成交量（张 / 基础货币）
	VolCcy      float64 `json:"volCcy"`      // 成交量（基础货币）
	VolCcyQuote float64 `json:"volCcyQuote"` // 成交额（计价货币）
	Confirm     bool    `json:"confirm"`     // K 线是否已完结；false 表示仍在更新
}

// Time 返回该 K 线的开始时间。
func (c Candle) Time() time.Time { return time.UnixMilli(c.Ts) }

// rawCandle 对应 OKX 返回的字符串数组形式。
type rawCandle []string

func (r rawCandle) toCandle() Candle {
	get := func(i int) string {
		if i < len(r) {
			return r[i]
		}
		return ""
	}
	f := func(i int) float64 {
		v, _ := strconv.ParseFloat(get(i), 64)
		return v
	}
	ts, _ := strconv.ParseInt(get(0), 10, 64)
	return Candle{
		Ts:          ts,
		Open:        f(1),
		High:        f(2),
		Low:         f(3),
		Close:       f(4),
		Vol:         f(5),
		VolCcy:      f(6),
		VolCcyQuote: f(7),
		Confirm:     get(8) == "1",
	}
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
