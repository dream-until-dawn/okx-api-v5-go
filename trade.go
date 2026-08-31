package okx

import (
	"context"
	"net/http"
)

// TradeService 封装 /api/v5/trade/* 交易接口，全部需要签名。
type TradeService struct{ c *Client }

// 交易方向。
const (
	SideBuy  = "buy"
	SideSell = "sell"
)

// 持仓方向（双向持仓模式下必填）。
const (
	PosSideLong  = "long"
	PosSideShort = "short"
	PosSideNet   = "net"
)

// 交易模式。
const (
	TdModeCross    = "cross"    // 全仓
	TdModeIsolated = "isolated" // 逐仓
	TdModeCash     = "cash"     // 非保证金（现货）
)

// 订单类型。
const (
	OrdTypeMarket          = "market"            // 市价单
	OrdTypeLimit           = "limit"             // 限价单
	OrdTypePostOnly        = "post_only"         // 只做 maker
	OrdTypeFOK             = "fok"               // 全部成交或立即取消
	OrdTypeIOC             = "ioc"               // 立即成交并取消剩余
	OrdTypeOptimalLimitIOC = "optimal_limit_ioc" // 市价委托立即成交并取消剩余（仅合约）
)

// 订单状态。
const (
	StateLive            = "live"             // 等待成交
	StatePartiallyFilled = "partially_filled" // 部分成交
	StateFilled          = "filled"           // 完全成交
	StateCanceled        = "canceled"         // 已撤单
)

// OrderRequest 是下单请求。字段与 OKX /api/v5/trade/order 一致。
type OrderRequest struct {
	InstID  string `json:"instId"`            // 必填，产品 ID，如 ETH-USDT-SWAP
	TdMode  string `json:"tdMode"`            // 必填，cross / isolated / cash
	Side    string `json:"side"`              // 必填，buy / sell
	OrdType string `json:"ordType"`           // 必填，market / limit / post_only / fok / ioc
	Sz      Num    `json:"sz"`                // 必填，委托数量（合约为张数）
	Px      Num    `json:"px,omitempty"`      // 限价单必填
	PosSide string `json:"posSide,omitempty"` // 双向持仓模式下的合约交易必填：long / short
	Ccy     string `json:"ccy,omitempty"`     // 保证金币种，仅用于单币种保证金模式的全仓杠杆

	ClOrdID string `json:"clOrdId,omitempty"` // 客户自定义订单 ID，字母数字，1-32 位
	Tag     string `json:"tag,omitempty"`     // 订单标签；留空时使用 WithBrokerTag 配置的默认值

	ReduceOnly bool   `json:"reduceOnly,omitempty"` // 是否只减仓
	TgtCcy     string `json:"tgtCcy,omitempty"`     // 现货市价单委托数量单位：base_ccy / quote_ccy

	// 下单附带的止盈止损（简化版，只支持一组）。
	TpTriggerPx Num `json:"tpTriggerPx,omitempty"` // 止盈触发价
	TpOrdPx     Num `json:"tpOrdPx,omitempty"`     // 止盈委托价，-1 表示市价
	SlTriggerPx Num `json:"slTriggerPx,omitempty"` // 止损触发价
	SlOrdPx     Num `json:"slOrdPx,omitempty"`     // 止损委托价，-1 表示市价
}

// OrderResult 是下单 / 撤单 / 改单的返回结果。
type OrderResult struct {
	OrdID   string `json:"ordId"`
	ClOrdID string `json:"clOrdId"`
	Tag     string `json:"tag"`
	Ts      Num    `json:"ts"`
	SCode   string `json:"sCode"` // "0" 表示该笔成功
	SMsg    string `json:"sMsg"`
}

// OK 报告该笔操作是否成功。批量接口中即使整体返回 code=2，也要逐条检查。
func (r OrderResult) OK() bool { return r.SCode == "0" || r.SCode == "" }

// applyDefaults 补上客户端配置的默认 broker tag。
func (c *Client) applyOrderDefaults(req *OrderRequest) {
	if req.Tag == "" {
		req.Tag = c.opt.brokerTag
	}
}

// PlaceOrder 下单。返回的 OrderResult 里带有 OrdID。
//
// 注意：即使 err 为 nil，也建议检查 OKX 返回的 SCode；当整体 code 非 "0" 时，
// err 为 *APIError 且 SCode / SMsg 已填充具体原因。
func (s *TradeService) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	s.c.applyOrderDefaults(&req)
	return requestOne[OrderResult](ctx, s.c, http.MethodPost, "/api/v5/trade/order", nil, req, true)
}

// PlaceOrders 批量下单，单次最多 20 笔。
// 部分失败时返回 *APIError（code = "2"），同时结果切片仍然可用，逐条检查 OK 即可。
func (s *TradeService) PlaceOrders(ctx context.Context, reqs []OrderRequest) ([]OrderResult, error) {
	for i := range reqs {
		s.c.applyOrderDefaults(&reqs[i])
	}
	return request[OrderResult](ctx, s.c, http.MethodPost, "/api/v5/trade/batch-orders", nil, reqs, true)
}

// CancelOrderRequest 是撤单请求，OrdID 与 ClOrdID 至少填一个。
type CancelOrderRequest struct {
	InstID  string `json:"instId"`
	OrdID   string `json:"ordId,omitempty"`
	ClOrdID string `json:"clOrdId,omitempty"`
}

// CancelOrder 撤销单个订单。
func (s *TradeService) CancelOrder(ctx context.Context, req CancelOrderRequest) (OrderResult, error) {
	return requestOne[OrderResult](ctx, s.c, http.MethodPost, "/api/v5/trade/cancel-order", nil, req, true)
}

// CancelOrders 批量撤单，单次最多 20 笔。
func (s *TradeService) CancelOrders(ctx context.Context, reqs []CancelOrderRequest) ([]OrderResult, error) {
	return request[OrderResult](ctx, s.c, http.MethodPost, "/api/v5/trade/cancel-batch-orders", nil, reqs, true)
}

// AmendOrderRequest 是改单请求。OrdID 与 ClOrdID 至少填一个，
// NewSz 与 NewPx 至少填一个。
type AmendOrderRequest struct {
	InstID    string `json:"instId"`
	OrdID     string `json:"ordId,omitempty"`
	ClOrdID   string `json:"clOrdId,omitempty"`
	ReqID     string `json:"reqId,omitempty"`     // 用户自定义的修改事件 ID
	NewSz     Num    `json:"newSz,omitempty"`     // 修改后的数量
	NewPx     Num    `json:"newPx,omitempty"`     // 修改后的价格
	CxlOnFail bool   `json:"cxlOnFail,omitempty"` // 修改失败时是否自动撤单
}

// AmendOrder 修改未成交的订单。
func (s *TradeService) AmendOrder(ctx context.Context, req AmendOrderRequest) (OrderResult, error) {
	return requestOne[OrderResult](ctx, s.c, http.MethodPost, "/api/v5/trade/amend-order", nil, req, true)
}

// ClosePositionRequest 是市价全平请求。
type ClosePositionRequest struct {
	InstID  string `json:"instId"`            // 必填
	MgnMode string `json:"mgnMode"`           // 必填，cross / isolated
	PosSide string `json:"posSide,omitempty"` // 双向持仓必填：long / short
	Ccy     string `json:"ccy,omitempty"`     // 保证金币种（单币种保证金模式的全仓杠杆）
	AutoCxl bool   `json:"autoCxl,omitempty"` // 存在挂单时是否自动撤单后平仓
	ClOrdID string `json:"clOrdId,omitempty"` //
	Tag     string `json:"tag,omitempty"`     // 留空时使用 WithBrokerTag 配置的默认值
}

// ClosePositionResult 是市价全平的返回。
type ClosePositionResult struct {
	InstID  string `json:"instId"`
	PosSide string `json:"posSide"`
	ClOrdID string `json:"clOrdId"`
	Tag     string `json:"tag"`
}

// ClosePosition 市价全平指定方向的持仓。
func (s *TradeService) ClosePosition(ctx context.Context, req ClosePositionRequest) (ClosePositionResult, error) {
	if req.Tag == "" {
		req.Tag = s.c.opt.brokerTag
	}
	return requestOne[ClosePositionResult](ctx, s.c, http.MethodPost, "/api/v5/trade/close-position", nil, req, true)
}

// Order 是订单详情。
type Order struct {
	InstType    string `json:"instType"`
	InstID      string `json:"instId"`
	OrdID       string `json:"ordId"`
	ClOrdID     string `json:"clOrdId"`
	Tag         string `json:"tag"`
	Px          Num    `json:"px"`           // 委托价格
	Sz          Num    `json:"sz"`           // 委托数量
	OrdType     string `json:"ordType"`      // 订单类型
	Side        string `json:"side"`         // buy / sell
	PosSide     string `json:"posSide"`      // long / short / net
	TdMode      string `json:"tdMode"`       // cross / isolated / cash
	AccFillSz   Num    `json:"accFillSz"`    // 累计成交数量
	FillPx      Num    `json:"fillPx"`       // 最新成交价
	FillSz      Num    `json:"fillSz"`       // 最新成交数量
	AvgPx       Num    `json:"avgPx"`        // 成交均价
	State       string `json:"state"`        // live / partially_filled / filled / canceled
	Lever       Num    `json:"lever"`        // 杠杆倍数
	Fee         Num    `json:"fee"`          // 手续费（负数为支出）
	FeeCcy      string `json:"feeCcy"`       // 手续费币种
	Pnl         Num    `json:"pnl"`          // 收益
	ReduceOnly  string `json:"reduceOnly"`   // 是否只减仓
	Ccy         string `json:"ccy"`          //
	Source      string `json:"source"`       //
	Category    string `json:"category"`     // normal / twap / adl / full_liquidation ...
	CancelSrc   string `json:"cancelSource"` // 撤单原因码
	TpTriggerPx Num    `json:"tpTriggerPx"`  //
	TpOrdPx     Num    `json:"tpOrdPx"`      //
	SlTriggerPx Num    `json:"slTriggerPx"`  //
	SlOrdPx     Num    `json:"slOrdPx"`      //
	CTime       Num    `json:"cTime"`        // 创建时间，毫秒
	UTime       Num    `json:"uTime"`        // 更新时间，毫秒
	FillTime    Num    `json:"fillTime"`     // 最新成交时间，毫秒
}

// Order 查询单个订单详情，ordID 与 clOrdID 至少填一个。
func (s *TradeService) Order(ctx context.Context, instID, ordID, clOrdID string) (Order, error) {
	q := newParams().set("instId", instID).set("ordId", ordID).set("clOrdId", clOrdID)
	return requestOne[Order](ctx, s.c, http.MethodGet, "/api/v5/trade/order", q.values(), nil, true)
}

// OrderListRequest 是订单列表查询条件，所有字段均可选。
type OrderListRequest struct {
	InstType string // SPOT / SWAP / FUTURES / OPTION / MARGIN
	InstID   string
	OrdType  string
	State    string // live / partially_filled / filled / canceled
	After    string // 分页游标：返回 ordId 小于该值的记录（更旧）
	Before   string // 分页游标：返回 ordId 大于该值的记录（更新）
	Begin    int64  // 起始时间，毫秒
	End      int64  // 结束时间，毫秒
	Limit    int    // 最大 100
}

func (r OrderListRequest) params() params {
	return newParams().
		set("instType", r.InstType).
		set("instId", r.InstID).
		set("ordType", r.OrdType).
		set("state", r.State).
		set("after", r.After).
		set("before", r.Before).
		setInt64("begin", r.Begin).
		setInt64("end", r.End).
		setInt("limit", r.Limit)
}

// PendingOrders 查询当前未成交订单。
func (s *TradeService) PendingOrders(ctx context.Context, req OrderListRequest) ([]Order, error) {
	return request[Order](ctx, s.c, http.MethodGet, "/api/v5/trade/orders-pending", req.params().values(), nil, true)
}

// OrdersHistory 查询近 7 天的历史订单（已完结）。
func (s *TradeService) OrdersHistory(ctx context.Context, req OrderListRequest) ([]Order, error) {
	return request[Order](ctx, s.c, http.MethodGet, "/api/v5/trade/orders-history", req.params().values(), nil, true)
}

// OrdersHistoryArchive 查询近 3 个月的历史订单。
func (s *TradeService) OrdersHistoryArchive(ctx context.Context, req OrderListRequest) ([]Order, error) {
	return request[Order](ctx, s.c, http.MethodGet, "/api/v5/trade/orders-history-archive", req.params().values(), nil, true)
}

// Fill 是一条成交明细。
type Fill struct {
	InstType   string `json:"instType"`
	InstID     string `json:"instId"`
	TradeID    string `json:"tradeId"`
	OrdID      string `json:"ordId"`
	ClOrdID    string `json:"clOrdId"`
	BillID     string `json:"billId"`
	Tag        string `json:"tag"`
	FillPx     Num    `json:"fillPx"`
	FillSz     Num    `json:"fillSz"`
	Side       string `json:"side"`
	PosSide    string `json:"posSide"`
	ExecType   string `json:"execType"` // T taker / M maker
	FeeCcy     string `json:"feeCcy"`
	Fee        Num    `json:"fee"`
	FillPnl    Num    `json:"fillPnl"`
	FillFwdPx  Num    `json:"fillFwdPx"`
	FillMarkPx Num    `json:"fillMarkPx"`
	SubType    string `json:"subType"`
	Ts         Num    `json:"ts"`
	FillTime   Num    `json:"fillTime"`
	FeeRate    Num    `json:"feeRate"`
}

// Fills 查询近 3 天的成交明细。instType / instID / ordID 均可选，limit 最大 100。
func (s *TradeService) Fills(ctx context.Context, instType, instID, ordID string, limit int) ([]Fill, error) {
	q := newParams().
		set("instType", instType).
		set("instId", instID).
		set("ordId", ordID).
		setInt("limit", limit)
	return request[Fill](ctx, s.c, http.MethodGet, "/api/v5/trade/fills", q.values(), nil, true)
}

// FillsHistory 查询近 3 个月的成交明细。
func (s *TradeService) FillsHistory(ctx context.Context, instType, instID, ordID string, limit int) ([]Fill, error) {
	q := newParams().
		set("instType", instType).
		set("instId", instID).
		set("ordId", ordID).
		setInt("limit", limit)
	return request[Fill](ctx, s.c, http.MethodGet, "/api/v5/trade/fills-history", q.values(), nil, true)
}

// AlgoOrderRequest 是策略委托（止盈止损 / 计划委托）请求。
type AlgoOrderRequest struct {
	InstID        string `json:"instId"`                  // 必填
	TdMode        string `json:"tdMode"`                  // 必填
	Side          string `json:"side"`                    // 必填
	OrdType       string `json:"ordType"`                 // 必填：conditional（单向止盈止损）/ oco / trigger / move_order_stop
	Sz            Num    `json:"sz,omitempty"`            // 委托数量
	PosSide       string `json:"posSide,omitempty"`       //
	Ccy           string `json:"ccy,omitempty"`           //
	Tag           string `json:"tag,omitempty"`           //
	AlgoClOrdID   string `json:"algoClOrdId,omitempty"`   // 客户自定义策略订单 ID
	ReduceOnly    bool   `json:"reduceOnly,omitempty"`    //
	CloseFraction Num    `json:"closeFraction,omitempty"` // 平仓比例，传 "1" 表示全平

	// conditional / oco 用：
	TpTriggerPx     Num    `json:"tpTriggerPx,omitempty"`     // 止盈触发价
	TpTriggerPxType string `json:"tpTriggerPxType,omitempty"` // last / index / mark
	TpOrdPx         Num    `json:"tpOrdPx,omitempty"`         // 止盈委托价，-1 为市价
	SlTriggerPx     Num    `json:"slTriggerPx,omitempty"`     // 止损触发价
	SlTriggerPxType string `json:"slTriggerPxType,omitempty"` //
	SlOrdPx         Num    `json:"slOrdPx,omitempty"`         // 止损委托价，-1 为市价

	// trigger（计划委托）用：
	TriggerPx     Num    `json:"triggerPx,omitempty"`
	TriggerPxType string `json:"triggerPxType,omitempty"`
	OrderPx       Num    `json:"orderPx,omitempty"` // -1 为市价
}

// AlgoOrderResult 是策略委托的返回结果。
type AlgoOrderResult struct {
	AlgoID      string `json:"algoId"`
	AlgoClOrdID string `json:"algoClOrdId"`
	ClOrdID     string `json:"clOrdId"`
	Tag         string `json:"tag"`
	SCode       string `json:"sCode"`
	SMsg        string `json:"sMsg"`
}

// OK 报告该笔策略委托是否成功。
func (r AlgoOrderResult) OK() bool { return r.SCode == "0" || r.SCode == "" }

// PlaceAlgoOrder 下策略委托单（止盈止损、计划委托等）。
func (s *TradeService) PlaceAlgoOrder(ctx context.Context, req AlgoOrderRequest) (AlgoOrderResult, error) {
	if req.Tag == "" {
		req.Tag = s.c.opt.brokerTag
	}
	return requestOne[AlgoOrderResult](ctx, s.c, http.MethodPost, "/api/v5/trade/order-algo", nil, req, true)
}

// CancelAlgoRequest 是撤销策略委托的请求。
type CancelAlgoRequest struct {
	AlgoID string `json:"algoId"`
	InstID string `json:"instId"`
}

// CancelAlgoOrders 撤销未触发的策略委托，单次最多 10 笔。
func (s *TradeService) CancelAlgoOrders(ctx context.Context, reqs []CancelAlgoRequest) ([]AlgoOrderResult, error) {
	return request[AlgoOrderResult](ctx, s.c, http.MethodPost, "/api/v5/trade/cancel-algos", nil, reqs, true)
}

// AlgoOrder 是策略委托的详情。
type AlgoOrder struct {
	InstType    string `json:"instType"`
	InstID      string `json:"instId"`
	AlgoID      string `json:"algoId"`
	AlgoClOrdID string `json:"algoClOrdId"`
	OrdID       string `json:"ordId"`
	OrdType     string `json:"ordType"`
	Side        string `json:"side"`
	PosSide     string `json:"posSide"`
	TdMode      string `json:"tdMode"`
	Sz          Num    `json:"sz"`
	State       string `json:"state"` // live / pause / effective / canceled / order_failed
	Lever       Num    `json:"lever"`
	TpTriggerPx Num    `json:"tpTriggerPx"`
	TpOrdPx     Num    `json:"tpOrdPx"`
	SlTriggerPx Num    `json:"slTriggerPx"`
	SlOrdPx     Num    `json:"slOrdPx"`
	TriggerPx   Num    `json:"triggerPx"`
	OrderPx     Num    `json:"ordPx"`
	ActualPx    Num    `json:"actualPx"`
	ActualSide  string `json:"actualSide"`
	TriggerTime Num    `json:"triggerTime"`
	CTime       Num    `json:"cTime"`
}

// PendingAlgoOrders 查询未触发的策略委托。ordType 必填（conditional / oco / trigger / move_order_stop）。
func (s *TradeService) PendingAlgoOrders(ctx context.Context, instType, instID, ordType string, limit int) ([]AlgoOrder, error) {
	q := newParams().
		set("instType", instType).
		set("instId", instID).
		set("ordType", ordType).
		setInt("limit", limit)
	return request[AlgoOrder](ctx, s.c, http.MethodGet, "/api/v5/trade/orders-algo-pending", q.values(), nil, true)
}

// AlgoOrdersHistory 查询已完结的策略委托。state 与 algoID 至少填一个，
// state 取值 effective / canceled / order_failed。
func (s *TradeService) AlgoOrdersHistory(ctx context.Context, instType, instID, ordType, state, algoID string, limit int) ([]AlgoOrder, error) {
	q := newParams().
		set("instType", instType).
		set("instId", instID).
		set("ordType", ordType).
		set("state", state).
		set("algoId", algoID).
		setInt("limit", limit)
	return request[AlgoOrder](ctx, s.c, http.MethodGet, "/api/v5/trade/orders-algo-history", q.values(), nil, true)
}
