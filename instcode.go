package okx

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// instCodeCache 缓存 instId 到 instIdCode 的映射。
//
// WebSocket 下单要求带上 instIdCode（产品的数字编码），而 REST 不需要。
// 让调用方自己去查这个码是很差的体验，所以 SDK 在首次用到时自动解析并缓存。
type instCodeCache struct {
	mu sync.RWMutex
	m  map[string]int64
}

func newInstCodeCache() *instCodeCache { return &instCodeCache{m: make(map[string]int64)} }

func (c *instCodeCache) get(instID string) (int64, bool) {
	c.mu.RLock()
	code, ok := c.m[instID]
	c.mu.RUnlock()
	return code, ok
}

func (c *instCodeCache) put(instID string, code int64) {
	if instID == "" || code == 0 {
		return
	}
	c.mu.Lock()
	c.m[instID] = code
	c.mu.Unlock()
}

// InferInstType 从产品 ID 推断产品类型。
//
//	ETH-USDT-SWAP    -> SWAP
//	BTC-USD-240329   -> FUTURES
//	BTC-USD-240329-50000-C -> OPTION
//	ETH-USDT         -> SPOT
//
// 无法判断时返回空串。
func InferInstType(instID string) string {
	parts := strings.Split(instID, "-")
	switch {
	case strings.HasSuffix(instID, "-SWAP"):
		return "SWAP"
	case len(parts) == 5 && (parts[4] == "C" || parts[4] == "P"):
		return "OPTION"
	case len(parts) == 3:
		return "FUTURES"
	case len(parts) == 2:
		return "SPOT"
	}
	return ""
}

// PreloadInstrumentCodes 预取某一产品类型的全部 instIdCode 并缓存。
//
// WebSocket 下单需要 instIdCode，SDK 会在首次下单时自动查询——但那会给第一笔单
// 增加一次 REST 往返，抵消掉 WS 交易的低延迟优势。对延迟敏感的策略应当在启动时
// 先调用本方法把缓存热起来：
//
//	if err := client.PreloadInstrumentCodes(ctx, "SWAP"); err != nil {
//		log.Fatal(err)
//	}
//
// 缓存是进程级的，不会过期。OKX 上新产品后需要重新调用才能拿到新产品的编码。
func (c *Client) PreloadInstrumentCodes(ctx context.Context, instType string) error {
	insts, err := c.Public.Instruments(ctx, instType, "", "", "")
	if err != nil {
		return fmt.Errorf("okx: preload %s instrument codes: %w", instType, err)
	}
	for _, in := range insts {
		c.instCodes.put(in.InstID, in.InstIDCode)
	}
	return nil
}

// InstrumentCode 返回产品的 instIdCode，未命中缓存时会向交易所查询一次。
func (c *Client) InstrumentCode(ctx context.Context, instID string) (int64, error) {
	if code, ok := c.instCodes.get(instID); ok {
		return code, nil
	}

	instType := InferInstType(instID)
	if instType == "" {
		return 0, fmt.Errorf("okx: 无法从 %q 推断产品类型，请先调用 PreloadInstrumentCodes", instID)
	}

	in, err := c.Public.Instrument(ctx, instType, instID)
	if err != nil {
		return 0, fmt.Errorf("okx: 查询 %s 的 instIdCode: %w", instID, err)
	}
	if in.InstIDCode == 0 {
		return 0, fmt.Errorf("okx: %s 没有返回 instIdCode", instID)
	}
	c.instCodes.put(in.InstID, in.InstIDCode)
	return in.InstIDCode, nil
}
