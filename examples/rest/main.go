// REST 接口示例：读取公共行情，并在配置了 API Key 时查询账户与持仓。
//
// 运行：
//
//	go run ./examples/rest
//
// 可选环境变量：OKX_API_KEY / OKX_SECRET_KEY / OKX_PASSPHRASE / OKX_PROXY，
// 设置 OKX_SIMULATED=1 走模拟盘。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	okx "github.com/dream-until-dawn/okx-api-v5-go"
)

func main() {
	opts := []okx.Option{
		okx.WithTimeout(15 * time.Second),
	}
	if key := os.Getenv("OKX_API_KEY"); key != "" {
		opts = append(opts, okx.WithCredentials(key, os.Getenv("OKX_SECRET_KEY"), os.Getenv("OKX_PASSPHRASE")))
	}
	if proxy := os.Getenv("OKX_PROXY"); proxy != "" {
		opts = append(opts, okx.WithProxy(proxy))
	}
	if os.Getenv("OKX_SIMULATED") == "1" {
		opts = append(opts, okx.WithSimulated(true))
	}

	client, err := okx.NewClient(opts...)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const instID = "ETH-USDT-SWAP"

	// 1. 行情快照
	ticker, err := client.Market.Ticker(ctx, instID)
	if err != nil {
		log.Fatalf("获取行情失败: %v", err)
	}
	fmt.Printf("%s 最新价 %.2f，24h 涨跌 %.2f%%\n",
		ticker.InstID,
		ticker.Last.Float64(),
		(ticker.Last.Float64()/ticker.Open24h.Float64()-1)*100)

	// 2. K 线
	candles, err := client.Market.Candles(ctx, okx.CandlesRequest{
		InstID: instID,
		Bar:    "1H",
		Limit:  5,
	})
	if err != nil {
		log.Fatalf("获取 K 线失败: %v", err)
	}
	for _, c := range candles {
		fmt.Printf("  %s O=%.2f H=%.2f L=%.2f C=%.2f 收线=%v\n",
			c.Time().Format("01-02 15:04"), c.Open, c.High, c.Low, c.Close, c.Confirm)
	}

	// 3. 产品信息（下单精度、合约面值）
	inst, err := client.Public.Instrument(ctx, "SWAP", instID)
	if err != nil {
		log.Fatalf("获取产品信息失败: %v", err)
	}
	fmt.Printf("下单价格精度 %s，数量精度 %s，最小下单量 %s，合约面值 %s %s\n",
		inst.TickSz, inst.LotSz, inst.MinSz, inst.CtVal, inst.CtValCcy)

	if !client.HasCredentials() {
		fmt.Println("\n未配置 API Key，跳过私有接口示例")
		return
	}

	// 4. 账户余额
	balance, err := client.Account.Balance(ctx, "USDT")
	if err != nil {
		log.Fatalf("查询余额失败: %v", err)
	}
	if usdt, ok := balance.Detail("USDT"); ok {
		fmt.Printf("\nUSDT 权益 %.4f，可用 %.4f，未实现盈亏 %.4f\n",
			usdt.Eq.Float64(), usdt.AvailBal.Float64(), usdt.Upl.Float64())
	}

	// 5. 当前持仓
	positions, err := client.Account.Positions(ctx, "SWAP", nil, nil)
	if err != nil {
		log.Fatalf("查询持仓失败: %v", err)
	}
	if len(positions) == 0 {
		fmt.Println("当前没有持仓")
	}
	for _, p := range positions {
		fmt.Printf("%s %s 张数=%s 均价=%.2f 未实现盈亏=%.4f (%.2f%%)\n",
			p.InstID, p.PosSide, p.Pos,
			p.AvgPx.Float64(), p.Upl.Float64(), p.UplRatio.Float64()*100)
	}

	// 6. 下单示例（默认注释掉，取消注释前请务必先在模拟盘验证）
	//
	// maxSize, err := client.Account.MaxSize(ctx, []string{instID}, okx.TdModeIsolated, "USDT", "")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// res, err := client.Trade.PlaceOrder(ctx, okx.OrderRequest{
	// 	InstID:  instID,
	// 	TdMode:  okx.TdModeIsolated,
	// 	Side:    okx.SideBuy,
	// 	PosSide: okx.PosSideLong,
	// 	OrdType: okx.OrdTypeMarket,
	// 	Sz:      maxSize[0].MaxBuy,
	// })
	// if err != nil {
	// 	if apiErr, ok := okx.AsAPIError(err); ok {
	// 		log.Fatalf("下单被拒: %s / %s", apiErr.SCode, apiErr.SMsg)
	// 	}
	// 	log.Fatal(err)
	// }
	// fmt.Println("下单成功, ordId =", res.OrdID)
}
