# okx-api-v5-go

一个使用 Go 实现的 OKX V5 API SDK，仅包含常用接口，可直接被其他 Go 项目引入。

- 零业务耦合：不依赖任何配置框架或日志库，日志、限流、HTTP Client 都可注入
- REST：行情 / 公共数据 / 账户 / 交易，内置签名、重试、模拟盘切换
- WebSocket：公共、业务、私有三类连接，自动登录、心跳、断线重连与重订阅
- 唯一外部依赖：`github.com/gorilla/websocket`

## 安装

```bash
go get github.com/dream-until-dawn/okx-api-v5-go
```

要求 Go 1.22 以上。

## 快速开始

```go
package main

import (
	"context"
	"fmt"
	"log"

	okx "github.com/dream-until-dawn/okx-api-v5-go"
)

func main() {
	client, err := okx.NewClient(
		okx.WithCredentials("apiKey", "secretKey", "passphrase"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	ticker, err := client.Market.Ticker(ctx, "ETH-USDT-SWAP")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("最新价:", ticker.Last.Float64())

	balance, err := client.Account.Balance(ctx, "USDT")
	if err != nil {
		log.Fatal(err)
	}
	if usdt, ok := balance.Detail("USDT"); ok {
		fmt.Println("USDT 可用:", usdt.AvailBal.Float64())
	}
}
```

完整示例见 [examples/rest](examples/rest/main.go)、[examples/ws](examples/ws/main.go) 与 [examples/backtest](examples/backtest/main.go)。

能力范围、已验证程度与已知风险见 [docs/scope.md](docs/scope.md)。

## 配置

所有配置都通过 `Option` 传入 `NewClient`：

| Option | 说明 |
| --- | --- |
| `WithCredentials(key, secret, passphrase)` | API 凭证；只调公共接口可省略 |
| `WithSimulated(true)` | 模拟盘：REST 自动带 `x-simulated-trading: 1`，WS 自动切 `wspap.okx.com`。**注意它同时会截断公共历史行情**，见下 |
| `WithRESTURL(u)` | 自定义 REST 地址，如 `okx.AWSRESTURL` |
| `WithWSURLs(pub, priv, biz)` | 自定义 WebSocket 地址 |
| `WithWSPort443(true)` | WebSocket 改走 443 端口（见下方「WebSocket 连不上」） |
| `WithProxy(u)` | HTTP/SOCKS5 代理，REST 与 WS 都会使用 |
| `WithTimeout(d)` | 单次 HTTP 超时，默认 15s |
| `WithRetry(times, delay)` | 重试次数与间隔，默认 3 次 / 1s |
| `WithHTTPClient(hc)` | 直接复用项目已有的 `*http.Client` |
| `WithBrokerTag(tag)` | 下单默认 tag（经纪商返佣标识） |
| `WithLogger(l)` | 注入日志实现，默认静默 |
| `WithRateLimit(rps, burst)` | 启用内置令牌桶限流（默认不限流） |
| `WithLimiter(l)` | 注入自己的限流器，接口与 `golang.org/x/time/rate.Limiter` 兼容 |
| `WithWSReconnectDelay(d)` / `WithWSPingInterval(d)` | WS 重连间隔与心跳间隔 |

## 数值类型

OKX 的 JSON 里所有数值都是字符串，且可能为空串。SDK 用 `Num`（底层就是 `string`）承载，
既不丢精度，也能方便转换：

```go
pos.Upl.Float64()  // 空串或非法值都返回 0
pos.Upl.Float64E() // 需要区分「空值」与「格式错误」时用这个
pos.Upl.String()   // 原始字符串，不丢精度
pos.UTime.Time()   // 毫秒时间戳 -> time.Time
okx.NumOf(1.5)     // 由 float64 构造，用于下单参数
```

## 已覆盖的 REST 接口

**行情 `client.Market`**：`Tickers` / `Ticker` / `Candles` / `HistoryCandles` / `MarkPriceCandles` / `HistoryMarkPriceCandles` / `IndexCandles` / `HistoryIndexCandles` / `IndexTicker` / `IndexTickers` / `Books` / `Trades` / `HistoryTrades` / `CandleHistory` / `EachCandlePage`

**公共数据 `client.Public`**：`Instruments` / `Instrument` / `MarkPrices` / `FundingRate` / `FundingRateHistory` / `FundingRateHistoryAll` / `PositionTiers` / `OpenInterests` / `PriceLimit` / `ServerTime`

**账户 `client.Account`**：`Balance` / `Positions` / `PositionsHistory` / `Config` / `LeverageInfo` / `SetLeverage` / `MaxSize` / `MaxAvailSize` / `SetPositionMode` / `Bills` / `TradeFee` / `AdjustPositionMargin`

**交易 `client.Trade`**：`PlaceOrder` / `PlaceOrders` / `CancelOrder` / `CancelOrders` / `AmendOrder` / `ClosePosition` / `Order` / `PendingOrders` / `OrdersHistory` / `OrdersHistoryArchive` / `Fills` / `FillsHistory` / `PlaceAlgoOrder` / `CancelAlgoOrders` / `PendingAlgoOrders` / `AlgoOrdersHistory` / `CancelAllAfter`

没封装的接口可以直接用泛型的 `Do`：

```go
resp, err := okx.Do[map[string]any](ctx, client, http.MethodGet,
	"/api/v5/asset/balances", url.Values{"ccy": {"USDT"}}, nil, true)
```

## K 线分页语义

OKX 的分页参数与直觉相反，SDK 保持了原样，请注意：

- `After`：返回**早于**该毫秒时间戳的数据（更旧）
- `Before`：返回**晚于**该毫秒时间戳的数据（更新）

`Candles` 返回近期数据（含未收线的当前一根），`HistoryCandles` 用于拉取更久远的历史。
两者都按时间**倒序**返回，第 0 条最新。

## WebSocket

三类连接对应 OKX 的三个接入点，按频道所属选择：

```go
pub  := client.NewPublicWS()   // tickers / books / mark-price / trades
biz  := client.NewBusinessWS() // candle1m 等 K 线、策略委托
priv := client.NewPrivateWS()  // account / positions / orders（自动登录）
```

订阅可以在 `Connect` **之前**登记，连接就绪后自动发送；**断线重连后也会自动重放**，
调用方不需要关心重连细节：

```go
ws := client.NewBusinessWS()
defer ws.Close()

ws.OnConnect(func() { log.Println("已连接，可在此用 REST 拉一次快照对齐状态") })
ws.OnError(func(err error) { log.Println("ws 错误:", err) })

if err := ws.SubscribeCandles("ETH-USDT-SWAP", "1m", func(c okx.Candle) {
	if c.Confirm { // Confirm 为 false 表示这根 K 线还在走
		log.Printf("收线 %s C=%.2f", c.Time().Format("15:04"), c.Close)
	}
}); err != nil {
	log.Fatal(err)
}

if err := ws.Connect(ctx); err != nil { // ctx 取消即停止重连
	log.Fatal(err)
}
```

### WebSocket 连不上（REST 却正常）

官方文档给的 WS 地址是 **8443** 端口。部分企业网、校园网和运营商只放行 443，
会让 8443 的 TLS 握手直接超时，表现为 `okx: dial public ws: context deadline exceeded`，
而 REST（443）一切正常。OKX 在 443 上提供同样的服务，打开开关即可：

```go
client, _ := okx.NewClient(okx.WithWSPort443(true))
```

排查时可以先确认是不是端口问题：

```bash
curl -sv --http1.1 -o /dev/null --max-time 6 https://ws.okx.com:8443/ws/v5/public
```

内置的类型化订阅：`SubscribeTickers` / `SubscribeTrades` / `SubscribeMarkPrice` /
`SubscribeBooks` / `SubscribeCandles` / `SubscribeAccount` / `SubscribePositions` /
`SubscribeOrders` / `SubscribeAlgoOrders`。

其他频道用泛型的 `SubscribeTyped`：

```go
okx.SubscribeTyped(ws, okx.Arg{Channel: "liquidation-orders", InstType: "SWAP"},
	func(arg okx.Arg, items []MyPayload) { /* ... */ })
```

Handler 在读循环里被**同步**调用，耗时逻辑请自行投递到别的 goroutine，避免阻塞后续消息。

### WebSocket 下单

私有连接支持直接下单，省去每次 REST 调用的请求构造与签名开销：

```go
ws := client.NewPrivateWS()
defer ws.Close()
ws.OnConnect(func() { log.Println("已登录，可以下单") })
if err := ws.Connect(ctx); err != nil {
	log.Fatal(err)
}

res, err := ws.PlaceOrder(ctx, okx.OrderRequest{
	InstID: "ETH-USDT-SWAP", TdMode: okx.TdModeIsolated,
	Side: okx.SideBuy, PosSide: okx.PosSideLong,
	OrdType: okx.OrdTypeLimit, Sz: "1", Px: okx.NumOf(2400),
})
```

参数、返回和错误语义与 `client.Trade.PlaceOrder` 完全一致，另有
`PlaceOrders` / `CancelOrder` / `CancelOrders` / `AmendOrder` / `AmendOrders`。
并发调用是安全的，SDK 按请求 id 关联应答，不会串号。

**关于延迟：** WS 下单省掉的是 REST 每次请求的签名与报文构造，不是网络往返。
在跨境链路上往返时延占绝对大头——实测同一环境下 WS 与 REST 下单往返分别是
354ms 和 362ms，**几乎没有差别**。真正的收益出现在低延迟链路（同区域机房）
或高频场景下，不要指望它能抵消地理距离。

**instIdCode：** OKX 的 WS 下单要求带产品数字编码。SDK 会自动解析并缓存，
但首次用到某个产品时会触发一次 REST 查询。对延迟敏感的策略请在启动时预热：

```go
if err := client.PreloadInstrumentCodes(ctx, "SWAP"); err != nil {
	log.Fatal(err)
}
```

## 错误处理

```go
res, err := client.Trade.PlaceOrder(ctx, req)
switch {
case err == nil:
	fmt.Println("ordId:", res.OrdID)
case okx.IsCode(err, "51008"): // 余额不足
	// ...
default:
	if apiErr, ok := okx.AsAPIError(err); ok {
		// 业务错误：批量接口下 SCode / SMsg 是单条失败的真正原因
		log.Printf("okx 拒绝: code=%s sCode=%s sMsg=%s", apiErr.Code, apiErr.SCode, apiErr.SMsg)
	}
	if httpErr, ok := okx.AsHTTPError(err); ok {
		log.Printf("HTTP %d", httpErr.StatusCode)
	}
}
```

- OKX 的鉴权、限频失败会用**非 2xx 状态码**返回，但 body 仍是标准信封
  （例如只读 Key 下单会得到 `401` + `code=50120`）。这类错误同样是 `*APIError`，
  可以直接用 `IsCode` 分支；`HTTPStatus` 字段非零即表示它来自非 2xx 响应，
  底层的 `*HTTPError` 仍可通过 `AsHTTPError` 从错误链里取到
- 网络错误、5xx、429 会按 `WithRetry` 配置重试；4xx 与业务错误码不重试
- 批量接口部分成功时（顶层 `code = "2"`）会同时返回结果切片和 `*APIError`，
  逐条检查 `OrderResult.OK()` 即可分辨哪几笔成功
- `ErrNoData`：接口成功但 `data` 为空；`ErrNoCredentials`：调私有接口但没配 Key

## 逐仓 + 永续 + 双向持仓的字段语义

这是最常见的合约策略配置。下面几条是在真实账户上实测出来的，光看文档容易踩：

**账户余额要读 `Details`，不要读顶层。** 在单币种保证金模式（`Account.Config` 的
`acctLv=2`，绝大多数人的默认）下，`Balance` 顶层的 `AvailEq` / `MgnRatio` / `Upl` /
`OrdFroz` / `NotionalUsd` 实测**全部返回空串**，只有 `TotalEq`、`IsoEq`、`UTime` 有值。
真实数据在 `Balance.Details` 里逐币种给出：

```go
d, _ := balance.Detail("USDT")
d.AvailBal.Float64()  // 可动用余额，开新仓看它
d.FrozenBal.Float64() // 逐仓仓位占用的保证金计入这里
d.IsoEq.Float64()     // 逐仓仓位权益；Eq ≈ CashBal + IsoEq
d.IsoUpl.Float64()    // 逐仓未实现盈亏
// d.MgnRatio / d.MaxLoan / d.Liab 在该模式下恒为空，别依赖
```

**持仓的保证金看 `Margin`，不是 `Imr`。** 逐仓下 `Position.Imr` 恒为空（那是全仓字段），
仓位实际占用的保证金在 `Margin`。`PosCcy` 在 USDT 本位合约下也恒为空。

**两套未实现盈亏，回测要选对。** `Upl` / `UplRatio` 按**标记价** `MarkPx` 计算，
OKX 的强平判定用的就是它；`UplLastPx` / `UplRatioLastPx` 按**最新成交价** `Last` 计算。
回测通常以成交价撮合，用后者口径才对得上（实测两者会差 0.03% 左右）。

**下单量的单位是「张」。** 用 `Public.Instrument` 换算：

```go
inst, _ := client.Public.Instrument(ctx, "SWAP", "ETH-USDT-SWAP")
// inst.CtVal=0.1 ETH/张，inst.LotSz=0.01 张，inst.MinSz=0.01 张
coins := contracts * inst.CtVal.Float64() * inst.CtMult.Float64()
```

**双向持仓下 `PosSide` 必填。** 开多 `side=buy, posSide=long`，平多
`side=sell, posSide=long`（或用 `Trade.ClosePosition`）。历史持仓
`PositionHistory` 同时给 `Direction` 和 `PosSide`，两者都可用来区分多空。

**止损建议触发在标记价上。** OKX 默认按最新价触发，容易被插针扫掉；永续的强平既然按
标记价判定，止损也设成 `mark` 更一致：

```go
req := okx.OrderRequest{
	InstID:  "ETH-USDT-SWAP",
	TdMode:  okx.TdModeIsolated,
	Side:    okx.SideBuy,
	PosSide: okx.PosSideLong,
	OrdType: okx.OrdTypeLimit,
	Sz:      "1",
	Px:      okx.NumOf(2400),

	SlTriggerPx:     okx.NumOf(2000),
	SlTriggerPxType: "mark", // last（默认）/ index / mark
	SlOrdPx:         "-1",   // -1 表示市价止损
}
```

**逐仓的资金费用不走账户余额。** 账单里资金费（`Bill.Type == "8"`）的 `BalChg` 是 0，
实际扣减记在 `PosBalChg`（仓位保证金变动）上。只累加 `BalChg` 会把资金费全部漏掉——
实盘对账要同时看这两个字段。

### 结构体收录范围

字段是按「逐仓 / 永续 / 双向持仓」这个场景，用实盘与模拟盘的真实报文逐个核对后选定的，
不是照抄文档。刻意未收录的有：期权希腊字母（`deltaBS`、`gammaPA` 等）、币币杠杆与借币
（`liab`、`interest`、`maxLoan` 等）、现货与跟单理财（`spotUpl`、`stgyEq`、`twap` 等）、
交割合约结算（`settledPnl`、`nonSettleAvgPx`）、组合保证金与对冲（`hedgedPos`、`delta`），
这些在该场景下实测恒为空。

需要它们时不用改 SDK，用泛型 `Do` 拿原始 JSON 即可：

```go
resp, _ := okx.Do[map[string]json.RawMessage](ctx, client, http.MethodGet,
	"/api/v5/account/positions", url.Values{"instType": {"SWAP"}}, nil, true)
```

## 回测场景

回测的成败取决于成本和强平模型是否真实。下面几个是实测踩出来的要点。

### 手续费：别读错字段

OKX 按保证金币种把费率放在不同字段里。实测按 `instFamily` 查 USDT 永续时，
`maker` / `taker` 返回的是**空串**，真实费率在 `makerU` / `takerU`。直接读前者
会得到零手续费，回测 PnL 会系统性偏乐观。用 `Rates` 按结算币种取：

```go
fee, _ := client.Account.TradeFee(ctx, "SWAP", "", "ETH-USDT", "")
maker, taker := fee.Rates("USDT") // -0.0002 / -0.0005（负数表示支出）
```

### 强平：维持保证金率是分档跳变的

`MMR` 不是常数，随仓位大小分档。实测 ETH-USDT-SWAP 共 99 档，
第 1 档 `mmr=0.004`、最大杠杆 100，第 22 档 `mmr=0.1025`、最大杠杆只剩 8.69——
**相差 25 倍**。用固定 MMR 估算强平价，仓位一大就会严重偏离：

```go
tiers, _ := client.Public.PositionTiers(ctx, "SWAP", okx.TdModeIsolated, "ETH-USDT", "", "", "")
if tier, ok := okx.TierFor(tiers, 6000); ok { // 6000 张
	fmt.Println(tier.MMR, tier.MaxLever) // 0.005 66.66
}
```

### 强平判定用的是标记价，不是成交价

回测建模爆仓要用标记价序列，否则插针行情下会得出偏乐观的结果：

```go
marks, _ := client.Market.CandleHistory(ctx, okx.HistoryRequest{
	InstID: "ETH-USDT-SWAP",
	Bar:    "1m",
	Source: okx.CandleSourceMark, // 标记价
	Begin:  begin,
})
```

标记价与指数 K 线**没有成交量**（交易所只返回 6 个字段），`Vol` 恒为 0。

**模拟盘的历史行情被截断，不要用它做回测。** 实测模拟盘（`WithSimulated(true)`）
的 K 线一律只回溯到 **2022-05-27**，而实盘可回溯到合约上线：

| 合约 | 数据源 | 实盘 | 模拟盘 |
| --- | --- | --- | --- |
| ETH-USDT-SWAP | 成交价 | 2467 根，2019-11-30 起 | 1558 根，2022-05-27 起 |
| ETH-USDT-SWAP | 标记价 | 2435 根，2020-01-01 起 | 1558 根，2022-05-27 起 |
| BTC-USDT-SWAP | 成交价 | 2469 根，2019-11-28 起 | 1558 根，2022-05-27 起 |

（表中日期同为港时，参见上文关于时区的说明。）

这条容易踩，因为 `x-simulated-trading` 直觉上只该影响交易，不该影响**公共**行情。
两条序列还在同一点齐断，所以在模拟盘上对比「标记价和成交价谁更深」会得到
「一样深」的假象——那其实是「一样取不到」。**拉历史数据请用不带 `WithSimulated`
的客户端**，交易再单独用模拟盘客户端。

**标记价历史比成交价浅。** 实测 OKX 的标记价与指数 K 线一律**不早于 2020-01-01
（港时）**，
而成交价 K 线可以回溯到合约上线当天。2020 年之前上线的合约因此存在开头缺口：

| 合约 | 上线 | 成交价最早 | 标记价最早 | 缺口 |
| --- | --- | --- | --- | --- |
| BTC-USD-SWAP | 2018-08-28 | 2018-12-18 | 2020-01-01 | 379 天 |
| BTC-USDT-SWAP | 2019-11-12 | 2019-11-28 | 2020-01-01 | 34 天 |
| ETH-USDT-SWAP | 2019-11-12 | 2019-11-30 | 2020-01-01 | 32 天 |
| SOL-USDT-SWAP | 2021-01-22 | 2021-01-22 | 2021-01-22 | 同深 |

> 表中日期均为**港时（UTC+8）**。OKX 的日线按港时对齐——1D K 线开盘于 UTC 16:00，
> 所以同一根 K 线在 UTC 口径下会显示为前一天。标记价边界的原始时间戳是
> `1577808000000`（= 2019-12-31 16:00 UTC = 2020-01-01 00:00 HKT），拿它比较最不容易错。
> 注意 `Candle.Time()` 返回的是**本地时区**时间，在非 UTC+8 的机器上格式化会得到不同日期。

`CandleHistory` 会如实返回交易所给了多少就是多少，**不会为缺口报错**。
如果你的回测依赖标记价建模强平，把起点定在 2020-01-01 之前会拿到一段没有强平
判定依据的数据——请自行比对两条序列的首根时间，或直接把回测起点设在 2020-01-01
之后。样本 5 个合约，这条界线是观察结论而非官方承诺，建议在你关心的标的上自己核一遍。

### 拉长周期历史数据

单页上限 300 根，拉一年 1 分钟线需要约 1750 次请求。`CandleHistory` 会自动翻页、
去重、按时间**正序**返回，并在页间留出间隔避免限频：

```go
candles, err := client.Market.CandleHistory(ctx, okx.HistoryRequest{
	InstID: "ETH-USDT-SWAP",
	Bar:    "1m",
	Begin:  time.Now().AddDate(0, 0, -30).UnixMilli(),
	End:    time.Now().UnixMilli(), // 不含
})
```

一年的 1 分钟线有 50 多万根、上百兆内存。不想全部驻留时用流式版本，
回调拿到的每一页都已是正序，返回 `false` 即可提前停止：

```go
req := okx.HistoryRequest{InstID: "ETH-USDT-SWAP", Bar: "1m", Begin: begin}

err := client.Market.EachCandlePage(ctx, req, func(page []okx.Candle) bool {
	for _, k := range page { // 每一页都已是正序
		engine.OnBar(k)
	}
	return true
})
```

默认会剔除尚未收线的 K 线（`Confirm` 为 false），否则序列最后一根是残缺的；
需要保留时设 `IncludeUnclosed: true`。`MaxPages` 可以在参数写错时兜底。

### 资金费用必须计入

永续每 8 小时结算一次资金费，持仓过夜的策略不计入会高估收益。
实测 ETH-USDT-SWAP 近 30 天累计资金费率 **0.4937%**——对多头是实打实的成本：

```go
rates, _ := client.Public.FundingRateHistoryAll(ctx, "ETH-USDT-SWAP",
	time.Now().AddDate(0, 0, -30).UnixMilli(), 0, 0)
```

**但资金费率历史只有约 3 个月，且会静默截断。** 实测 OKX 的
`funding-rate-history` 是一个约 92 天的滚动窗口，更早的区间直接返回空数组——
实盘与模拟盘表现一致，这一条不受 `WithSimulated` 影响：

| 合约 | 条数 | 覆盖区间 | 跨度 |
| --- | --- | --- | --- |
| BTC-USDT-SWAP | 277 | 2026-06-01 ~ 2026-09-01 | 92 天 |
| BTC-USD-SWAP | 277 | 2026-06-01 ~ 2026-09-01 | 92 天 |
| ETH-USDT-SWAP | 277 | 2026-06-01 ~ 2026-09-01 | 92 天 |

`FundingRateHistoryAll` 传再早的 `begin` 也只会拿到窗口内的数据，**不会报错**。
回测超过 3 个月却直接累加返回值，会把资金费成本严重低估——一年期回测只能拿到
约四分之一的资金费。超窗口的部分只能自己另行估算（例如用窗口内的均值外推，
并在结论里注明这部分是估计值）。

同时注意逐仓下资金费**不走账户余额**，账单里 `BalChg` 是 0、实际扣在 `PosBalChg` 上
（详见上一节）。

### 其他可用输入

`Public.OpenInterests`（持仓量）、`Public.PriceLimit`（限价范围，可模拟挂单被拒）、
`Market.HistoryTrades`（逐笔成交，tick 级回测）、`Market.IndexTicker`。

完整示例见 [examples/backtest](examples/backtest/main.go)。

## 实盘防护

### 断线自动撤单

程序崩溃、断网或卡死时，挂单会失去看管。`CancelAllAfter` 让交易所在倒计时结束后
**撤销该账户的全部挂单**——策略需要持续续期，一旦停止续期就自动兜底：

```go
go func() {
	t := time.NewTicker(20 * time.Second) // 续期节奏取倒计时的 1/3
	defer t.Stop()
	for {
		if _, err := client.Trade.CancelAllAfter(ctx, 60); err != nil {
			log.Println("续期失败:", err)
		}
		select {
		case <-ctx.Done():
			client.Trade.CancelAllAfter(context.Background(), 0) // 正常退出时解除
			return
		case <-t.C:
		}
	}
}()
```

这是**账户级**开关，会影响该账户的所有挂单，不区分产品。传 `0` 取消倒计时。

### 逐仓补减保证金

逐仓的风险完全由仓位自带的保证金承担。行情不利时补保证金可以把强平价推远，
是逐仓策略的常规风控手段（全仓不需要也不支持）：

```go
_, err := client.Account.AdjustPositionMargin(ctx, okx.PositionMarginRequest{
	InstID:  "ETH-USDT-SWAP",
	PosSide: okx.PosSideLong,
	Type:    okx.MarginAdd, // 或 okx.MarginReduce
	Amt:     okx.NumOf(100),
})
```

减保证金受维持保证金要求限制，交易所会直接拒绝（`59301`）——实测中即使只减
1% 也可能被拒，**务必检查返回的错误，不要假定成功**。

## 注意事项

- **下单前先在模拟盘验证**（`WithSimulated(true)`）。合约的 `Sz` 单位是**张**，不是币的数量，
  用 `Public.Instrument` 拿 `CtVal`（合约面值）和 `LotSz`（数量精度）换算。
- 双向持仓模式下合约下单必须传 `PosSide`；单向持仓模式下不能传。可用 `Account.Config` 确认当前模式。
- OKX 各接口有独立的频率限制，高频调用建议通过 `WithLimiter` 注入限流器。
- 签名依赖本地时钟，偏差过大会被拒绝（错误码 50102）。可用 `Public.ServerTime` 校准。

## 许可证

[MIT](LICENSE)
