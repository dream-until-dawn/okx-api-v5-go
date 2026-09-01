package okx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// WebSocket 交易操作名。
const (
	wsOpOrder       = "order"
	wsOpBatchOrders = "batch-orders"
	wsOpCancelOrder = "cancel-order"
	wsOpBatchCancel = "batch-cancel-orders"
	wsOpAmendOrder  = "amend-order"
	wsOpBatchAmend  = "batch-amend-orders"
)

// ErrWSNotTrading 表示在不支持交易的连接上调用了下单类方法。
// WebSocket 交易只能走私有连接（[Client.NewPrivateWS]）。
var ErrWSNotTrading = errors.New("okx: ws trading is only available on the private connection")

// wsTradeRequest 是 WS 交易请求的报文结构。
type wsTradeRequest struct {
	ID   string `json:"id"`
	Op   string `json:"op"`
	Args []any  `json:"args"`
}

// wsTradeResponse 是 WS 交易应答。
//
// 顶层 code 的语义与 REST 一致："0" 全部成功、"1" 全部失败、"2" 部分成功；
// 逐条的真正原因在 data 里的 sCode / sMsg 上。
type wsTradeResponse struct {
	ID      string          `json:"id"`
	Op      string          `json:"op"`
	Code    string          `json:"code"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
	InTime  string          `json:"inTime"`
	OutTime string          `json:"outTime"`
}

// pendingCalls 管理「已发出、等待应答」的 WS 交易请求。
type pendingCalls struct {
	mu   sync.Mutex
	m    map[string]chan *wsTradeResponse
	next atomic.Int64
}

func newPendingCalls() *pendingCalls {
	return &pendingCalls{m: make(map[string]chan *wsTradeResponse)}
}

// register 分配一个请求 ID 并登记等待通道。
func (p *pendingCalls) register() (string, chan *wsTradeResponse) {
	// OKX 要求 id 是字母数字且不超过 32 位；这里用自增序号加纳秒后缀，
	// 保证同一条连接内不重复，重连后也不会和旧请求撞上。
	id := strconv.FormatInt(p.next.Add(1), 36) + strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	ch := make(chan *wsTradeResponse, 1)

	p.mu.Lock()
	p.m[id] = ch
	p.mu.Unlock()
	return id, ch
}

func (p *pendingCalls) resolve(id string, resp *wsTradeResponse) bool {
	p.mu.Lock()
	ch, ok := p.m[id]
	if ok {
		delete(p.m, id)
	}
	p.mu.Unlock()
	if !ok {
		return false
	}
	ch <- resp
	return true
}

func (p *pendingCalls) forget(id string) {
	p.mu.Lock()
	delete(p.m, id)
	p.mu.Unlock()
}

// failAll 在连接断开时唤醒所有等待中的请求，避免它们一直挂到超时。
func (p *pendingCalls) failAll() {
	p.mu.Lock()
	pending := p.m
	p.m = make(map[string]chan *wsTradeResponse)
	p.mu.Unlock()

	for _, ch := range pending {
		ch <- nil // nil 表示连接已断，调用方据此报错
	}
}

// call 发出一次 WS 交易请求并等待应答。
func (w *WS) call(ctx context.Context, op string, args []any) (*wsTradeResponse, error) {
	if w.endpoint != EndpointPrivate {
		return nil, ErrWSNotTrading
	}
	w.mu.Lock()
	ready := w.conn != nil && w.loggedIn
	w.mu.Unlock()
	if !ready {
		return nil, errors.New("okx: ws not connected or not logged in yet")
	}

	id, ch := w.pending.register()
	if err := w.writeJSON(wsTradeRequest{ID: id, Op: op, Args: args}); err != nil {
		w.pending.forget(id)
		return nil, fmt.Errorf("okx: send ws %s: %w", op, err)
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("okx: ws %s: connection closed before the reply arrived", op)
		}
		return resp, nil
	case <-ctx.Done():
		w.pending.forget(id)
		return nil, ctx.Err()
	}
}

// decodeTradeResult 把应答解析成 []T，并按 code / sCode 生成错误。
func decodeTradeResult[T any](resp *wsTradeResponse, op string) ([]T, error) {
	var out []T
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &out); err != nil {
			return nil, fmt.Errorf("okx: decode ws %s reply: %w", op, err)
		}
	}
	if resp.Code == "0" {
		return out, nil
	}

	apiErr := &APIError{Code: resp.Code, Msg: resp.Msg, Body: string(resp.Data)}
	var details []itemStatus
	if json.Unmarshal(resp.Data, &details) == nil {
		for _, it := range details {
			if it.SCode != "" && it.SCode != "0" {
				apiErr.SCode, apiErr.SMsg = it.SCode, it.SMsg
				break
			}
		}
	}
	// 部分成功时结果仍然返回，调用方逐条检查 OK() 即可。
	return out, apiErr
}

// PlaceOrder 通过 WebSocket 下单。
//
// 相比 REST 少了一次 TCP 与 TLS 握手，延迟更低，适合对时延敏感的策略。
// 只能在私有连接上调用，且必须已登录（[WS.Connect] 返回后 [WS.OnConnect]
// 触发即表示就绪）。
//
// 参数与返回和 [TradeService.PlaceOrder] 完全一致，错误语义也一致：
// 交易所拒绝时返回 *APIError，其中 SCode / SMsg 是这一笔的真正原因。
func (w *WS) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	w.c.applyOrderDefaults(&req)
	if err := w.fillInstCode(ctx, req.InstID, &req.InstIDCode); err != nil {
		return OrderResult{}, err
	}
	resp, err := w.call(ctx, wsOpOrder, []any{req})
	if err != nil {
		return OrderResult{}, err
	}
	results, err := decodeTradeResult[OrderResult](resp, wsOpOrder)
	return first(results), err
}

// PlaceOrders 通过 WebSocket 批量下单，单次最多 20 笔。
func (w *WS) PlaceOrders(ctx context.Context, reqs []OrderRequest) ([]OrderResult, error) {
	if err := w.ensureTrading(); err != nil {
		return nil, err
	}
	args := make([]any, 0, len(reqs))
	for i := range reqs {
		w.c.applyOrderDefaults(&reqs[i])
		if err := w.fillInstCode(ctx, reqs[i].InstID, &reqs[i].InstIDCode); err != nil {
			return nil, err
		}
		args = append(args, reqs[i])
	}
	resp, err := w.call(ctx, wsOpBatchOrders, args)
	if err != nil {
		return nil, err
	}
	return decodeTradeResult[OrderResult](resp, wsOpBatchOrders)
}

// CancelOrder 通过 WebSocket 撤单。
func (w *WS) CancelOrder(ctx context.Context, req CancelOrderRequest) (OrderResult, error) {
	if err := w.fillInstCode(ctx, req.InstID, &req.InstIDCode); err != nil {
		return OrderResult{}, err
	}
	resp, err := w.call(ctx, wsOpCancelOrder, []any{req})
	if err != nil {
		return OrderResult{}, err
	}
	results, err := decodeTradeResult[OrderResult](resp, wsOpCancelOrder)
	return first(results), err
}

// CancelOrders 通过 WebSocket 批量撤单，单次最多 20 笔。
func (w *WS) CancelOrders(ctx context.Context, reqs []CancelOrderRequest) ([]OrderResult, error) {
	if err := w.ensureTrading(); err != nil {
		return nil, err
	}
	args := make([]any, 0, len(reqs))
	for i := range reqs {
		if err := w.fillInstCode(ctx, reqs[i].InstID, &reqs[i].InstIDCode); err != nil {
			return nil, err
		}
		args = append(args, reqs[i])
	}
	resp, err := w.call(ctx, wsOpBatchCancel, args)
	if err != nil {
		return nil, err
	}
	return decodeTradeResult[OrderResult](resp, wsOpBatchCancel)
}

// AmendOrder 通过 WebSocket 改单。
func (w *WS) AmendOrder(ctx context.Context, req AmendOrderRequest) (OrderResult, error) {
	if err := w.fillInstCode(ctx, req.InstID, &req.InstIDCode); err != nil {
		return OrderResult{}, err
	}
	resp, err := w.call(ctx, wsOpAmendOrder, []any{req})
	if err != nil {
		return OrderResult{}, err
	}
	results, err := decodeTradeResult[OrderResult](resp, wsOpAmendOrder)
	return first(results), err
}

// AmendOrders 通过 WebSocket 批量改单，单次最多 20 笔。
func (w *WS) AmendOrders(ctx context.Context, reqs []AmendOrderRequest) ([]OrderResult, error) {
	if err := w.ensureTrading(); err != nil {
		return nil, err
	}
	args := make([]any, 0, len(reqs))
	for i := range reqs {
		if err := w.fillInstCode(ctx, reqs[i].InstID, &reqs[i].InstIDCode); err != nil {
			return nil, err
		}
		args = append(args, reqs[i])
	}
	resp, err := w.call(ctx, wsOpBatchAmend, args)
	if err != nil {
		return nil, err
	}
	return decodeTradeResult[OrderResult](resp, wsOpBatchAmend)
}

func first[T any](s []T) T {
	if len(s) > 0 {
		return s[0]
	}
	var zero T
	return zero
}

// fillInstCode 在调用方没提供 instIdCode 时自动补齐。
//
// WebSocket 交易要求带这个数字编码，而 REST 不需要。首次用到某个产品时会触发
// 一次 REST 查询，之后走进程内缓存；对延迟敏感的场景请在启动时先调用
// [Client.PreloadInstrumentCodes] 把缓存热起来。
func (w *WS) fillInstCode(ctx context.Context, instID string, dst *int64) error {
	// 先确认连接类型。所有交易方法都以本函数开头，把检查放在这里可以保证在
	// 公共 / 业务连接上立刻返回 ErrWSNotTrading，而不是先白发一次 REST 查询、
	// 再抛出一个和真正原因无关的错误。
	if w.endpoint != EndpointPrivate {
		return ErrWSNotTrading
	}
	if *dst != 0 || instID == "" {
		return nil
	}
	code, err := w.c.InstrumentCode(ctx, instID)
	if err != nil {
		return err
	}
	*dst = code
	return nil
}

// ensureTrading 确认当前连接支持交易。批量方法在入参为空切片时不会进入
// fillInstCode 的循环，因此单独检查一次。
func (w *WS) ensureTrading() error {
	if w.endpoint != EndpointPrivate {
		return ErrWSNotTrading
	}
	return nil
}
