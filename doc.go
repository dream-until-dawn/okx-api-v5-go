// Package okx 是 OKX V5 API 的 Go SDK，可直接被其他 Go 项目引入。
//
// 它覆盖常用的 REST 接口（行情 / 公共数据 / 账户 / 交易）以及 WebSocket
// 公共、业务与私有频道，内置签名、重试、模拟盘切换、自动重连与断线重订阅。
//
// 快速开始：
//
//	c, err := okx.NewClient(
//		okx.WithCredentials("apiKey", "secretKey", "passphrase"),
//	)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	balances, err := c.Account.Balance(ctx, "USDT")
//
// 所有数值字段均以 [Num]（底层为 string）返回，既保留交易所原始精度，
// 也可通过 Float64 / Int64 / Time 等方法便捷转换。
package okx
