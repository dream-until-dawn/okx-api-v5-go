package okx

import (
	"context"
	"net/http"
)

// PublicService 封装 /api/v5/public/* 公共数据接口，无需 API Key。
type PublicService struct{ c *Client }

// Instrument 是产品（交易对 / 合约）的基础信息，下单前用来获取下单精度与合约面值。
type Instrument struct {
	InstType  string `json:"instType"`
	InstID    string `json:"instId"`
	Uly       string `json:"uly"`        // 标的指数，如 ETH-USDT
	InstFamil string `json:"instFamily"` // 交易品种
	BaseCcy   string `json:"baseCcy"`    // 交易货币（币对）
	QuoteCcy  string `json:"quoteCcy"`   // 计价货币（币对）
	SettleCcy string `json:"settleCcy"`  // 盈亏结算货币（合约）
	CtVal     Num    `json:"ctVal"`      // 合约面值
	CtMult    Num    `json:"ctMult"`     // 合约乘数
	CtValCcy  string `json:"ctValCcy"`   // 合约面值计价货币
	ListTime  Num    `json:"listTime"`   // 上线时间，毫秒
	ExpTime   Num    `json:"expTime"`    // 到期时间，毫秒
	Lever     Num    `json:"lever"`      // 最大杠杆倍数
	TickSz    Num    `json:"tickSz"`     // 下单价格精度（最小变动价位）
	LotSz     Num    `json:"lotSz"`      // 下单数量精度
	MinSz     Num    `json:"minSz"`      // 最小下单数量
	CtType    string `json:"ctType"`     // linear（正向）/ inverse（反向）
	State     string `json:"state"`      // live / suspend / preopen / expired
	MaxLmtSz  Num    `json:"maxLmtSz"`   // 限价单最大委托数量
	MaxMktSz  Num    `json:"maxMktSz"`   // 市价单最大委托数量
	// 策略委托的数量上限，下 [TradeService.PlaceAlgoOrder] 前应据此校验。
	MaxTriggerSz Num `json:"maxTriggerSz"` // 计划委托单最大委托数量
	MaxStopSz    Num `json:"maxStopSz"`    // 止盈止损单最大委托数量
}

// Instruments 获取产品列表。instType 必填（SPOT / SWAP / FUTURES / OPTION），
// 其余为可选过滤条件。
func (s *PublicService) Instruments(ctx context.Context, instType, uly, instFamily, instID string) ([]Instrument, error) {
	q := newParams().
		set("instType", instType).
		set("uly", uly).
		set("instFamily", instFamily).
		set("instId", instID)
	return request[Instrument](ctx, s.c, http.MethodGet, "/api/v5/public/instruments", q.values(), nil, false)
}

// Instrument 获取单个产品信息，是 [PublicService.Instruments] 的便捷封装。
func (s *PublicService) Instrument(ctx context.Context, instType, instID string) (Instrument, error) {
	q := newParams().set("instType", instType).set("instId", instID)
	return requestOne[Instrument](ctx, s.c, http.MethodGet, "/api/v5/public/instruments", q.values(), nil, false)
}

// MarkPrice 是标记价格。
type MarkPrice struct {
	InstType string `json:"instType"`
	InstID   string `json:"instId"`
	MarkPx   Num    `json:"markPx"`
	Ts       Num    `json:"ts"`
}

// MarkPrices 获取标记价格。instType 必填（MARGIN / SWAP / FUTURES / OPTION）。
func (s *PublicService) MarkPrices(ctx context.Context, instType, uly, instID string) ([]MarkPrice, error) {
	q := newParams().set("instType", instType).set("uly", uly).set("instId", instID)
	return request[MarkPrice](ctx, s.c, http.MethodGet, "/api/v5/public/mark-price", q.values(), nil, false)
}

// FundingRate 是永续合约资金费率。
type FundingRate struct {
	InstType        string `json:"instType"`
	InstID          string `json:"instId"`
	FundingRate     Num    `json:"fundingRate"`     // 当期资金费率
	NextFundingRate Num    `json:"nextFundingRate"` // 下期预测资金费率
	FundingTime     Num    `json:"fundingTime"`     // 当期结算时间，毫秒
	NextFundingTime Num    `json:"nextFundingTime"` // 下期结算时间，毫秒
	Method          string `json:"method"`
}

// FundingRate 获取永续合约当前资金费率。
func (s *PublicService) FundingRate(ctx context.Context, instID string) (FundingRate, error) {
	q := newParams().set("instId", instID)
	return requestOne[FundingRate](ctx, s.c, http.MethodGet, "/api/v5/public/funding-rate", q.values(), nil, false)
}

// FundingRateHistoryItem 是历史资金费率的一条记录。
type FundingRateHistoryItem struct {
	InstType     string `json:"instType"`
	InstID       string `json:"instId"`
	FundingRate  Num    `json:"fundingRate"`
	RealizedRate Num    `json:"realizedRate"`
	FundingTime  Num    `json:"fundingTime"`
	Method       string `json:"method"`
}

// FundingRateHistory 获取历史资金费率。after / before 为毫秒时间戳，limit 最大 100。
func (s *PublicService) FundingRateHistory(ctx context.Context, instID string, after, before int64, limit int) ([]FundingRateHistoryItem, error) {
	q := newParams().
		set("instId", instID).
		setInt64("after", after).
		setInt64("before", before).
		setInt("limit", limit)
	return request[FundingRateHistoryItem](ctx, s.c, http.MethodGet, "/api/v5/public/funding-rate-history", q.values(), nil, false)
}

// ServerTime 返回 OKX 服务器时间（毫秒时间戳），可用于校准本地时钟避免签名过期。
func (s *PublicService) ServerTime(ctx context.Context) (Num, error) {
	type ts struct {
		Ts Num `json:"ts"`
	}
	v, err := requestOne[ts](ctx, s.c, http.MethodGet, "/api/v5/public/time", nil, nil, false)
	return v.Ts, err
}

// PositionTier 是一档仓位档位信息。
//
// 维持保证金率 MMR 随仓位大小分档跳变，不是常数。回测里若用固定 MMR 估算强平价，
// 仓位一大就会算错——档位越高 MMR 越大、可用杠杆越低，强平价会比估计的更近。
type PositionTier struct {
	InstType     string `json:"instType"`
	InstID       string `json:"instId"`
	InstFamily   string `json:"instFamily"`
	Uly          string `json:"uly"`
	Tier         Num    `json:"tier"`         // 档位序号
	MinSz        Num    `json:"minSz"`        // 该档的最小持仓量（张），不含
	MaxSz        Num    `json:"maxSz"`        // 该档的最大持仓量（张），含
	MMR          Num    `json:"mmr"`          // 维持保证金率
	IMR          Num    `json:"imr"`          // 最低初始保证金率
	MaxLever     Num    `json:"maxLever"`     // 该档最大可用杠杆
	OptMgnFactor Num    `json:"optMgnFactor"` // 期权保证金系数
	QuoteMaxLoan Num    `json:"quoteMaxLoan"`
	BaseMaxLoan  Num    `json:"baseMaxLoan"`
}

// PositionTiers 查询仓位档位。instType 与 tdMode 必填，
// 合约还需要 instFamily 或 uly（如 "ETH-USDT"）。
func (s *PublicService) PositionTiers(ctx context.Context, instType, tdMode, instFamily, uly, instID string, tier string) ([]PositionTier, error) {
	q := newParams().
		set("instType", instType).
		set("tdMode", tdMode).
		set("instFamily", instFamily).
		set("uly", uly).
		set("instId", instID).
		set("tier", tier)
	return request[PositionTier](ctx, s.c, http.MethodGet, "/api/v5/public/position-tiers", q.values(), nil, false)
}

// TierFor 在档位表里找出容纳给定持仓量（张）的那一档。
//
// 档位区间是左开右闭：(MinSz, MaxSz]。找不到时第二个返回值为 false，
// 通常意味着仓位超出了该品种的最高档上限。
func TierFor(tiers []PositionTier, sz float64) (PositionTier, bool) {
	for _, t := range tiers {
		if sz > t.MinSz.Float64() && sz <= t.MaxSz.Float64() {
			return t, true
		}
	}
	return PositionTier{}, false
}

// OpenInterest 是持仓总量。
type OpenInterest struct {
	InstType string `json:"instType"`
	InstID   string `json:"instId"`
	OI       Num    `json:"oi"`    // 持仓量，张
	OICcy    Num    `json:"oiCcy"` // 持仓量，币
	OIUsd    Num    `json:"oiUsd"` // 持仓量，美元
	Ts       Num    `json:"ts"`
}

// OpenInterests 获取持仓总量。instType 必填。
func (s *PublicService) OpenInterests(ctx context.Context, instType, uly, instFamily, instID string) ([]OpenInterest, error) {
	q := newParams().
		set("instType", instType).
		set("uly", uly).
		set("instFamily", instFamily).
		set("instId", instID)
	return request[OpenInterest](ctx, s.c, http.MethodGet, "/api/v5/public/open-interest", q.values(), nil, false)
}

// PriceLimit 是当前的限价范围。回测里模拟挂单是否会被交易所拒绝时用得上。
type PriceLimit struct {
	InstType string `json:"instType"`
	InstID   string `json:"instId"`
	BuyLmt   Num    `json:"buyLmt"`  // 最高买价
	SellLmt  Num    `json:"sellLmt"` // 最低卖价
	Enabled  bool   `json:"enabled"` // 限价是否生效
	Ts       Num    `json:"ts"`
}

// PriceLimit 获取指定产品的限价范围。
func (s *PublicService) PriceLimit(ctx context.Context, instID string) (PriceLimit, error) {
	q := newParams().set("instId", instID)
	return requestOne[PriceLimit](ctx, s.c, http.MethodGet, "/api/v5/public/price-limit", q.values(), nil, false)
}
