package okx

import (
	"context"
	"net/http"
	"time"
)

// 默认接入地址。
const (
	DefaultRESTURL = "https://www.okx.com"
	// AWSRESTURL 是 OKX 提供的 AWS 加速域名，部署在海外云上时通常更快。
	AWSRESTURL = "https://aws.okx.com"

	DefaultWSPublicURL   = "wss://ws.okx.com:8443/ws/v5/public"
	DefaultWSPrivateURL  = "wss://ws.okx.com:8443/ws/v5/private"
	DefaultWSBusinessURL = "wss://ws.okx.com:8443/ws/v5/business"

	// 模拟盘（Demo Trading）WebSocket 地址，REST 走同一域名但需带 x-simulated-trading 头。
	DemoWSPublicURL   = "wss://wspap.okx.com:8443/ws/v5/public"
	DemoWSPrivateURL  = "wss://wspap.okx.com:8443/ws/v5/private"
	DemoWSBusinessURL = "wss://wspap.okx.com:8443/ws/v5/business"
)

// 443 端口上的等价 WebSocket 地址。
//
// OKX 官方文档给的是 8443 端口，但同样的服务在 443 上也可用。部分企业网络、
// 校园网和运营商只放行 443，会让 8443 的 TLS 握手直接超时（表现为
// "dial ws: context deadline exceeded"，而 REST 却一切正常）。遇到这种情况
// 用 [WithWSPort443] 切过去即可。
const (
	AltWSPublicURL   = "wss://ws.okx.com/ws/v5/public"
	AltWSPrivateURL  = "wss://ws.okx.com/ws/v5/private"
	AltWSBusinessURL = "wss://ws.okx.com/ws/v5/business"

	AltDemoWSPublicURL   = "wss://wspap.okx.com/ws/v5/public"
	AltDemoWSPrivateURL  = "wss://wspap.okx.com/ws/v5/private"
	AltDemoWSBusinessURL = "wss://wspap.okx.com/ws/v5/business"
)

// Logger 是 SDK 的日志接口，默认不输出任何日志。
// 传入自己的实现即可接入项目现有日志库。
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Errorf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

// Limiter 是可选的限流器接口，签名与 golang.org/x/time/rate.Limiter 兼容，
// 可直接传入 rate.NewLimiter(...) 以遵守 OKX 的接口频率限制。
type Limiter interface {
	Wait(ctx context.Context) error
}

type options struct {
	apiKey     string
	secretKey  string
	passphrase string

	restURL       string
	wsPublicURL   string
	wsPrivateURL  string
	wsBusinessURL string

	simulated bool
	wsPort443 bool

	httpClient *http.Client
	timeout    time.Duration
	proxyURL   string

	retryTimes int
	retryDelay time.Duration

	brokerTag string

	logger  Logger
	limiter Limiter

	wsReconnectDelay time.Duration
	wsPingInterval   time.Duration
}

// Option 用于配置 Client。
type Option func(*options)

// WithCredentials 配置 API 凭证。只调用公共接口时可以省略。
func WithCredentials(apiKey, secretKey, passphrase string) Option {
	return func(o *options) {
		o.apiKey, o.secretKey, o.passphrase = apiKey, secretKey, passphrase
	}
}

// WithRESTURL 自定义 REST 接入地址，例如 [AWSRESTURL]。
func WithRESTURL(u string) Option { return func(o *options) { o.restURL = u } }

// WithWSURLs 自定义 WebSocket 接入地址（公共 / 私有 / 业务），传空串表示保持默认。
func WithWSURLs(public, private, business string) Option {
	return func(o *options) {
		if public != "" {
			o.wsPublicURL = public
		}
		if private != "" {
			o.wsPrivateURL = private
		}
		if business != "" {
			o.wsBusinessURL = business
		}
	}
}

// WithSimulated 切换到模拟盘：REST 自动附加 x-simulated-trading: 1，
// WebSocket 自动改用 wspap.okx.com 域名。
func WithSimulated(simulated bool) Option {
	return func(o *options) { o.simulated = simulated }
}

// WithHTTPClient 使用调用方自己的 *http.Client（此时 WithTimeout / WithProxy 不再生效）。
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.httpClient = hc }
}

// WithTimeout 设置单次 HTTP 请求超时，默认 15s。
func WithTimeout(d time.Duration) Option { return func(o *options) { o.timeout = d } }

// WithProxy 设置 HTTP 代理，例如 "http://127.0.0.1:7890" 或 "socks5://127.0.0.1:1080"。
func WithProxy(u string) Option { return func(o *options) { o.proxyURL = u } }

// WithRetry 设置失败重试次数与间隔，默认 3 次 / 1s。
// 只有网络错误、5xx 和 429 会重试，业务错误码不会。
func WithRetry(times int, delay time.Duration) Option {
	return func(o *options) { o.retryTimes, o.retryDelay = times, delay }
}

// WithBrokerTag 设置默认的订单 tag（OKX 经纪商返佣标识），
// 下单时未显式指定 Tag 的请求会自动带上。
func WithBrokerTag(tag string) Option { return func(o *options) { o.brokerTag = tag } }

// WithLogger 注入日志实现，默认静默。
func WithLogger(l Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithLimiter 注入限流器，每次 REST 请求前会调用 Wait。
func WithLimiter(l Limiter) Option { return func(o *options) { o.limiter = l } }

// WithWSPort443 让 WebSocket 走 443 端口而非默认的 8443。
//
// 当 REST 正常、WebSocket 却始终连不上（dial 超时）时，多半是网络只放行 443，
// 打开这个开关即可。它会同时作用于实盘与模拟盘；若已用 [WithWSURLs] 显式指定
// 地址，则以显式地址为准。
func WithWSPort443(enabled bool) Option {
	return func(o *options) { o.wsPort443 = enabled }
}

// WithWSReconnectDelay 设置 WebSocket 断线重连的等待时间，默认 5s。
func WithWSReconnectDelay(d time.Duration) Option {
	return func(o *options) { o.wsReconnectDelay = d }
}

// WithWSPingInterval 设置 WebSocket 心跳间隔，默认 20s（OKX 要求 30s 内有数据往来）。
func WithWSPingInterval(d time.Duration) Option {
	return func(o *options) { o.wsPingInterval = d }
}

func defaultOptions() *options {
	return &options{
		restURL:          DefaultRESTURL,
		wsPublicURL:      DefaultWSPublicURL,
		wsPrivateURL:     DefaultWSPrivateURL,
		wsBusinessURL:    DefaultWSBusinessURL,
		timeout:          15 * time.Second,
		retryTimes:       3,
		retryDelay:       time.Second,
		logger:           nopLogger{},
		wsReconnectDelay: 5 * time.Second,
		wsPingInterval:   20 * time.Second,
	}
}
