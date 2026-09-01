package okx

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// CandleSource 指定历史 K 线取哪一路价格。
type CandleSource string

const (
	// CandleSourceTrade 成交价 K 线，带成交量。
	CandleSourceTrade CandleSource = "trade"
	// CandleSourceMark 标记价 K 线。OKX 的强平按标记价判定，建模爆仓要用它，没有成交量。
	CandleSourceMark CandleSource = "mark"
	// CandleSourceIndex 指数 K 线，没有成交量。instID 用现货形式，如 "ETH-USDT"。
	CandleSourceIndex CandleSource = "index"
)

func (src CandleSource) historyPath() string {
	switch src {
	case CandleSourceMark:
		return "/api/v5/market/history-mark-price-candles"
	case CandleSourceIndex:
		return "/api/v5/market/history-index-candles"
	default:
		return "/api/v5/market/history-candles"
	}
}

// 单页条数上限。三个历史 K 线接口实测都接受 300
// （官方文档对 history-candles 写的是 100，实测给 300，吞吐差三倍）。
const maxCandlePageLimit = 300

// HistoryRequest 描述一次跨越任意长时间的历史 K 线拉取。
//
// OKX 单次最多返回 300 根，拉一年 1 分钟线需要约 1750 次请求。
// [MarketService.CandleHistory] 与 [MarketService.EachCandlePage] 会自动翻页、
// 去重、按时间正序输出，并在页与页之间留出间隔以避免触发限频。
type HistoryRequest struct {
	// InstID 必填。指数 K 线用现货形式（"ETH-USDT"），其余用合约 ID（"ETH-USDT-SWAP"）。
	InstID string
	// Bar 是 K 线周期，如 1m/5m/15m/1H/4H/1D，留空为 1m。
	Bar string
	// Begin 是起始时间（毫秒，含）。留 0 表示一直往前翻到交易所没有数据为止。
	Begin int64
	// End 是结束时间（毫秒，不含）。留 0 表示从最新开始。
	End int64
	// Source 指定价格来源，留空为成交价。
	Source CandleSource
	// PageLimit 是每页条数，留 0 用 300（上限）。
	PageLimit int
	// PageDelay 是每页之间的等待时间，留 0 用 120ms。
	// 历史 K 线接口的限频是 20 次 / 2 秒，默认值留了充足余量。
	PageDelay time.Duration
	// MaxPages 限制最多翻多少页，0 表示不限制。用于防止参数写错时无限翻页。
	MaxPages int
	// IncludeUnclosed 为 true 时保留尚未收线的 K 线（Confirm 为 false）。
	// 回测默认应当排除它们，否则最后一根是残缺的。
	IncludeUnclosed bool
}

func (r HistoryRequest) normalized() (HistoryRequest, error) {
	if r.InstID == "" {
		return r, fmt.Errorf("okx: HistoryRequest.InstID 不能为空")
	}
	if r.Bar == "" {
		r.Bar = "1m"
	}
	if r.PageLimit <= 0 || r.PageLimit > maxCandlePageLimit {
		r.PageLimit = maxCandlePageLimit
	}
	if r.PageDelay <= 0 {
		r.PageDelay = 120 * time.Millisecond
	}
	if r.Begin > 0 && r.End > 0 && r.Begin >= r.End {
		return r, fmt.Errorf("okx: HistoryRequest.Begin(%d) 必须早于 End(%d)", r.Begin, r.End)
	}
	return r, nil
}

// EachCandlePage 按页拉取历史 K 线并回调，页内已按时间**正序**排列。
//
// 回调返回 false 可提前停止（不算错误）。适合数据量大到不想全部驻留内存的场景——
// 一年的 1 分钟线有 50 多万根。
//
// 翻页方向是从新到旧：OKX 的 after 参数表示"返回早于该时间戳的数据"，
// 每页取完后用该页最旧的一根作为下一页的游标。
func (s *MarketService) EachCandlePage(ctx context.Context, req HistoryRequest, fn func(page []Candle) bool) error {
	req, err := req.normalized()
	if err != nil {
		return err
	}
	path := req.Source.historyPath()

	cursor := req.End // 0 表示从最新开始
	pages := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		q := newParams().
			set("instId", req.InstID).
			set("bar", req.Bar).
			setInt64("after", cursor).
			setInt("limit", req.PageLimit)

		raws, err := request[rawCandle](ctx, s.c, http.MethodGet, path, q.values(), nil, false)
		if err != nil {
			return fmt.Errorf("okx: 拉取 %s 第 %d 页: %w", req.InstID, pages+1, err)
		}
		if len(raws) == 0 {
			return nil // 交易所已无更早的数据
		}
		pages++

		// OKX 返回按时间倒序，最后一根最旧。
		page := make([]Candle, 0, len(raws))
		var oldest int64
		reachedBegin := false
		for _, r := range raws {
			c := r.toCandle()
			if oldest == 0 || c.Ts < oldest {
				oldest = c.Ts
			}
			if req.Begin > 0 && c.Ts < req.Begin {
				reachedBegin = true
				continue
			}
			if req.End > 0 && c.Ts >= req.End {
				continue
			}
			if !req.IncludeUnclosed && !c.Confirm {
				continue
			}
			page = append(page, c)
		}

		// 回调拿到的是正序，符合回测按时间推进的习惯。
		sort.Slice(page, func(i, j int) bool { return page[i].Ts < page[j].Ts })
		if len(page) > 0 && !fn(page) {
			return nil
		}

		if reachedBegin {
			return nil
		}
		if oldest == 0 || (cursor != 0 && oldest >= cursor) {
			// 游标没有前进，再翻下去就是死循环。
			return nil
		}
		cursor = oldest

		if req.MaxPages > 0 && pages >= req.MaxPages {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(req.PageDelay):
		}
	}
}

// CandleHistory 拉取一个时间区间内的全部历史 K 线，按时间**正序**返回，并已按
// 时间戳去重（相邻页在边界上可能重叠）。
//
// 数据量心里要有数：一年的 1 分钟线约 52.6 万根、上百兆内存。只想流式处理时
// 用 [MarketService.EachCandlePage]。
//
//	candles, err := client.Market.CandleHistory(ctx, okx.HistoryRequest{
//		InstID: "ETH-USDT-SWAP",
//		Bar:    "1H",
//		Begin:  time.Now().AddDate(0, -1, 0).UnixMilli(),
//	})
func (s *MarketService) CandleHistory(ctx context.Context, req HistoryRequest) ([]Candle, error) {
	var out []Candle
	seen := make(map[int64]struct{})

	err := s.EachCandlePage(ctx, req, func(page []Candle) bool {
		for _, c := range page {
			if _, dup := seen[c.Ts]; dup {
				continue
			}
			seen[c.Ts] = struct{}{}
			out = append(out, c)
		}
		return true
	})
	if err != nil {
		return out, err
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Ts < out[j].Ts })
	return out, nil
}

// FundingRateHistoryAll 拉取一个时间区间内的全部历史资金费率，按时间正序返回。
//
// 永续合约的资金费每 8 小时结算一次，回测持仓过夜的策略必须计入，否则收益会被高估。
// 单页上限 100，本方法自动翻页。
func (s *PublicService) FundingRateHistoryAll(ctx context.Context, instID string, begin, end int64, pageDelay time.Duration) ([]FundingRateHistoryItem, error) {
	if instID == "" {
		return nil, fmt.Errorf("okx: instID 不能为空")
	}
	if pageDelay <= 0 {
		pageDelay = 120 * time.Millisecond
	}

	var out []FundingRateHistoryItem
	seen := make(map[string]struct{})
	cursor := end

	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}

		items, err := s.FundingRateHistory(ctx, instID, cursor, 0, 100)
		if err != nil {
			return out, err
		}
		if len(items) == 0 {
			break
		}

		var oldest int64
		reachedBegin := false
		for _, it := range items {
			ts := it.FundingTime.Int64()
			if oldest == 0 || ts < oldest {
				oldest = ts
			}
			if begin > 0 && ts < begin {
				reachedBegin = true
				continue
			}
			if _, dup := seen[it.FundingTime.String()]; dup {
				continue
			}
			seen[it.FundingTime.String()] = struct{}{}
			out = append(out, it)
		}

		if reachedBegin || oldest == 0 || (cursor != 0 && oldest >= cursor) {
			break
		}
		cursor = oldest

		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(pageDelay):
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].FundingTime.Int64() < out[j].FundingTime.Int64()
	})
	return out, nil
}
