package okx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 固定向量（由独立的 HMAC-SHA256 + Base64 实现交叉验证），用来锁死签名算法不被改坏。
func TestSignRequest(t *testing.T) {
	c := &Client{opt: &options{secretKey: "22582BD0CFF14C41EDBF1AB98506286D"}}
	got := c.signRequest("2020-12-08T09:08:57.715Z", "GET", "/api/v5/account/balance?ccy=BTC", "")
	want := "HiZhvSfMtWJA3uUIVXV3a/bSXNPCWvYFXoGCVS8V4zY="
	if got != want {
		t.Fatalf("signRequest = %q, want %q", got, want)
	}
}

func TestSignRequestWithBody(t *testing.T) {
	c := &Client{opt: &options{secretKey: "secret"}}
	body := `{"instId":"BTC-USDT","tdMode":"cash"}`
	// 同样的输入必须得到同样的输出，且 body 参与签名。
	withBody := c.signRequest("2020-12-08T09:08:57.715Z", "POST", "/api/v5/trade/order", body)
	withoutBody := c.signRequest("2020-12-08T09:08:57.715Z", "POST", "/api/v5/trade/order", "")
	if withBody == withoutBody {
		t.Fatal("body must be part of the signature payload")
	}
}

func TestISOTimestamp(t *testing.T) {
	ts := isoTimestamp(time.Date(2020, 12, 8, 9, 8, 57, 715000000, time.UTC))
	if ts != "2020-12-08T09:08:57.715Z" {
		t.Fatalf("isoTimestamp = %q", ts)
	}
}

func TestWSLoginSign(t *testing.T) {
	c := &Client{opt: &options{secretKey: "secret"}}
	// WS 登录签名固定使用 GET /users/self/verify 作为 payload 的一部分。
	want := signHMAC("secret", "1538054050GET/users/self/verify")
	if got := c.wsLoginSign("1538054050"); got != want {
		t.Fatalf("wsLoginSign = %q, want %q", got, want)
	}
}

func TestNum(t *testing.T) {
	if got := Num("1.25").Float64(); got != 1.25 {
		t.Fatalf("Float64 = %v", got)
	}
	if got := Num("").Float64(); got != 0 {
		t.Fatalf("empty Float64 = %v, want 0", got)
	}
	if _, err := Num("").Float64E(); err != nil {
		t.Fatalf("empty Float64E returned error: %v", err)
	}
	if _, err := Num("abc").Float64E(); err == nil {
		t.Fatal("expected error for non-numeric Num")
	}
	if !Num("").IsEmpty() {
		t.Fatal("empty Num should report IsEmpty")
	}
	if got := Num("1700000000000").Time().UTC(); got.Year() != 2023 {
		t.Fatalf("Time = %v", got)
	}
	if got := NumOf(0.1); got != "0.1" {
		t.Fatalf("NumOf = %q", got)
	}
}

func TestParamsSkipsEmptyValues(t *testing.T) {
	q := newParams().
		set("instId", "ETH-USDT-SWAP").
		set("bar", "").
		setInt("limit", 0).
		setInt64("after", 0).
		setBool("reduceOnly", false).
		setList("ccy", []string{"USDT", "BTC"}).
		values()

	if got := q.Encode(); got != "ccy=USDT%2CBTC&instId=ETH-USDT-SWAP" {
		t.Fatalf("encoded query = %q", got)
	}
}

func TestCandleParsing(t *testing.T) {
	raw := rawCandle{"1700000000000", "100.5", "110", "99", "105", "12", "13", "14", "1"}
	c := raw.toCandle()
	if c.Ts != 1700000000000 || c.Open != 100.5 || c.High != 110 || c.Low != 99 || c.Close != 105 {
		t.Fatalf("unexpected candle: %+v", c)
	}
	if !c.Confirm {
		t.Fatal("confirm should be true when the raw field is \"1\"")
	}
	// 长度不足的数组不应 panic（OKX 部分频道只返回前几个字段）。
	short := rawCandle{"1700000000000", "100"}
	if got := short.toCandle(); got.Close != 0 || got.Ts != 1700000000000 {
		t.Fatalf("short candle mishandled: %+v", got)
	}
}

func TestBookLevelUnmarshal(t *testing.T) {
	var book OrderBook
	if err := json.Unmarshal([]byte(`{"asks":[["41006.8","0.6","0","4"]],"bids":[],"ts":"1700000000000"}`), &book); err != nil {
		t.Fatal(err)
	}
	if len(book.Asks) != 1 {
		t.Fatalf("asks = %v", book.Asks)
	}
	if book.Asks[0].Px != "41006.8" || book.Asks[0].Sz != "0.6" || book.Asks[0].Orders != "4" {
		t.Fatalf("unexpected level: %+v", book.Asks[0])
	}
}

// newTestClient 起一个假的 OKX 服务端，返回客户端与最近一次收到的请求。
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := NewClient(
		WithCredentials("key", "secret", "pass"),
		WithRESTURL(srv.URL),
		WithRetry(0, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPrivateGetSendsAuthHeaders(t *testing.T) {
	var gotPath string
	var gotHeader http.Header
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotHeader = r.Header.Clone()
		w.Write([]byte(`{"code":"0","msg":"","data":[{"totalEq":"100","details":[{"ccy":"USDT","eq":"100"}]}]}`))
	})

	bal, err := c.Account.Balance(context.Background(), "USDT")
	if err != nil {
		t.Fatal(err)
	}
	if bal.TotalEq != "100" {
		t.Fatalf("totalEq = %q", bal.TotalEq)
	}
	if _, ok := bal.Detail("USDT"); !ok {
		t.Fatal("USDT detail missing")
	}
	if gotPath != "/api/v5/account/balance?ccy=USDT" {
		t.Fatalf("path = %q", gotPath)
	}
	for _, h := range []string{"OK-ACCESS-KEY", "OK-ACCESS-SIGN", "OK-ACCESS-TIMESTAMP", "OK-ACCESS-PASSPHRASE"} {
		if gotHeader.Get(h) == "" {
			t.Fatalf("missing header %s", h)
		}
	}
	if gotHeader.Get("x-simulated-trading") != "" {
		t.Fatal("live client must not send the simulated-trading header")
	}
}

func TestSimulatedHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-simulated-trading")
		w.Write([]byte(`{"code":"0","msg":"","data":[{"ts":"1"}]}`))
	}))
	defer srv.Close()

	c, err := NewClient(WithRESTURL(srv.URL), WithSimulated(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Public.ServerTime(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "1" {
		t.Fatalf("x-simulated-trading = %q, want \"1\"", got)
	}
	if c.opt.wsPublicURL != DemoWSPublicURL {
		t.Fatalf("ws public url = %q, want demo url", c.opt.wsPublicURL)
	}
}

func TestSignedBodyMatchesSentBody(t *testing.T) {
	// 签名针对的 body 字节必须与实际发出的完全一致，否则交易所会判签名错误。
	var sentBody []byte
	var sign, ts string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		sentBody, _ = readAll(r)
		sign = r.Header.Get("OK-ACCESS-SIGN")
		ts = r.Header.Get("OK-ACCESS-TIMESTAMP")
		w.Write([]byte(`{"code":"0","msg":"","data":[{"ordId":"1","sCode":"0"}]}`))
	})

	if _, err := c.Trade.PlaceOrder(context.Background(), OrderRequest{
		InstID: "ETH-USDT-SWAP", TdMode: TdModeIsolated, Side: SideBuy, OrdType: OrdTypeMarket, Sz: "1",
	}); err != nil {
		t.Fatal(err)
	}

	want := c.signRequest(ts, http.MethodPost, "/api/v5/trade/order", string(sentBody))
	if sign != want {
		t.Fatalf("signature mismatch\n got: %s\nwant: %s\nbody: %s", sign, want, sentBody)
	}
}

func TestBrokerTagDefault(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := readAll(r)
		_ = json.Unmarshal(raw, &body)
		w.Write([]byte(`{"code":"0","msg":"","data":[{"ordId":"1","sCode":"0"}]}`))
	}))
	defer srv.Close()

	c, err := NewClient(
		WithCredentials("k", "s", "p"),
		WithRESTURL(srv.URL),
		WithBrokerTag("mytag"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Trade.PlaceOrder(context.Background(), OrderRequest{InstID: "X", TdMode: "cash", Side: "buy", OrdType: "market", Sz: "1"}); err != nil {
		t.Fatal(err)
	}
	if body["tag"] != "mytag" {
		t.Fatalf("tag = %v, want mytag", body["tag"])
	}
}

func TestAPIErrorCarriesItemCode(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":"1","msg":"","data":[{"ordId":"","sCode":"51000","sMsg":"Parameter sz error"}]}`))
	})

	_, err := c.Trade.PlaceOrder(context.Background(), OrderRequest{InstID: "X", TdMode: "cash", Side: "buy", OrdType: "market", Sz: "0"})
	if err == nil {
		t.Fatal("expected an error")
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("error is not *APIError: %T", err)
	}
	if apiErr.SCode != "51000" || apiErr.SMsg != "Parameter sz error" {
		t.Fatalf("unexpected error detail: %+v", apiErr)
	}
	if !IsCode(err, "51000") {
		t.Fatal("IsCode should match the item-level sCode")
	}
}

func TestRetryOnServerError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"code":"0","msg":"","data":[{"ts":"1"}]}`))
	}))
	defer srv.Close()

	c, err := NewClient(WithRESTURL(srv.URL), WithRetry(3, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Public.ServerTime(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c, err := NewClient(WithRESTURL(srv.URL), WithRetry(3, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Public.ServerTime(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if he, ok := AsHTTPError(err); !ok || he.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (4xx must not be retried)", calls)
	}
}

func TestPrivateEndpointRequiresCredentials(t *testing.T) {
	c, err := NewClient(WithRESTURL("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Account.Balance(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
}

func TestEmptyDataReturnsErrNoData(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":"0","msg":"","data":[]}`))
	})
	if _, err := c.Market.Ticker(context.Background(), "ETH-USDT"); !errors.Is(err, ErrNoData) {
		t.Fatalf("err = %v, want ErrNoData", err)
	}
}

func TestArgRouteKeyFallback(t *testing.T) {
	// 按 instType 订阅、按 instId 推送时，路由必须回退到只匹配频道。
	c, _ := NewClient()
	ws := c.NewPrivateWS()
	got := make(chan string, 1)
	if err := ws.Subscribe(func(m *WSMessage) { got <- m.Arg.InstID }, Arg{Channel: "positions", InstType: "SWAP"}); err != nil {
		t.Fatal(err)
	}
	ws.dispatch(&WSMessage{Arg: Arg{Channel: "positions", InstType: "SWAP", InstID: "ETH-USDT-SWAP"}}, nil)
	select {
	case id := <-got:
		if id != "ETH-USDT-SWAP" {
			t.Fatalf("instId = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not invoked")
	}
}

func TestWSUnmarshal(t *testing.T) {
	msg := &WSMessage{
		Arg:  Arg{Channel: "tickers", InstID: "ETH-USDT"},
		Data: json.RawMessage(`[{"instId":"ETH-USDT","last":"2500.1"}]`),
	}
	tickers, err := Unmarshal[Ticker](msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 1 || tickers[0].Last.Float64() != 2500.1 {
		t.Fatalf("tickers = %+v", tickers)
	}
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func TestNon2xxWithOKXEnvelopeYieldsAPIError(t *testing.T) {
	// OKX 的鉴权失败是 401 + 标准信封（实测：只读 Key 下单返回 401 code=50120）。
	// 这种情况必须能按业务错误码分支，而不是只拿到一个 HTTP 状态码。
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"msg":"This API key doesn't have permission to use this function","code":"50120"}`))
	})

	_, err := c.Trade.PlaceOrder(context.Background(), OrderRequest{InstID: "X", TdMode: "cash", Side: "buy", OrdType: "market", Sz: "1"})
	if err == nil {
		t.Fatal("expected an error")
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("error is not *APIError: %T %v", err, err)
	}
	if apiErr.Code != "50120" || apiErr.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("unexpected APIError: %+v", apiErr)
	}
	if !IsCode(err, "50120") {
		t.Fatal("IsCode should match a business code returned with a non-2xx status")
	}
	// 底层 HTTP 信息不能丢。
	httpErr, ok := AsHTTPError(err)
	if !ok || httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("HTTP error not reachable through the chain: %v", err)
	}
}

func TestNon2xxWithoutEnvelopeStaysHTTPError(t *testing.T) {
	// 网关返回的 HTML 之类不是 OKX 信封，应保持为纯 *HTTPError。
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`<html>502 Bad Gateway</html>`))
	})
	_, err := c.Account.Balance(context.Background())
	if _, ok := AsAPIError(err); ok {
		t.Fatalf("non-envelope body must not become an APIError: %v", err)
	}
	if he, ok := AsHTTPError(err); !ok || he.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWSURLResolution(t *testing.T) {
	cases := []struct {
		name            string
		opts            []Option
		public, private string
	}{
		{"默认", nil, DefaultWSPublicURL, DefaultWSPrivateURL},
		{"模拟盘", []Option{WithSimulated(true)}, DemoWSPublicURL, DemoWSPrivateURL},
		{"443 端口", []Option{WithWSPort443(true)}, AltWSPublicURL, AltWSPrivateURL},
		{"模拟盘+443", []Option{WithSimulated(true), WithWSPort443(true)}, AltDemoWSPublicURL, AltDemoWSPrivateURL},
		{"显式覆盖优先", []Option{WithSimulated(true), WithWSPort443(true), WithWSURLs("wss://x/pub", "wss://x/priv", "")},
			"wss://x/pub", "wss://x/priv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if c.opt.wsPublicURL != tc.public {
				t.Errorf("public = %q, want %q", c.opt.wsPublicURL, tc.public)
			}
			if c.opt.wsPrivateURL != tc.private {
				t.Errorf("private = %q, want %q", c.opt.wsPrivateURL, tc.private)
			}
		})
	}
}
