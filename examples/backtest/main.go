// 回测数据准备示例：把一个回测引擎需要的全部输入拉齐——
// 成交价与标记价 K 线、手续费率、仓位档位、资金费率，并演示如何用它们
// 估算强平价与持仓成本。
//
// 运行：
//
//	go run ./examples/backtest
//
// 手续费率需要 API Key（只读权限即可）；其余都是公开接口。
// 可选环境变量：OKX_API_KEY / OKX_SECRET_KEY / OKX_PASSPHRASE / OKX_PROXY，
// 网络只放行 443 时设 OKX_WS_443=1（本例不用 WebSocket，仅作示范）。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	okx "github.com/dream-until-dawn/okx-api-v5-go"
)

const (
	instID     = "ETH-USDT-SWAP"
	instFamily = "ETH-USDT"
	settleCcy  = "USDT"
	days       = 7
)

func main() {
	opts := []okx.Option{okx.WithTimeout(20 * time.Second)}
	if key := os.Getenv("OKX_API_KEY"); key != "" {
		opts = append(opts, okx.WithCredentials(key, os.Getenv("OKX_SECRET_KEY"), os.Getenv("OKX_PASSPHRASE")))
	}
	if proxy := os.Getenv("OKX_PROXY"); proxy != "" {
		opts = append(opts, okx.WithProxy(proxy))
	}

	client, err := okx.NewClient(opts...)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	begin := time.Now().AddDate(0, 0, -days).Truncate(time.Minute).UnixMilli()
	end := time.Now().Truncate(time.Minute).UnixMilli()

	// ---- 1. 合约规格：张与币的换算、下单精度 ----
	inst, err := client.Public.Instrument(ctx, "SWAP", instID)
	if err != nil {
		log.Fatalf("获取合约规格失败: %v", err)
	}
	ctVal := inst.CtVal.Float64() * inst.CtMult.Float64()
	fmt.Printf("合约规格: 1 张 = %g %s，价格精度 %s，数量精度 %s，最小 %s 张\n",
		ctVal, inst.CtValCcy, inst.TickSz, inst.LotSz, inst.MinSz)

	// ---- 2. 成交价 K 线 ----
	fmt.Printf("\n拉取 %d 天 1m 成交价 K 线…\n", days)
	t0 := time.Now()
	bars, err := client.Market.CandleHistory(ctx, okx.HistoryRequest{
		InstID: instID, Bar: "1m", Begin: begin, End: end,
	})
	if err != nil {
		log.Fatalf("拉取 K 线失败: %v", err)
	}
	fmt.Printf("  %d 根，耗时 %s，区间 %s ~ %s\n", len(bars), time.Since(t0).Round(time.Second),
		bars[0].Time().Format("01-02 15:04"), bars[len(bars)-1].Time().Format("01-02 15:04"))

	// ---- 3. 标记价 K 线：强平判定用它，不是成交价 ----
	marks, err := client.Market.CandleHistory(ctx, okx.HistoryRequest{
		InstID: instID, Bar: "1m", Begin: begin, End: end,
		Source: okx.CandleSourceMark,
	})
	if err != nil {
		log.Fatalf("拉取标记价 K 线失败: %v", err)
	}
	// 两条序列按时间对齐后，看看标记价与成交价的最大偏离——插针行情下正是这个差值
	// 决定了会不会被强平。
	markByTs := make(map[int64]okx.Candle, len(marks))
	for _, m := range marks {
		markByTs[m.Ts] = m
	}
	var maxDev float64
	var maxDevAt int64
	for _, b := range bars {
		m, ok := markByTs[b.Ts]
		if !ok || b.Low == 0 {
			continue
		}
		if dev := abs(m.Low-b.Low) / b.Low; dev > maxDev {
			maxDev, maxDevAt = dev, b.Ts
		}
	}
	fmt.Printf("  标记价 %d 根；与成交价最低价的最大偏离 %.4f%%（%s）\n",
		len(marks), maxDev*100, time.UnixMilli(maxDevAt).Format("01-02 15:04"))

	// ---- 4. 手续费率 ----
	var maker, taker okx.Num
	if client.HasCredentials() {
		fee, err := client.Account.TradeFee(ctx, "SWAP", "", instFamily, "")
		if err != nil {
			log.Printf("获取费率失败（继续用默认值）: %v", err)
		} else {
			maker, taker = fee.Rates(settleCcy)
			fmt.Printf("\n手续费率（%s 等级）: maker %s  taker %s   ← 负数表示支出\n",
				fee.Level, maker, taker)
			fmt.Printf("  注意裸字段 maker=%q taker=%q，USDT 本位要走 Rates(\"USDT\")\n",
				fee.Maker, fee.Taker)
		}
	} else {
		fmt.Println("\n未配置 API Key，跳过手续费率（回测请务必用真实费率）")
	}

	// ---- 5. 仓位档位：维持保证金率随仓位跳变 ----
	tiers, err := client.Public.PositionTiers(ctx, "SWAP", okx.TdModeIsolated, instFamily, "", "", "")
	if err != nil {
		log.Fatalf("获取仓位档位失败: %v", err)
	}
	fmt.Printf("\n仓位档位共 %d 档：\n", len(tiers))
	for _, sz := range []float64{100, 6000, 100000} {
		tier, ok := okx.TierFor(tiers, sz)
		if !ok {
			fmt.Printf("  %8.0f 张 -> 超出最高档\n", sz)
			continue
		}
		fmt.Printf("  %8.0f 张 -> 档位 %-3s mmr=%-8s 最大杠杆 %s\n",
			sz, tier.Tier, tier.MMR, tier.MaxLever)
	}

	// ---- 6. 资金费率：持仓过夜必须计入 ----
	rates, err := client.Public.FundingRateHistoryAll(ctx, instID, begin, 0, 0)
	if err != nil {
		log.Fatalf("获取资金费率失败: %v", err)
	}
	// OKX 的资金费率历史只有约 92 天的滚动窗口，超窗口的部分会被静默丢掉。
	// 回测跨度更长时直接累加会低估成本，这里显式检查覆盖是否完整。
	if len(rates) > 0 {
		covered := time.Since(rates[0].FundingTime.Time()).Hours() / 24
		if covered < float64(days)-1 {
			log.Printf("警告: 只拿到 %.0f 天的资金费率，回测跨度 %d 天——超出部分需自行估算",
				covered, days)
		}
	}
	var cumFunding float64
	for _, r := range rates {
		cumFunding += r.FundingRate.Float64()
	}
	fmt.Printf("\n资金费率 %d 次（每 8h 一次），%d 天累计 %.4f%%\n",
		len(rates), days, cumFunding*100)

	// ---- 7. 把上面这些拼成一次完整的成本估算 ----
	const (
		contracts = 100.0 // 开 100 张
		lever     = 5.0
	)
	entry := bars[0].Close
	exit := bars[len(bars)-1].Close
	notional := contracts * ctVal * entry
	margin := notional / lever

	tier, _ := okx.TierFor(tiers, contracts)
	mmr := tier.MMR.Float64()
	// 逐仓多头强平价的近似：保证金耗尽到只剩维持保证金时的价格。
	liq := entry * (1 - 1/lever + mmr)

	takerRate := taker.Float64()
	if takerRate == 0 {
		takerRate = -0.0005 // 未取到费率时用 Lv1 默认值，仅用于演示
	}
	feeCost := notional * abs(takerRate) * 2 // 开 + 平，都按吃单
	fundingCost := notional * cumFunding     // 多头付出为正
	gross := contracts * ctVal * (exit - entry)

	fmt.Printf("\n—— 持有 %d 天的成本拆解（%g 张，%gx 逐仓多头）——\n", days, contracts, lever)
	fmt.Printf("  名义价值   %12.2f USDT（保证金 %.2f）\n", notional, margin)
	fmt.Printf("  开仓价     %12.2f  ->  平仓价 %.2f\n", entry, exit)
	fmt.Printf("  估算强平价 %12.2f（用档位 %s 的 mmr=%g；固定 mmr 会算错）\n", liq, tier.Tier, mmr)
	fmt.Printf("  毛盈亏     %12.2f USDT\n", gross)
	fmt.Printf("  手续费     %12.2f USDT（开平各一次吃单）\n", -feeCost)
	fmt.Printf("  资金费     %12.2f USDT\n", -fundingCost)
	fmt.Printf("  净盈亏     %12.2f USDT\n", gross-feeCost-fundingCost)
	fmt.Printf("  摩擦成本占毛盈亏 %.1f%%\n", (feeCost+fundingCost)/absNonZero(gross)*100)

	// 标记价是否触及过强平线——这正是必须用标记价序列的原因。
	var touched bool
	var touchedAt int64
	for _, m := range marks {
		if m.Low <= liq {
			touched, touchedAt = true, m.Ts
			break
		}
	}
	if touched {
		fmt.Printf("  !! 标记价在 %s 触及强平价，该仓位实际会被强平\n",
			time.UnixMilli(touchedAt).Format("01-02 15:04"))
	} else {
		fmt.Println("  期间标记价未触及强平价")
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func absNonZero(f float64) float64 {
	if a := abs(f); a > 1e-9 {
		return a
	}
	return 1
}
