package okx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeOKX 是一个最小的 OKX WebSocket 服务端仿真：处理 ping/pong、login、subscribe，
// 并允许测试主动推送数据或断开连接，用来验证登录、订阅、心跳与重连重订阅。
type fakeOKX struct {
	*httptest.Server

	mu        sync.Mutex
	conn      *websocket.Conn
	logins    []wsLoginArg
	subscribe []Arg
	conns     int

	connected  chan struct{} // 每建立一条连接推送一次
	subscribed chan Arg      // 每收到一次订阅推送一次
}

func newFakeOKX(t *testing.T) *fakeOKX {
	t.Helper()
	f := &fakeOKX{
		connected:  make(chan struct{}, 8),
		subscribed: make(chan Arg, 8),
	}
	up := websocket.Upgrader{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conn = conn
		f.conns++
		f.mu.Unlock()
		select {
		case f.connected <- struct{}{}:
		default:
		}
		f.serve(conn)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeOKX) serve(conn *websocket.Conn) {
	defer conn.Close()
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage && string(data) == "ping" {
			if err := f.write(websocket.TextMessage, []byte("pong")); err != nil {
				return
			}
			continue
		}

		var op struct {
			Op   string            `json:"op"`
			Args []json.RawMessage `json:"args"`
		}
		if json.Unmarshal(data, &op) != nil {
			continue
		}

		switch op.Op {
		case "login":
			var arg wsLoginArg
			if len(op.Args) > 0 {
				_ = json.Unmarshal(op.Args[0], &arg)
			}
			f.mu.Lock()
			f.logins = append(f.logins, arg)
			f.mu.Unlock()
			_ = f.writeJSON(map[string]string{"event": "login", "code": "0", "msg": "", "connId": "abc"})
		case "subscribe":
			for _, raw := range op.Args {
				var a Arg
				if json.Unmarshal(raw, &a) != nil {
					continue
				}
				f.mu.Lock()
				f.subscribe = append(f.subscribe, a)
				f.mu.Unlock()
				_ = f.writeJSON(map[string]any{"event": "subscribe", "arg": a})
				select {
				case f.subscribed <- a:
				default:
				}
			}
		}
	}
}

func (f *fakeOKX) write(mt int, data []byte) error {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	if conn == nil {
		return websocket.ErrCloseSent
	}
	return conn.WriteMessage(mt, data)
}

func (f *fakeOKX) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return f.write(websocket.TextMessage, data)
}

// push 模拟一条频道推送。
func (f *fakeOKX) push(arg Arg, dataJSON string) error {
	argJSON, _ := json.Marshal(arg)
	return f.write(websocket.TextMessage, []byte(`{"arg":`+string(argJSON)+`,"data":`+dataJSON+`}`))
}

// dropConn 单方面掐断当前连接，用来触发客户端重连。
func (f *fakeOKX) dropConn() {
	f.mu.Lock()
	conn := f.conn
	f.conn = nil
	f.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (f *fakeOKX) wsURL() string { return "ws" + strings.TrimPrefix(f.Server.URL, "http") }

func (f *fakeOKX) connCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

func (f *fakeOKX) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.logins)
}

func newWSTestClient(t *testing.T, f *fakeOKX, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithCredentials("key", "secret", "pass"),
		WithWSURLs(f.wsURL(), f.wsURL(), f.wsURL()),
		WithWSPingInterval(200 * time.Millisecond),
		WithWSReconnectDelay(50 * time.Millisecond),
	}
	c, err := NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestWSPrivateLoginThenSubscribeAndDispatch(t *testing.T) {
	f := newFakeOKX(t)
	c := newWSTestClient(t, f)

	ws := c.NewPrivateWS()
	defer ws.Close()

	got := make(chan Position, 4)
	// 订阅在 Connect 之前登记：必须被缓存，并在登录成功后自动发出。
	if err := ws.SubscribePositions("SWAP", "", func(ps []Position) {
		for _, p := range ps {
			got <- p
		}
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ws.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	arg := waitFor(t, f.subscribed, "subscribe")
	if arg.Channel != ChannelPosition || arg.InstType != "SWAP" {
		t.Fatalf("unexpected subscribe arg: %+v", arg)
	}

	// 登录参数必须带上正确的签名。
	f.mu.Lock()
	login := f.logins[0]
	f.mu.Unlock()
	if login.APIKey != "key" || login.Passphrase != "pass" {
		t.Fatalf("unexpected login arg: %+v", login)
	}
	if want := c.wsLoginSign(login.Timestamp); login.Sign != want {
		t.Fatalf("login sign = %q, want %q", login.Sign, want)
	}

	// 推送里带具体 instId，订阅时却只给了 instType，路由必须回退匹配。
	if err := f.push(Arg{Channel: ChannelPosition, InstType: "SWAP", InstID: "ETH-USDT-SWAP"},
		`[{"instId":"ETH-USDT-SWAP","posSide":"long","pos":"3","upl":"1.5"}]`); err != nil {
		t.Fatal(err)
	}

	p := waitFor(t, got, "position push")
	if p.InstID != "ETH-USDT-SWAP" || p.Pos != "3" || p.Upl.Float64() != 1.5 {
		t.Fatalf("unexpected position: %+v", p)
	}
}

func TestWSReconnectResubscribes(t *testing.T) {
	f := newFakeOKX(t)
	c := newWSTestClient(t, f)

	ws := c.NewBusinessWS()
	defer ws.Close()

	got := make(chan Candle, 4)
	if err := ws.SubscribeCandles("ETH-USDT-SWAP", "1m", func(k Candle) { got <- k }); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ws.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f.subscribed, "first subscribe")

	// 掐断连接，客户端应自动重连并重放订阅，无需调用方介入。
	f.dropConn()
	waitFor(t, f.connected, "reconnect")
	arg := waitFor(t, f.subscribed, "resubscribe")
	if arg.Channel != "candle1m" {
		t.Fatalf("resubscribed channel = %q", arg.Channel)
	}
	if f.connCount() < 2 {
		t.Fatalf("connCount = %d, want >= 2", f.connCount())
	}

	if err := f.push(Arg{Channel: "candle1m", InstID: "ETH-USDT-SWAP"},
		`[["1700000000000","100","110","99","105","1","2","3","1"]]`); err != nil {
		t.Fatal(err)
	}
	k := waitFor(t, got, "candle push")
	if k.Close != 105 || !k.Confirm {
		t.Fatalf("unexpected candle: %+v", k)
	}
}

func TestWSPingKeepsConnectionAlive(t *testing.T) {
	f := newFakeOKX(t)
	c := newWSTestClient(t, f)

	ws := c.NewPublicWS()
	defer ws.Close()

	if err := ws.SubscribeTickers("ETH-USDT", func(Ticker) {}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ws.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f.subscribed, "subscribe")

	// 心跳间隔 200ms、读超时为其两倍：静置若干个周期后连接仍应存活，
	// 说明 ping/pong 正常刷新了读截止时间。
	time.Sleep(900 * time.Millisecond)

	if f.connCount() != 1 {
		t.Fatalf("connCount = %d, want 1 (connection should not have been recycled)", f.connCount())
	}
	if err := f.push(Arg{Channel: ChannelTickers, InstID: "ETH-USDT"}, `[{"instId":"ETH-USDT","last":"1"}]`); err != nil {
		t.Fatalf("connection is dead: %v", err)
	}
}

func TestWSCloseStopsReconnect(t *testing.T) {
	f := newFakeOKX(t)
	c := newWSTestClient(t, f)

	ws := c.NewPublicWS()
	if err := ws.SubscribeTickers("ETH-USDT", func(Ticker) {}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ws.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f.connected, "connect")

	if err := ws.Close(); err != nil && !strings.Contains(err.Error(), "closed") {
		t.Fatal(err)
	}
	// 二次 Close 必须是安全的。
	_ = ws.Close()

	before := f.connCount()
	time.Sleep(300 * time.Millisecond)
	if after := f.connCount(); after != before {
		t.Fatalf("reconnected after Close: %d -> %d", before, after)
	}
}

func TestWSPrivateRequiresCredentials(t *testing.T) {
	f := newFakeOKX(t)
	c, err := NewClient(WithWSURLs(f.wsURL(), f.wsURL(), f.wsURL()))
	if err != nil {
		t.Fatal(err)
	}
	ws := c.NewPrivateWS()
	defer ws.Close()
	if err := ws.Connect(context.Background()); err != ErrNoCredentials {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if f.loginCount() != 0 {
		t.Fatal("must not attempt login without credentials")
	}
}
