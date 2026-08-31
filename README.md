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

完整示例见 [examples/rest](examples/rest/main.go) 与 [examples/ws](examples/ws/main.go)。

## 配置

所有配置都通过 `Option` 传入 `NewClient`：

| Option | 说明 |
| --- | --- |
| `WithCredentials(key, secret, passphrase)` | API 凭证；只调公共接口可省略 |
| `WithSimulated(true)` | 模拟盘：REST 自动带 `x-simulated-trading: 1`，WS 自动切 `wspap.okx.com` |
| `WithRESTURL(u)` | 自定义 REST 地址，如 `okx.AWSRESTURL` |
| `WithWSURLs(pub, priv, biz)` | 自定义 WebSocket 地址 |
| `WithWSPort443(true)` | WebSocket 改走 443 端口（见下方「WebSocket 连不上」） |
| `WithProxy(u)` | HTTP/SOCKS5 代理，REST 与 WS 都会使用 |
| `WithTimeout(d)` | 单次 HTTP 超时，默认 15s |
| `WithRetry(times, delay)` | 重试次数与间隔，默认 3 次 / 1s |
| `WithHTTPClient(hc)` | 直接复用项目已有的 `*http.Client` |
| `WithBrokerTag(tag)` | 下单默认 tag（经纪商返佣标识） |
| `WithLogger(l)` | 注入日志实现，默认静默 |
| `WithLimiter(l)` | 注入限流器，接口与 `golang.org/x/time/rate.Limiter` 兼容 |
| `WithWSReconnectDelay(d)` / `WithWSPingInterval(d)` | WS 重连间隔与心跳间隔 |

## 数值类型

OKX 的 JSON 里所有数值都是字符串，且可能为空串。SDK 用 `Num`（底层就是 `string`）承载，
既不丢精度，也能方便转换：

```go
p.Upl.Float64()   // 空串或非法值返回 0
p.Upl.Float64E()  // 需要区分错误时用这个
p.Ts.Time()       // 毫秒时间戳 -> time.Time
p.Upl.String()    // 原始字符串
okx.NumOf(1.5)    // 构造，用于下单参数
```

## 已覆盖的 REST 接口

**行情 `client.Market`**：`Tickers` / `Ticker` / `Candles` / `HistoryCandles` / `Books` / `Trades`

**公共数据 `client.Public`**：`Instruments` / `Instrument` / `MarkPrices` / `FundingRate` / `FundingRateHistory` / `ServerTime`

**账户 `client.Account`**：`Balance` / `Positions` / `PositionsHistory` / `Config` / `LeverageInfo` / `SetLeverage` / `MaxSize` / `MaxAvailSize` / `SetPositionMode` / `Bills`

**交易 `client.Trade`**：`PlaceOrder` / `PlaceOrders` / `CancelOrder` / `CancelOrders` / `AmendOrder` / `ClosePosition` / `Order` / `PendingOrders` / `OrdersHistory` / `OrdersHistoryArchive` / `Fills` / `FillsHistory` / `PlaceAlgoOrder` / `CancelAlgoOrders` / `PendingAlgoOrders` / `AlgoOrdersHistory`

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

## 注意事项

- **下单前先在模拟盘验证**（`WithSimulated(true)`）。合约的 `Sz` 单位是**张**，不是币的数量，
  用 `Public.Instrument` 拿 `CtVal`（合约面值）和 `LotSz`（数量精度）换算。
- 双向持仓模式下合约下单必须传 `PosSide`；单向持仓模式下不能传。可用 `Account.Config` 确认当前模式。
- OKX 各接口有独立的频率限制，高频调用建议通过 `WithLimiter` 注入限流器。
- 签名依赖本地时钟，偏差过大会被拒绝（错误码 50102）。可用 `Public.ServerTime` 校准。

## 许可证

[MIT](LICENSE)
