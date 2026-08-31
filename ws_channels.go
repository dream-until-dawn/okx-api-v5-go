package okx

// 常用频道名。完整列表见 OKX 官方文档。
const (
	ChannelTickers  = "tickers"      // 行情（公共）
	ChannelBooks    = "books"        // 400 档增量深度（公共）
	ChannelBooks5   = "books5"       // 5 档全量深度（公共）
	ChannelBBOTbt   = "bbo-tbt"      // 买一卖一（公共）
	ChannelMarkPx   = "mark-price"   // 标记价格（公共）
	ChannelFunding  = "funding-rate" // 资金费率（公共）
	ChannelTrades   = "trades"       // 公共成交（公共）
	ChannelAccount  = "account"      // 账户（私有）
	ChannelPosition = "positions"    // 持仓（私有）
	ChannelOrders   = "orders"       // 订单（私有）
	ChannelAlgo     = "orders-algo"  // 策略委托（私有）
)

// CandleChannel 由 K 线周期拼出业务频道名，例如 CandleChannel("1D") == "candle1D"。
// 周期取值与 REST 的 bar 一致：1m/3m/5m/15m/30m/1H/2H/4H/6H/12H/1D/1W/1M 等。
func CandleChannel(bar string) string { return "candle" + bar }

// SubscribeTyped 订阅一个频道并把推送的 data 数组解析为 []T。
//
// 这是所有类型化订阅方法的底层实现；SDK 未内置的频道可以直接用它，例如：
//
//	okx.SubscribeTyped(ws, okx.Arg{Channel: "liquidation-orders", InstType: "SWAP"},
//		func(arg okx.Arg, items []MyType) { ... })
func SubscribeTyped[T any](w *WS, arg Arg, fn func(arg Arg, items []T)) error {
	return w.Subscribe(func(msg *WSMessage) {
		items, err := Unmarshal[T](msg)
		if err != nil {
			w.reportError(err)
			return
		}
		if len(items) > 0 {
			fn(msg.Arg, items)
		}
	}, arg)
}

// SubscribeTickers 订阅行情频道（公共连接）。
func (w *WS) SubscribeTickers(instID string, fn func(Ticker)) error {
	return SubscribeTyped(w, Arg{Channel: ChannelTickers, InstID: instID}, func(_ Arg, items []Ticker) {
		for _, t := range items {
			fn(t)
		}
	})
}

// SubscribeTrades 订阅公共成交频道（公共连接）。
func (w *WS) SubscribeTrades(instID string, fn func(Trade)) error {
	return SubscribeTyped(w, Arg{Channel: ChannelTrades, InstID: instID}, func(_ Arg, items []Trade) {
		for _, t := range items {
			fn(t)
		}
	})
}

// SubscribeMarkPrice 订阅标记价格频道（公共连接）。
func (w *WS) SubscribeMarkPrice(instID string, fn func(MarkPrice)) error {
	return SubscribeTyped(w, Arg{Channel: ChannelMarkPx, InstID: instID}, func(_ Arg, items []MarkPrice) {
		for _, m := range items {
			fn(m)
		}
	})
}

// SubscribeBooks 订阅深度频道（公共连接）。channel 传 [ChannelBooks] / [ChannelBooks5] / [ChannelBBOTbt]。
//
// books 频道是增量推送：action 为 "snapshot" 时为全量快照，"update" 时为增量，
// 需要调用方自行维护本地订单簿。books5 每次都是全量 5 档，无需合并。
func (w *WS) SubscribeBooks(instID, channel string, fn func(action string, book OrderBook)) error {
	if channel == "" {
		channel = ChannelBooks5
	}
	return w.Subscribe(func(msg *WSMessage) {
		books, err := Unmarshal[OrderBook](msg)
		if err != nil {
			w.reportError(err)
			return
		}
		for _, b := range books {
			fn(msg.Action, b)
		}
	}, Arg{Channel: channel, InstID: instID})
}

// SubscribeCandles 订阅 K 线频道（业务连接，需用 [Client.NewBusinessWS]）。
// bar 取值与 REST 一致，如 "1m"、"1H"、"1D"。
//
// 未走完的当前 K 线也会推送，通过 Candle.Confirm 判断是否已收线。
func (w *WS) SubscribeCandles(instID, bar string, fn func(Candle)) error {
	return SubscribeTyped(w, Arg{Channel: CandleChannel(bar), InstID: instID}, func(_ Arg, items []rawCandle) {
		for _, r := range items {
			fn(r.toCandle())
		}
	})
}

// SubscribeAccount 订阅账户频道（私有连接）。ccy 可为空表示全部币种。
func (w *WS) SubscribeAccount(ccy string, fn func(Balance)) error {
	return SubscribeTyped(w, Arg{Channel: ChannelAccount, Ccy: ccy}, func(_ Arg, items []Balance) {
		for _, b := range items {
			fn(b)
		}
	})
}

// SubscribePositions 订阅持仓频道（私有连接）。instType 必填，如 "SWAP" 或 "ANY"；
// instID 可为空表示该类型下全部产品。
func (w *WS) SubscribePositions(instType, instID string, fn func([]Position)) error {
	return SubscribeTyped(w, Arg{Channel: ChannelPosition, InstType: instType, InstID: instID},
		func(_ Arg, items []Position) { fn(items) })
}

// SubscribeOrders 订阅订单频道（私有连接）。instType 必填，如 "SWAP" 或 "ANY"。
// 订单的每次状态变化都会推送一条，用 Order.State 判断成交与否。
func (w *WS) SubscribeOrders(instType, instID string, fn func(Order)) error {
	return SubscribeTyped(w, Arg{Channel: ChannelOrders, InstType: instType, InstID: instID},
		func(_ Arg, items []Order) {
			for _, o := range items {
				fn(o)
			}
		})
}

// SubscribeAlgoOrders 订阅策略委托频道（业务连接）。instType 必填。
func (w *WS) SubscribeAlgoOrders(instType, instID string, fn func(AlgoOrder)) error {
	return SubscribeTyped(w, Arg{Channel: ChannelAlgo, InstType: instType, InstID: instID},
		func(_ Arg, items []AlgoOrder) {
			for _, o := range items {
				fn(o)
			}
		})
}
