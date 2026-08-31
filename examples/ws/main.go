// WebSocket 示例：同时订阅公共行情、业务 K 线与私有账户 / 持仓 / 订单频道。
//
// 运行：
//
//	go run ./examples/ws
//
// 可选环境变量：OKX_API_KEY / OKX_SECRET_KEY / OKX_PASSPHRASE / OKX_PROXY，
// 设置 OKX_SIMULATED=1 走模拟盘。未配置 API Key 时只跑公共频道。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	okx "github.com/dream-until-dawn/okx-api-v5-go"
)

const instID = "ETH-USDT-SWAP"

func main() {
	opts := []okx.Option{okx.WithLogger(stdLogger{})}
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- 公共频道：行情 ----
	pub := client.NewPublicWS()
	defer pub.Close()

	// 订阅可以在 Connect 之前登记，连接就绪（以及每次重连）后会自动重放。
	if err := pub.SubscribeTickers(instID, func(t okx.Ticker) {
		fmt.Printf("[ticker] %s 最新价 %s 买一 %s 卖一 %s\n", t.InstID, t.Last, t.BidPx, t.AskPx)
	}); err != nil {
		log.Fatal(err)
	}
	if err := pub.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	// ---- 业务频道：K 线 ----
	biz := client.NewBusinessWS()
	defer biz.Close()

	if err := biz.SubscribeCandles(instID, "1m", func(c okx.Candle) {
		status := "进行中"
		if c.Confirm {
			status = "已收线"
		}
		fmt.Printf("[candle1m] %s C=%.2f 量=%.2f %s\n", c.Time().Format("15:04"), c.Close, c.Vol, status)
	}); err != nil {
		log.Fatal(err)
	}
	if err := biz.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	// ---- 私有频道：账户 / 持仓 / 订单 ----
	if client.HasCredentials() {
		priv := client.NewPrivateWS()
		defer priv.Close()

		priv.OnConnect(func() { fmt.Println("[private] 已登录，开始接收推送") })
		priv.OnError(func(err error) { fmt.Println("[private] 错误:", err) })

		if err := priv.SubscribeAccount("USDT", func(b okx.Balance) {
			if d, ok := b.Detail("USDT"); ok {
				fmt.Printf("[account] USDT 权益 %s 可用 %s\n", d.Eq, d.AvailBal)
			}
		}); err != nil {
			log.Fatal(err)
		}
		if err := priv.SubscribePositions("SWAP", "", func(ps []okx.Position) {
			for _, p := range ps {
				fmt.Printf("[position] %s %s 张数=%s 盈亏=%s\n", p.InstID, p.PosSide, p.Pos, p.Upl)
			}
		}); err != nil {
			log.Fatal(err)
		}
		if err := priv.SubscribeOrders("SWAP", "", func(o okx.Order) {
			fmt.Printf("[order] %s %s %s 状态=%s 成交均价=%s\n", o.InstID, o.Side, o.Sz, o.State, o.AvgPx)
		}); err != nil {
			log.Fatal(err)
		}
		if err := priv.Connect(ctx); err != nil {
			log.Fatal(err)
		}
	} else {
		fmt.Println("未配置 API Key，跳过私有频道")
	}

	fmt.Println("运行中，Ctrl+C 退出…")
	<-ctx.Done()
	fmt.Println("正在退出")
}

// stdLogger 把 SDK 日志接到标准库 log 上。
type stdLogger struct{}

func (stdLogger) Debugf(format string, args ...any) { log.Printf("DEBUG "+format, args...) }
func (stdLogger) Infof(format string, args ...any)  { log.Printf("INFO  "+format, args...) }
func (stdLogger) Errorf(format string, args ...any) { log.Printf("ERROR "+format, args...) }
