package okx

import (
	"encoding/json"
	"testing"
)

// 本文件用真实接口返回的报文片段锁住字段解析，样本取自逐仓 + 永续 + 双向持仓的
// 单币种保证金账户（数值已改写，结构与字段名保持原样）。

func TestPositionIsolatedSwapFields(t *testing.T) {
	// 逐仓 SWAP 多头的真实返回形态：imr / posCcy 为空，保证金在 margin，
	// 且同时给出按标记价和按最新价两套未实现盈亏。
	const raw = `{
		"instType":"SWAP","instId":"ETH-USDT-SWAP","posId":"3733742730691092480",
		"posSide":"long","mgnMode":"isolated","pos":"49.18","availPos":"49.18",
		"posCcy":"","ccy":"USDT","avgPx":"1805.5767334669338683","bePx":"1786.8588123660445",
		"markPx":"2419.52","last":"2419.52","idxPx":"2420.774","liqPx":"1466.3929432660957",
		"lever":"5","margin":"1700.6075811251431723","mgnRatio":"88.14749114412055",
		"imr":"","mmr":"47.59679744","upl":"3019.3729848096195","uplRatio":"1.7001306428949658",
		"uplLastPx":"3018.5","uplRatioLastPx":"1.6996","realizedPnl":"96.44862179378151",
		"pnl":"182.04988519038076","fee":"-10.066836395","fundingFee":"-75.53442700159925",
		"liqPenalty":"0","notionalUsd":"11896.5815361408","adl":"1","usdPx":"0.99978",
		"closeOrderAlgo":[],"cTime":"1783910703686","uTime":"1788134400560","tradeId":"3156960179"
	}`
	var p Position
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}

	if p.PosSide != PosSideLong || p.MgnMode != TdModeIsolated {
		t.Fatalf("posSide=%q mgnMode=%q", p.PosSide, p.MgnMode)
	}
	// 逐仓下 imr 恒为空，保证金要看 margin —— 用 imr 算仓位占用会得到 0。
	if !p.Imr.IsEmpty() {
		t.Errorf("逐仓的 imr 应为空，实际 %q", p.Imr)
	}
	if p.Margin.Float64() != 1700.6075811251431723 {
		t.Errorf("margin = %v", p.Margin.Float64())
	}
	// USDT 本位合约的 posCcy 恒为空，仓位单位是「张」，换算靠 Instrument.CtVal。
	if p.PosCcy != "" {
		t.Errorf("USDT 本位合约的 posCcy 应为空，实际 %q", p.PosCcy)
	}
	// 两套盈亏口径必须都解析出来且确实不同：upl 按标记价，uplLastPx 按最新成交价。
	if p.Upl.Float64() != 3019.3729848096195 || p.UplLastPx.Float64() != 3018.5 {
		t.Errorf("upl=%v uplLastPx=%v", p.Upl.Float64(), p.UplLastPx.Float64())
	}
	if p.UplRatioLastPx.Float64() != 1.6996 {
		t.Errorf("uplRatioLastPx = %v", p.UplRatioLastPx.Float64())
	}
	if p.UsdPx.Float64() != 0.99978 {
		t.Errorf("usdPx = %v", p.UsdPx.Float64())
	}
	if p.AvailPos.Float64() != 49.18 || p.Lever.Float64() != 5 {
		t.Errorf("availPos=%v lever=%v", p.AvailPos.Float64(), p.Lever.Float64())
	}
	if len(p.CloseOrderAlgo) != 0 {
		t.Errorf("closeOrderAlgo = %+v", p.CloseOrderAlgo)
	}
}

func TestPositionCloseOrderAlgo(t *testing.T) {
	// 仓位上挂了止盈止损时 closeOrderAlgo 才有内容。
	const raw = `{"instId":"ETH-USDT-SWAP","closeOrderAlgo":[
		{"algoId":"123","slTriggerPx":"2000","slTriggerPxType":"mark",
		 "tpTriggerPx":"3000","tpTriggerPxType":"last","closeFraction":"1"}]}`
	var p Position
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.CloseOrderAlgo) != 1 {
		t.Fatalf("closeOrderAlgo = %+v", p.CloseOrderAlgo)
	}
	a := p.CloseOrderAlgo[0]
	if a.AlgoID != "123" || a.SlTriggerPxType != "mark" || a.TpTriggerPx.Float64() != 3000 {
		t.Fatalf("unexpected algo: %+v", a)
	}
}

func TestBalanceSingleCcyMarginMode(t *testing.T) {
	// 单币种保证金模式（acctLv=2）下顶层聚合字段几乎全空，真实数据在 details 里。
	const raw = `{
		"totalEq":"95926.4782843792","isoEq":"4718.942170210257","uTime":"1788147715094",
		"adjEq":"","availEq":"","imr":"","mmr":"","mgnRatio":"","notionalUsd":"",
		"notionalUsdForSwap":"","ordFroz":"","upl":"",
		"details":[{"ccy":"USDT","eq":"4720.3467606665545","cashBal":"0.3661947317916033",
			"availBal":"0.3661947317916033","availEq":"0.3661947317916033",
			"frozenBal":"4719.980565934763","ordFrozen":"0","isoEq":"4719.980565934763",
			"isoUpl":"3019.3729848096195","upl":"3019.3729848096195",
			"eqUsd":"4719.308284379208","disEq":"4718.942170210257",
			"mgnRatio":"","maxLoan":"","liab":"","interest":"","imr":"0","mmr":"0",
			"notionalLever":"0","uTime":"1787172647016"}]
	}`
	var b Balance
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	if !b.AvailEq.IsEmpty() || !b.Upl.IsEmpty() || !b.MgnRatio.IsEmpty() {
		t.Error("该账户模式下顶层聚合字段应为空，策略不应依赖它们")
	}
	d, ok := b.Detail("USDT")
	if !ok {
		t.Fatal("缺少 USDT 明细")
	}
	// 逐仓仓位保证金计入 frozenBal，可动用的只有 availBal。
	if d.AvailBal.Float64() != 0.3661947317916033 {
		t.Errorf("availBal = %v", d.AvailBal.Float64())
	}
	if d.FrozenBal.Float64() != 4719.980565934763 || d.IsoEq.Float64() != 4719.980565934763 {
		t.Errorf("frozenBal=%v isoEq=%v", d.FrozenBal.Float64(), d.IsoEq.Float64())
	}
	if d.IsoUpl.Float64() != 3019.3729848096195 {
		t.Errorf("isoUpl = %v", d.IsoUpl.Float64())
	}
	// Eq ≈ CashBal + IsoEq
	if diff := d.Eq.Float64() - (d.CashBal.Float64() + d.IsoEq.Float64()); diff > 1e-6 || diff < -1e-6 {
		t.Errorf("eq(%v) 与 cashBal+isoEq(%v) 不一致", d.Eq.Float64(), d.CashBal.Float64()+d.IsoEq.Float64())
	}
	if !d.MgnRatio.IsEmpty() || !d.MaxLoan.IsEmpty() {
		t.Error("该模式下 mgnRatio / maxLoan 应为空")
	}
}

func TestPositionHistoryHasPosSide(t *testing.T) {
	// 双向持仓下必须能区分多空；OKX 同时给 direction 和 posSide。
	const raw = `[{"instId":"ETH-USDT-SWAP","direction":"short","posSide":"short",
		"mgnMode":"isolated","openAvgPx":"2500","closeAvgPx":"2400","pnl":"32.22729",
		"pnlRatio":"0.05","fee":"-1.2","fundingFee":"-0.5","lever":"5","type":"2"}]`
	var hs []PositionHistory
	if err := json.Unmarshal([]byte(raw), &hs); err != nil {
		t.Fatal(err)
	}
	if hs[0].PosSide != PosSideShort || hs[0].Direction != "short" {
		t.Fatalf("posSide=%q direction=%q", hs[0].PosSide, hs[0].Direction)
	}
}

func TestOrderExtraFields(t *testing.T) {
	const raw = `{"instId":"ETH-USDT-SWAP","ordId":"1","state":"canceled","side":"sell",
		"posSide":"long","tdMode":"isolated","sz":"0.01","px":"3143.96","avgPx":"",
		"accFillSz":"0","fee":"0","rebate":"0","rebateCcy":"USDT","tradeId":"",
		"tgtCcy":"","algoId":"","algoClOrdId":"","tpTriggerPxType":"mark",
		"slTriggerPxType":"last","cancelSource":"1","cancelSourceReason":"Order was canceled by you."}`
	var o Order
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	if o.CancelSourceReason != "Order was canceled by you." {
		t.Errorf("cancelSourceReason = %q", o.CancelSourceReason)
	}
	if o.TpTriggerPxType != "mark" || o.SlTriggerPxType != "last" {
		t.Errorf("触发价类型解析错误: tp=%q sl=%q", o.TpTriggerPxType, o.SlTriggerPxType)
	}
	if o.RebateCcy != "USDT" {
		t.Errorf("rebateCcy = %q", o.RebateCcy)
	}
}

func TestOrderRequestTriggerPxTypeOmitted(t *testing.T) {
	// 不设置触发价类型时不能出现在报文里，否则 OKX 会因空串报参数错误。
	b, err := json.Marshal(OrderRequest{InstID: "X", TdMode: "isolated", Side: "buy", OrdType: "market", Sz: "1"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"tpTriggerPxType", "slTriggerPxType", "px", "posSide", "tag"} {
		if _, present := m[k]; present {
			t.Errorf("空字段 %s 不应出现在请求体里: %s", k, b)
		}
	}

	b, _ = json.Marshal(OrderRequest{
		InstID: "X", TdMode: "isolated", Side: "buy", OrdType: "limit", Sz: "1", Px: "100",
		SlTriggerPx: "90", SlTriggerPxType: "mark",
	})
	_ = json.Unmarshal(b, &m)
	if m["slTriggerPxType"] != "mark" || m["slTriggerPx"] != "90" {
		t.Errorf("止损参数未正确序列化: %s", b)
	}
}

func TestBillIsolatedFundingFeeGoesToPosBalChg(t *testing.T) {
	// 逐仓资金费用不动账户余额：balChg 为 0，实际扣在 posBalChg 上。
	// 只累加 balChg 会漏掉全部资金费，这条测试把该语义固定下来。
	const raw = `[{"billId":"1","instType":"SWAP","instId":"ETH-USDT-SWAP","type":"8",
		"subType":"173","ccy":"USDT","balChg":"0.0000000000000000","bal":"0.366",
		"pnl":"-1.0828473350000706","posBalChg":"-1.0828473350000706","posBal":"1700.6",
		"fee":"0","mgnMode":"isolated","execType":"","tradeId":"0","ts":"1788134400560"}]`
	var bs []Bill
	if err := json.Unmarshal([]byte(raw), &bs); err != nil {
		t.Fatal(err)
	}
	b := bs[0]
	if b.Type != "8" {
		t.Fatalf("type = %q", b.Type)
	}
	if b.BalChg.Float64() != 0 {
		t.Errorf("逐仓资金费的 balChg 应为 0，实际 %v", b.BalChg.Float64())
	}
	if b.PosBalChg.Float64() != -1.0828473350000706 {
		t.Errorf("posBalChg = %v", b.PosBalChg.Float64())
	}
	if b.MgnMode != TdModeIsolated {
		t.Errorf("mgnMode = %q", b.MgnMode)
	}
}

func TestFillFields(t *testing.T) {
	const raw = `[{"instType":"SWAP","instId":"ETH-USDT-SWAP","tradeId":"1","ordId":"2",
		"fillPx":"1936.72","fillSz":"2.06","side":"sell","posSide":"long","execType":"T",
		"fee":"-0.19948216","feeCcy":"USDT","fillPnl":"5.5","fillIdxPx":"1937.7139",
		"fillMarkPx":"1936.9","ts":"1788134400560","fillTime":"1788134400560"}]`
	var fs []Fill
	if err := json.Unmarshal([]byte(raw), &fs); err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	if f.ExecType != "T" || f.FillIdxPx.Float64() != 1937.7139 {
		t.Fatalf("execType=%q fillIdxPx=%v", f.ExecType, f.FillIdxPx.Float64())
	}
	if f.PosSide != PosSideLong || f.Side != SideSell {
		t.Fatalf("posSide=%q side=%q", f.PosSide, f.Side)
	}
}

func TestInstrumentSwapSizing(t *testing.T) {
	// 合约下单量的单位是「张」，换算成币要用 ctVal；这几个字段是下单前的必备输入。
	const raw = `{"instType":"SWAP","instId":"ETH-USDT-SWAP","ctType":"linear",
		"ctVal":"0.1","ctValCcy":"ETH","ctMult":"1","settleCcy":"USDT",
		"tickSz":"0.01","lotSz":"0.01","minSz":"0.01","lever":"100","state":"live",
		"maxLmtSz":"100000000","maxMktSz":"20000","maxTriggerSz":"100000000","maxStopSz":"200000"}`
	var in Instrument
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if in.CtVal.Float64() != 0.1 || in.CtValCcy != "ETH" {
		t.Fatalf("ctVal=%v ctValCcy=%q", in.CtVal.Float64(), in.CtValCcy)
	}
	if in.MaxStopSz.Float64() != 200000 || in.MaxTriggerSz.Float64() != 100000000 {
		t.Fatalf("maxStopSz=%v maxTriggerSz=%v", in.MaxStopSz.Float64(), in.MaxTriggerSz.Float64())
	}
	// 1 张 = 0.1 ETH，10 张 = 1 ETH
	if got := 10 * in.CtVal.Float64() * in.CtMult.Float64(); got != 1 {
		t.Fatalf("10 张应折合 1 ETH，实际 %v", got)
	}
}
