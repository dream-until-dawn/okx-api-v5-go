package okx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSEndpoint 标识 OKX 的三类 WebSocket 接入点。
type WSEndpoint int

const (
	// EndpointPublic 公共频道：行情、深度、标记价格等，无需登录。
	EndpointPublic WSEndpoint = iota
	// EndpointBusiness 业务频道：K 线、策略委托等，无需登录。
	EndpointBusiness
	// EndpointPrivate 私有频道：账户、持仓、订单，需要登录。
	EndpointPrivate
)

func (e WSEndpoint) String() string {
	switch e {
	case EndpointPublic:
		return "public"
	case EndpointBusiness:
		return "business"
	case EndpointPrivate:
		return "private"
	}
	return "unknown"
}

// Arg 是 WebSocket 的频道订阅参数。Channel 必填，其余按频道要求选填。
type Arg struct {
	Channel    string `json:"channel"`
	InstType   string `json:"instType,omitempty"`
	InstFamily string `json:"instFamily,omitempty"`
	InstID     string `json:"instId,omitempty"`
	Ccy        string `json:"ccy,omitempty"`
	Uly        string `json:"uly,omitempty"`
	AlgoID     string `json:"algoId,omitempty"`
}

// routeKey 是消息路由键。OKX 推送回来的 arg 未必与订阅时完全一致
// （例如 account 频道订阅时带 ccy，推送时只有 channel），
// 因此只用 channel + instId 做键，并在查不到时回退到 channel。
func (a Arg) routeKey() string { return a.Channel + "|" + a.InstID }

// WSMessage 是一条频道推送消息。
type WSMessage struct {
	Arg Arg `json:"arg"`
	// Action 仅深度频道有值：snapshot（全量）/ update（增量）。
	Action string `json:"action"`
	// Data 是未解析的业务数据数组，可用 [Unmarshal] 或 encoding/json 自行解析。
	Data json.RawMessage `json:"data"`
	// Raw 是完整的原始报文，便于调试。
	Raw []byte `json:"-"`
}

// Unmarshal 把消息的 data 数组解析为 []T。
func Unmarshal[T any](msg *WSMessage) ([]T, error) {
	var out []T
	if len(msg.Data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(msg.Data, &out); err != nil {
		return nil, fmt.Errorf("okx: decode %s payload: %w", msg.Arg.Channel, err)
	}
	return out, nil
}

// Handler 处理一条频道推送。它在读循环内被同步调用，
// 因此耗时逻辑应自行投递到别的 goroutine，避免阻塞后续消息。
type Handler func(msg *WSMessage)

// WSEvent 是非数据类的事件报文（订阅确认、登录结果、错误等）。
type WSEvent struct {
	Event     string `json:"event"` // subscribe / unsubscribe / login / error / channel-conn-count
	Code      string `json:"code"`
	Msg       string `json:"msg"`
	ConnID    string `json:"connId"`
	ConnCount string `json:"connCount"`
	Arg       Arg    `json:"arg"`
}

type wsOp struct {
	Op   string `json:"op"`
	Args []any  `json:"args"`
}

type wsLoginArg struct {
	APIKey     string `json:"apiKey"`
	Passphrase string `json:"passphrase"`
	Timestamp  string `json:"timestamp"`
	Sign       string `json:"sign"`
}

type subscription struct {
	arg     Arg
	handler Handler
}

// WS 是一条 WebSocket 长连接，负责连接、登录、订阅、心跳与断线重连。
//
// 通过 [Client.NewPublicWS] / [Client.NewBusinessWS] / [Client.NewPrivateWS] 创建。
// 它是并发安全的：可以在任意 goroutine 中调用 Subscribe / Unsubscribe / Close。
type WS struct {
	c        *Client
	endpoint WSEndpoint
	url      string

	mu       sync.Mutex
	conn     *websocket.Conn
	subs     map[string]*subscription // routeKey -> 订阅
	closed   bool
	loggedIn bool

	writeMu sync.Mutex

	loginCh chan error // 等待登录结果

	// pending 关联已发出的 WS 交易请求与其应答（见 ws_trade.go）。
	pending *pendingCalls

	onEvent      func(WSEvent)
	onError      func(error)
	onConnect    func()
	onDisconnect func(error)
	fallback     Handler

	done   chan struct{}
	closeO sync.Once
}

// NewPublicWS 创建公共频道连接（行情、深度、标记价格等）。
func (c *Client) NewPublicWS() *WS { return c.newWS(EndpointPublic, c.opt.wsPublicURL) }

// NewBusinessWS 创建业务频道连接（K 线、策略委托等）。
func (c *Client) NewBusinessWS() *WS { return c.newWS(EndpointBusiness, c.opt.wsBusinessURL) }

// NewPrivateWS 创建私有频道连接（账户、持仓、订单），需要已配置 API 凭证。
func (c *Client) NewPrivateWS() *WS { return c.newWS(EndpointPrivate, c.opt.wsPrivateURL) }

func (c *Client) newWS(ep WSEndpoint, u string) *WS {
	return &WS{
		c:        c,
		endpoint: ep,
		url:      u,
		subs:     make(map[string]*subscription),
		pending:  newPendingCalls(),
		done:     make(chan struct{}),
	}
}

// 以下 On* 回调应在 [WS.Connect] 之前注册，之后不要再修改。
//
// OnEvent 注册事件回调（订阅确认、登录结果、错误码等）。
func (w *WS) OnEvent(fn func(WSEvent)) { w.onEvent = fn }

// OnError 注册错误回调（连接失败、解析失败等）。
func (w *WS) OnError(fn func(error)) { w.onError = fn }

// OnConnect 注册连接就绪回调；私有频道会在登录成功后触发。
// 重连成功后同样会触发，可用于在此刷新一次 REST 快照。
func (w *WS) OnConnect(fn func()) { w.onConnect = fn }

// OnDisconnect 注册断开回调。
func (w *WS) OnDisconnect(fn func(error)) { w.onDisconnect = fn }

// OnUnhandled 注册兜底处理器，接收所有没有匹配到订阅的推送消息。
func (w *WS) OnUnhandled(h Handler) { w.fallback = h }

// URL 返回该连接使用的地址。
func (w *WS) URL() string { return w.url }

// Connect 建立连接。私有频道会自动登录，成功后自动（重）订阅已注册的频道。
//
// 首次连接失败会直接返回错误；连接建立后由内部 goroutine 负责心跳与断线重连，
// 直到 ctx 取消或调用 [WS.Close]。
func (w *WS) Connect(ctx context.Context) error {
	if w.endpoint == EndpointPrivate && !w.c.HasCredentials() {
		return ErrNoCredentials
	}
	if err := w.dialAndSetup(ctx); err != nil {
		return err
	}
	go w.supervise(ctx)
	return nil
}

// dialAndSetup 建立一次连接并完成登录与重订阅。
func (w *WS) dialAndSetup(ctx context.Context) error {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}
	if w.c.opt.proxyURL != "" {
		pu, err := url.Parse(w.c.opt.proxyURL)
		if err != nil {
			return fmt.Errorf("okx: invalid proxy url %q: %w", w.c.opt.proxyURL, err)
		}
		dialer.Proxy = http.ProxyURL(pu)
	}

	conn, _, err := dialer.DialContext(ctx, w.url, nil)
	if err != nil {
		return fmt.Errorf("okx: dial %s ws: %w", w.endpoint, err)
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		conn.Close()
		return errors.New("okx: ws already closed")
	}
	w.conn = conn
	w.loggedIn = false
	w.loginCh = make(chan error, 1)
	w.mu.Unlock()

	// 心跳间隔的两倍作为读超时：OKX 要求 30s 内必须有数据往来，
	// 我们每 pingInterval 发一次 ping，两个周期没有任何消息就认定连接已死。
	w.resetReadDeadline()

	if w.endpoint == EndpointPrivate {
		if err := w.login(); err != nil {
			conn.Close()
			return err
		}
	}
	return nil
}

// supervise 负责读循环、心跳与断线重连，直到 ctx 结束或 Close 被调用。
func (w *WS) supervise(ctx context.Context) {
	delay := w.c.opt.wsReconnectDelay
	for {
		connErr := w.runConnection(ctx)

		if w.isClosed() || ctx.Err() != nil {
			return
		}
		if w.onDisconnect != nil {
			w.onDisconnect(connErr)
		}
		w.c.opt.logger.Infof("okx: %s ws disconnected (%v), reconnecting in %s", w.endpoint, connErr, delay)

		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-time.After(delay):
		}

		if err := w.dialAndSetup(ctx); err != nil {
			w.reportError(err)
			// 指数退避，上限 60s，避免交易所故障时打爆重连。
			if delay < 60*time.Second {
				delay *= 2
			}
			continue
		}
		delay = w.c.opt.wsReconnectDelay
	}
}

// runConnection 跑完一条连接的生命周期，返回导致其结束的错误。
func (w *WS) runConnection(ctx context.Context) error {
	conn := w.currentConn()
	if conn == nil {
		return errors.New("okx: no active connection")
	}

	readErr := make(chan error, 1)
	go func() { readErr <- w.readLoop(conn) }()

	// 这条连接结束时，让所有还在等应答的交易请求立刻失败，而不是挂到超时。
	defer w.pending.failAll()

	// 公共 / 业务频道无需登录，连上就可以订阅；私有频道等 login 成功。
	if w.endpoint != EndpointPrivate {
		w.afterReady()
	} else {
		go w.waitLoginThenSubscribe(ctx)
	}

	ticker := time.NewTicker(w.c.opt.wsPingInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-readErr:
			conn.Close()
			return err
		case <-ctx.Done():
			conn.Close()
			<-readErr
			return ctx.Err()
		case <-w.done:
			conn.Close()
			<-readErr
			return errors.New("okx: ws closed")
		case <-ticker.C:
			// OKX 的心跳是纯文本 "ping"，服务端回 "pong"。
			if err := w.writeRaw(websocket.TextMessage, []byte("ping")); err != nil {
				conn.Close()
				<-readErr
				return fmt.Errorf("okx: ws ping: %w", err)
			}
		}
	}
}

func (w *WS) waitLoginThenSubscribe(ctx context.Context) {
	w.mu.Lock()
	ch := w.loginCh
	w.mu.Unlock()

	select {
	case err := <-ch:
		if err != nil {
			w.reportError(err)
			return
		}
		w.afterReady()
	case <-time.After(15 * time.Second):
		w.reportError(errors.New("okx: ws login timeout"))
	case <-ctx.Done():
	case <-w.done:
	}
}

// afterReady 在连接（并登录）就绪后重放全部订阅。
func (w *WS) afterReady() {
	w.mu.Lock()
	args := make([]Arg, 0, len(w.subs))
	for _, s := range w.subs {
		args = append(args, s.arg)
	}
	w.mu.Unlock()

	if len(args) > 0 {
		if err := w.sendOp("subscribe", args); err != nil {
			w.reportError(fmt.Errorf("okx: resubscribe: %w", err))
			return
		}
	}
	if w.onConnect != nil {
		w.onConnect()
	}
}

func (w *WS) readLoop(conn *websocket.Conn) error {
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		w.resetReadDeadline()

		// 心跳回复不是 JSON，需要先挡掉。
		if mt == websocket.TextMessage && string(data) == "pong" {
			continue
		}

		var probe struct {
			ID    string          `json:"id"`
			Op    string          `json:"op"`
			Event string          `json:"event"`
			Arg   Arg             `json:"arg"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			w.reportError(fmt.Errorf("okx: decode ws message %s: %w", truncate(string(data), 256), err))
			continue
		}

		// 交易应答带 id，先按 id 交还给等待中的调用方。
		// 这类报文既没有 arg 也未必有 event，不先挑出来会被当成无人认领的推送。
		if probe.ID != "" {
			var resp wsTradeResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				w.reportError(fmt.Errorf("okx: decode ws trade reply: %w", err))
				continue
			}
			if !w.pending.resolve(resp.ID, &resp) {
				w.c.opt.logger.Debugf("okx: ws reply for unknown id %s: %s", resp.ID, truncate(string(data), 200))
			}
			continue
		}

		if probe.Event != "" {
			w.handleEvent(data, probe.Event)
			continue
		}
		w.dispatch(&WSMessage{Arg: probe.Arg, Data: probe.Data, Raw: data}, data)
	}
}

func (w *WS) handleEvent(raw []byte, event string) {
	var ev WSEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		w.reportError(fmt.Errorf("okx: decode ws event: %w", err))
		return
	}

	switch event {
	case "login":
		w.mu.Lock()
		ch := w.loginCh
		if ev.Code == "0" {
			w.loggedIn = true
		}
		w.mu.Unlock()
		var err error
		if ev.Code != "0" {
			err = &APIError{Code: ev.Code, Msg: ev.Msg, Body: string(raw)}
		}
		select {
		case ch <- err:
		default:
		}
	case "error":
		w.reportError(&APIError{Code: ev.Code, Msg: ev.Msg, Body: string(raw)})
	case "subscribe":
		w.c.opt.logger.Debugf("okx: subscribed %s %s", ev.Arg.Channel, ev.Arg.InstID)
	case "unsubscribe":
		w.c.opt.logger.Debugf("okx: unsubscribed %s %s", ev.Arg.Channel, ev.Arg.InstID)
	}

	if w.onEvent != nil {
		w.onEvent(ev)
	}
}

func (w *WS) dispatch(msg *WSMessage, raw []byte) {
	w.mu.Lock()
	sub, ok := w.subs[msg.Arg.routeKey()]
	if !ok {
		// 订阅时未指定 instId（例如按 instType 订阅 positions），
		// 但推送里带上了具体 instId，此时回退到只按频道匹配。
		sub, ok = w.subs[msg.Arg.Channel+"|"]
	}
	w.mu.Unlock()

	if ok && sub.handler != nil {
		sub.handler(msg)
		return
	}
	if w.fallback != nil {
		w.fallback(msg)
		return
	}
	w.c.opt.logger.Debugf("okx: unhandled ws push: %s", truncate(string(raw), 256))
}

// Subscribe 订阅一个或多个频道，同一个 handler 处理这批频道的推送。
//
// 可以在 [WS.Connect] 之前调用：订阅会被缓存，连接就绪后自动发送；
// 断线重连后也会自动重放，无需调用方处理。
func (w *WS) Subscribe(h Handler, args ...Arg) error {
	if len(args) == 0 {
		return errors.New("okx: no channel to subscribe")
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return errors.New("okx: ws already closed")
	}
	for _, a := range args {
		w.subs[a.routeKey()] = &subscription{arg: a, handler: h}
	}
	ready := w.conn != nil && (w.endpoint != EndpointPrivate || w.loggedIn)
	w.mu.Unlock()

	if !ready {
		// 尚未连接（或尚未登录完成）时先缓存，afterReady 会统一发出去。
		return nil
	}
	return w.sendOp("subscribe", args)
}

// Unsubscribe 取消订阅。
func (w *WS) Unsubscribe(args ...Arg) error {
	w.mu.Lock()
	for _, a := range args {
		delete(w.subs, a.routeKey())
	}
	connected := w.conn != nil
	w.mu.Unlock()

	if !connected {
		return nil
	}
	return w.sendOp("unsubscribe", args)
}

func (w *WS) sendOp(op string, args []Arg) error {
	payload := make([]any, 0, len(args))
	for _, a := range args {
		payload = append(payload, a)
	}
	return w.writeJSON(wsOp{Op: op, Args: payload})
}

func (w *WS) login() error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	arg := wsLoginArg{
		APIKey:     w.c.opt.apiKey,
		Passphrase: w.c.opt.passphrase,
		Timestamp:  ts,
		Sign:       w.c.wsLoginSign(ts),
	}
	return w.writeJSON(wsOp{Op: "login", Args: []any{arg}})
}

func (w *WS) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.writeRaw(websocket.TextMessage, data)
}

func (w *WS) writeRaw(mt int, data []byte) error {
	conn := w.currentConn()
	if conn == nil {
		return errors.New("okx: ws not connected")
	}
	// gorilla/websocket 不允许并发写，这里串行化所有写操作。
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(mt, data)
}

func (w *WS) currentConn() *websocket.Conn {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn
}

func (w *WS) resetReadDeadline() {
	if conn := w.currentConn(); conn != nil {
		_ = conn.SetReadDeadline(time.Now().Add(2*w.c.opt.wsPingInterval + 5*time.Second))
	}
}

func (w *WS) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *WS) reportError(err error) {
	if err == nil {
		return
	}
	w.c.opt.logger.Errorf("okx: %s ws: %v", w.endpoint, err)
	if w.onError != nil {
		w.onError(err)
	}
}

// Close 关闭连接并停止重连。多次调用是安全的。
func (w *WS) Close() error {
	var err error
	w.closeO.Do(func() {
		w.mu.Lock()
		w.closed = true
		conn := w.conn
		w.mu.Unlock()

		close(w.done)
		if conn != nil {
			err = conn.Close()
		}
		w.pending.failAll()
	})
	return err
}
