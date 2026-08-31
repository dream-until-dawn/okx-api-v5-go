package okx

import (
	"context"
	"net/http"
)

// AccountService 封装 /api/v5/account/* 账户接口，全部需要签名。
type AccountService struct{ c *Client }

// Balance 是账户整体资产信息。
//
// 注意：顶层的聚合字段（AdjEq / AvailEq / Imr / Mmr / MgnRatio / Upl / OrdFroz /
// NotionalUsd 等）只在跨币种保证金、组合保证金模式下才有值。实测在最常见的
// 单币种保证金模式（acctLv=2）下它们全部返回空串，真实数据在 [Balance.Details]
// 里逐币种给出——逐仓永续的策略应当直接读 [BalanceDetail]。
type Balance struct {
	TotalEq            Num             `json:"totalEq"`            // 美元计价的账户总权益
	AdjEq              Num             `json:"adjEq"`              // 美元计价的有效保证金
	AvailEq            Num             `json:"availEq"`            // 美元计价的可用保证金
	IsoEq              Num             `json:"isoEq"`              // 美元计价的逐仓仓位权益
	OrdFroz            Num             `json:"ordFroz"`            // 挂单冻结保证金
	Imr                Num             `json:"imr"`                // 初始保证金
	Mmr                Num             `json:"mmr"`                // 维持保证金
	MgnRatio           Num             `json:"mgnRatio"`           // 保证金率
	NotionalUsd        Num             `json:"notionalUsd"`        // 美元计价的持仓名义价值
	NotionalUsdForSwap Num             `json:"notionalUsdForSwap"` // 美元计价的永续合约持仓名义价值
	Upl                Num             `json:"upl"`                // 未实现盈亏
	UTime              Num             `json:"uTime"`              // 更新时间，毫秒
	Details            []BalanceDetail `json:"details"`            // 各币种明细
}

// BalanceDetail 是单个币种的资产明细，逐仓永续策略主要看这里。
//
// 逐仓场景下的字段语义（实测于单币种保证金 + 逐仓 SWAP 账户）：
//   - AvailBal / AvailEq 是真正可动用的余额，开新仓看它；
//   - 逐仓仓位占用的保证金计入 FrozenBal，同时体现在 IsoEq 里；
//   - Eq ≈ CashBal + IsoEq，IsoUpl 是逐仓仓位的未实现盈亏；
//   - MgnRatio / MaxLoan / Liab / Interest 在该模式下恒为空，不要依赖。
type BalanceDetail struct {
	Ccy           string `json:"ccy"`           // 币种
	Eq            Num    `json:"eq"`            // 币种总权益
	CashBal       Num    `json:"cashBal"`       // 币种余额
	AvailBal      Num    `json:"availBal"`      // 可用余额
	AvailEq       Num    `json:"availEq"`       // 可用保证金
	FrozenBal     Num    `json:"frozenBal"`     // 冻结余额
	OrdFrozen     Num    `json:"ordFrozen"`     // 挂单冻结
	EqUsd         Num    `json:"eqUsd"`         // 币种权益的美元价值
	DisEq         Num    `json:"disEq"`         // 币种美元折算权益
	IsoEq         Num    `json:"isoEq"`         // 币种逐仓仓位权益
	IsoUpl        Num    `json:"isoUpl"`        // 逐仓未实现盈亏
	Upl           Num    `json:"upl"`           // 未实现盈亏
	Liab          Num    `json:"liab"`          // 币种负债
	Interest      Num    `json:"interest"`      // 计息
	MgnRatio      Num    `json:"mgnRatio"`      // 保证金率
	Imr           Num    `json:"imr"`           // 初始保证金
	Mmr           Num    `json:"mmr"`           // 维持保证金
	MaxLoan       Num    `json:"maxLoan"`       // 最大可借
	NotionalLever Num    `json:"notionalLever"` // 币种杠杆倍数
	UTime         Num    `json:"uTime"`         // 更新时间，毫秒
}

// Detail 按币种查找明细，未找到时第二个返回值为 false。
func (b Balance) Detail(ccy string) (BalanceDetail, bool) {
	for _, d := range b.Details {
		if d.Ccy == ccy {
			return d, true
		}
	}
	return BalanceDetail{}, false
}

// Balance 查询账户余额。ccy 可传多个币种做过滤，不传则返回全部。
func (s *AccountService) Balance(ctx context.Context, ccy ...string) (Balance, error) {
	q := newParams().setList("ccy", ccy)
	return requestOne[Balance](ctx, s.c, http.MethodGet, "/api/v5/account/balance", q.values(), nil, true)
}

// Position 是一个持仓。
type Position struct {
	InstType string `json:"instType"`
	InstID   string `json:"instId"`
	PosID    string `json:"posId"`    // 持仓 ID
	PosSide  string `json:"posSide"`  // long / short / net
	MgnMode  string `json:"mgnMode"`  // cross（全仓）/ isolated（逐仓）
	Pos      Num    `json:"pos"`      // 持仓数量（张）
	AvailPos Num    `json:"availPos"` // 可平仓数量
	PosCcy   string `json:"posCcy"`   // 仓位资产币种；USDT 本位合约恒为空
	Ccy      string `json:"ccy"`      // 保证金币种
	AvgPx    Num    `json:"avgPx"`    // 开仓均价
	BePx     Num    `json:"bePx"`     // 盈亏平衡价
	MarkPx   Num    `json:"markPx"`   // 标记价格
	LiqPx    Num    `json:"liqPx"`    // 预估强平价
	IdxPx    Num    `json:"idxPx"`    // 指数价格
	Last     Num    `json:"last"`     // 最新成交价
	Lever    Num    `json:"lever"`    // 杠杆倍数
	Margin   Num    `json:"margin"`   // 保证金余额（逐仓）
	MgnRatio Num    `json:"mgnRatio"` // 维持保证金率
	Imr      Num    `json:"imr"`      // 初始保证金；仅全仓有值，逐仓恒为空，逐仓看 Margin
	Mmr      Num    `json:"mmr"`      // 维持保证金
	Upl      Num    `json:"upl"`      // 未实现盈亏（按标记价 MarkPx 计算）
	UplRatio Num    `json:"uplRatio"` // 未实现收益率（按标记价计算）
	// UplLastPx / UplRatioLastPx 按最新成交价 Last 计算。回测通常以成交价撮合，
	// 用这两个字段和回测口径才对得上；OKX 的强平判定则依据按标记价的 Upl。
	UplLastPx      Num `json:"uplLastPx"`
	UplRatioLastPx Num `json:"uplRatioLastPx"`
	RealizedPnl    Num `json:"realizedPnl"` // 已实现盈亏
	Pnl            Num `json:"pnl"`         // 平仓订单累计收益额
	Fee            Num `json:"fee"`         // 累计手续费
	FundingFee     Num `json:"fundingFee"`  // 累计资金费用
	LiqPenalty     Num `json:"liqPenalty"`  // 累计爆仓罚金
	NotionalUsd    Num `json:"notionalUsd"` // 美元计价的持仓名义价值
	Adl            Num `json:"adl"`         // 自动减仓等级
	UsdPx          Num `json:"usdPx"`       // 保证金币种对美元的价格，用于折算 USD 口径
	// CloseOrderAlgo 是挂在该仓位上的止盈止损委托；没有设置时为空数组。
	CloseOrderAlgo []CloseOrderAlgo `json:"closeOrderAlgo"`
	CTime          Num              `json:"cTime"` // 创建时间，毫秒
	UTime          Num              `json:"uTime"` // 更新时间，毫秒
	TradeID        string           `json:"tradeId"`
}

// CloseOrderAlgo 是附加在仓位上的止盈止损委托。
type CloseOrderAlgo struct {
	AlgoID          string `json:"algoId"`
	SlTriggerPx     Num    `json:"slTriggerPx"`     // 止损触发价
	SlTriggerPxType string `json:"slTriggerPxType"` // last / index / mark
	TpTriggerPx     Num    `json:"tpTriggerPx"`     // 止盈触发价
	TpTriggerPxType string `json:"tpTriggerPxType"` // last / index / mark
	CloseFraction   Num    `json:"closeFraction"`   // 平仓比例
}

// Positions 查询当前持仓。三个参数均可选：instType 如 SWAP，instID 可传多个，
// posID 可传多个持仓 ID。
func (s *AccountService) Positions(ctx context.Context, instType string, instID []string, posID []string) ([]Position, error) {
	q := newParams().set("instType", instType).setList("instId", instID).setList("posId", posID)
	return request[Position](ctx, s.c, http.MethodGet, "/api/v5/account/positions", q.values(), nil, true)
}

// PositionHistory 是一条历史持仓记录（仓位从开到关的汇总）。
type PositionHistory struct {
	InstType       string `json:"instType"`
	InstID         string `json:"instId"`
	PosID          string `json:"posId"`
	MgnMode        string `json:"mgnMode"`
	Direction      string `json:"direction"`  // long / short
	PosSide        string `json:"posSide"`    // 持仓方向，双向持仓模式下用它区分多空
	Type           string `json:"type"`       // 平仓类型：1 部分平仓 2 完全平仓 3 强平 ...
	Lever          Num    `json:"lever"`      //
	OpenAvgPx      Num    `json:"openAvgPx"`  // 开仓均价
	CloseAvgPx     Num    `json:"closeAvgPx"` // 平仓均价
	OpenMaxPos     Num    `json:"openMaxPos"` // 最大持仓量
	CloseTotalPos  Num    `json:"closeTotalPos"`
	Pnl            Num    `json:"pnl"`      // 已实现收益
	PnlRatio       Num    `json:"pnlRatio"` // 已实现收益率
	Fee            Num    `json:"fee"`
	FundingFee     Num    `json:"fundingFee"`
	LiqPenalty     Num    `json:"liqPenalty"`
	Ccy            string `json:"ccy"`
	RealizedPnl    Num    `json:"realizedPnl"`
	UTime          Num    `json:"uTime"`
	CTime          Num    `json:"cTime"`
	TriggerPx      Num    `json:"triggerPx"`
	NonSettleAvgPx Num    `json:"nonSettleAvgPx"`
}

// PositionsHistory 查询历史持仓，最多保存近三个月。after / before 为毫秒时间戳，limit 最大 100。
func (s *AccountService) PositionsHistory(ctx context.Context, instType, instID string, after, before int64, limit int) ([]PositionHistory, error) {
	q := newParams().
		set("instType", instType).
		set("instId", instID).
		setInt64("after", after).
		setInt64("before", before).
		setInt("limit", limit)
	return request[PositionHistory](ctx, s.c, http.MethodGet, "/api/v5/account/positions-history", q.values(), nil, true)
}

// Config 是账户配置信息。
type Config struct {
	UID         string `json:"uid"`
	MainUID     string `json:"mainUid"`
	AcctLv      string `json:"acctLv"`     // 账户模式：1 现货 2 币币杠杆 3 跨币种保证金 4 组合保证金
	PosMode     string `json:"posMode"`    // long_short_mode（双向）/ net_mode（单向）
	AutoLoan    bool   `json:"autoLoan"`   // 是否自动借币
	GreeksType  string `json:"greeksType"` // 希腊字母展示方式
	Level       string `json:"level"`      // 手续费等级
	LevelTmp    string `json:"levelTmp"`
	CtIsoMode   string `json:"ctIsoMode"`  // 衍生品逐仓保证金划转模式
	MgnIsoMode  string `json:"mgnIsoMode"` // 杠杆逐仓保证金划转模式
	SpotOffsetT string `json:"spotOffsetType"`
	RoleType    string `json:"roleType"` // 0 普通用户 1 带单者 2 跟单者
	Label       string `json:"label"`
	KycLv       string `json:"kycLv"`
	IP          string `json:"ip"`
	Perm        string `json:"perm"` // API Key 权限：read_only / trade / withdraw
}

// Config 查看账户配置，可用来确认持仓模式（单向 / 双向）与账户模式。
func (s *AccountService) Config(ctx context.Context) (Config, error) {
	return requestOne[Config](ctx, s.c, http.MethodGet, "/api/v5/account/config", nil, nil, true)
}

// LeverageInfo 是杠杆倍数信息。
type LeverageInfo struct {
	InstID  string `json:"instId"`
	Ccy     string `json:"ccy"`
	MgnMode string `json:"mgnMode"`
	PosSide string `json:"posSide"`
	Lever   Num    `json:"lever"`
}

// LeverageInfo 查询杠杆倍数。mgnMode 取值 cross / isolated，instID 可传多个。
func (s *AccountService) LeverageInfo(ctx context.Context, instID []string, mgnMode string) ([]LeverageInfo, error) {
	q := newParams().setList("instId", instID).set("mgnMode", mgnMode)
	return request[LeverageInfo](ctx, s.c, http.MethodGet, "/api/v5/account/leverage-info", q.values(), nil, true)
}

// SetLeverageRequest 是设置杠杆倍数的请求。
type SetLeverageRequest struct {
	InstID  string `json:"instId,omitempty"`  // 产品 ID，逐仓必填
	Ccy     string `json:"ccy,omitempty"`     // 保证金币种，跨币种保证金模式下的全仓杠杆必填
	Lever   Num    `json:"lever"`             // 杠杆倍数，必填
	MgnMode string `json:"mgnMode"`           // cross / isolated，必填
	PosSide string `json:"posSide,omitempty"` // 逐仓 + 双向持仓时必填：long / short
}

// SetLeverage 设置杠杆倍数。
func (s *AccountService) SetLeverage(ctx context.Context, req SetLeverageRequest) ([]LeverageInfo, error) {
	return request[LeverageInfo](ctx, s.c, http.MethodPost, "/api/v5/account/set-leverage", nil, req, true)
}

// MaxSize 是最大可买卖 / 可开仓数量。
type MaxSize struct {
	InstID  string `json:"instId"`
	Ccy     string `json:"ccy"`
	MaxBuy  Num    `json:"maxBuy"`  // 现货为最大可买数量；合约为最大可开多张数
	MaxSell Num    `json:"maxSell"` // 现货为最大可卖数量；合约为最大可开空张数
}

// MaxSize 查询最大可开仓数量。tdMode 取值 cross / isolated / cash，
// px 为委托价（传空表示按市价估算），instID 可传多个。
func (s *AccountService) MaxSize(ctx context.Context, instID []string, tdMode, ccy string, px Num) ([]MaxSize, error) {
	q := newParams().setList("instId", instID).set("tdMode", tdMode).set("ccy", ccy).setNum("px", px)
	return request[MaxSize](ctx, s.c, http.MethodGet, "/api/v5/account/max-size", q.values(), nil, true)
}

// MaxAvailSize 是最大可用数量。
type MaxAvailSize struct {
	InstID    string `json:"instId"`
	AvailBuy  Num    `json:"availBuy"`
	AvailSell Num    `json:"availSell"`
}

// MaxAvailSize 查询最大可用（含已有仓位可平部分）数量。
// reduceOnly 为 true 时只计算可平仓量。
func (s *AccountService) MaxAvailSize(ctx context.Context, instID []string, tdMode, ccy string, reduceOnly bool) ([]MaxAvailSize, error) {
	q := newParams().setList("instId", instID).set("tdMode", tdMode).set("ccy", ccy).setBool("reduceOnly", reduceOnly)
	return request[MaxAvailSize](ctx, s.c, http.MethodGet, "/api/v5/account/max-avail-size", q.values(), nil, true)
}

// SetPositionMode 设置持仓模式。posMode 取值 long_short_mode（双向持仓）/ net_mode（单向持仓）。
// 仅在没有任何持仓和挂单时可以切换。
func (s *AccountService) SetPositionMode(ctx context.Context, posMode string) (string, error) {
	type result struct {
		PosMode string `json:"posMode"`
	}
	body := map[string]string{"posMode": posMode}
	v, err := requestOne[result](ctx, s.c, http.MethodPost, "/api/v5/account/set-position-mode", nil, body, true)
	return v.PosMode, err
}

// Bill 是账单流水记录，是核对手续费与资金费用的权威来源。
//
// 逐仓场景下有个容易踩的坑：资金费用（Type="8"）**不走账户余额**，
// BalChg 为 0，实际扣减记在 PosBalChg（仓位保证金变动）上。只统计 BalChg
// 会把全部资金费漏掉，逐仓永续的对账应同时看 BalChg 与 PosBalChg。
type Bill struct {
	BillID    string `json:"billId"`
	InstID    string `json:"instId"`
	InstType  string `json:"instType"`
	Type      string `json:"type"`    // 账单类型：1 划转 2 交易 8 资金费 ...
	SubType   string `json:"subType"` // 账单子类型
	Ccy       string `json:"ccy"`
	Bal       Num    `json:"bal"`    // 账户层面的余额
	BalChg    Num    `json:"balChg"` // 账户层面的余额变动
	Sz        Num    `json:"sz"`     // 数量
	Px        Num    `json:"px"`     // 价格
	Pnl       Num    `json:"pnl"`    // 收益
	Fee       Num    `json:"fee"`    // 手续费
	OrdID     string `json:"ordId"`
	ClOrdID   string `json:"clOrdId"`
	Tag       string `json:"tag"`
	TradeID   string `json:"tradeId"`
	MgnMode   string `json:"mgnMode"`   // cross / isolated
	ExecType  string `json:"execType"`  // T taker / M maker
	Interest  Num    `json:"interest"`  // 利息
	FillIdxPx Num    `json:"fillIdxPx"` // 成交时的指数价格
	PosBal    Num    `json:"posBal"`    // 仓位余额
	PosBalChg Num    `json:"posBalChg"` // 仓位余额变动
	From      string `json:"from"`      // 转出账户（划转类账单）
	To        string `json:"to"`        // 转入账户（划转类账单）
	Notes     string `json:"notes"`
	FillTime  Num    `json:"fillTime"`
	Ts        Num    `json:"ts"`
}

// Bills 查询最近 7 天的账单流水。instType / ccy / billType / subType 均为可选过滤条件。
func (s *AccountService) Bills(ctx context.Context, instType, ccy, billType, subType string, limit int) ([]Bill, error) {
	q := newParams().
		set("instType", instType).
		set("ccy", ccy).
		set("type", billType).
		set("subType", subType).
		setInt("limit", limit)
	return request[Bill](ctx, s.c, http.MethodGet, "/api/v5/account/bills", q.values(), nil, true)
}
