package okx

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是 OKX V5 API 的入口。它是并发安全的，一个进程共用一个实例即可。
//
// REST 接口按官方文档分组挂在 Account / Trade / Market / Public 四个字段下；
// WebSocket 通过 [Client.NewPublicWS]、[Client.NewBusinessWS]、[Client.NewPrivateWS] 创建。
type Client struct {
	opt  *options
	http *http.Client

	// Account 账户相关接口（/api/v5/account/*，需要签名）。
	Account *AccountService
	// Trade 交易相关接口（/api/v5/trade/*，需要签名）。
	Trade *TradeService
	// Market 行情接口（/api/v5/market/*，公开）。
	Market *MarketService
	// Public 公共数据接口（/api/v5/public/*，公开）。
	Public *PublicService
}

// NewClient 创建客户端。不传 [WithCredentials] 时只能调用公共接口。
func NewClient(opts ...Option) (*Client, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(o)
	}

	o.restURL = strings.TrimSuffix(o.restURL, "/")
	if o.restURL == "" {
		return nil, fmt.Errorf("okx: rest url must not be empty")
	}

	// 按 simulated（wspap 域名）与 wsPort443（443 端口）推导 WebSocket 地址。
	// 只在调用方没有用 WithWSURLs 显式指定时才推导，尊重其选择。
	if o.wsPublicURL == DefaultWSPublicURL {
		o.wsPublicURL = resolveWSURL(o, DemoWSPublicURL, AltWSPublicURL, AltDemoWSPublicURL, DefaultWSPublicURL)
	}
	if o.wsPrivateURL == DefaultWSPrivateURL {
		o.wsPrivateURL = resolveWSURL(o, DemoWSPrivateURL, AltWSPrivateURL, AltDemoWSPrivateURL, DefaultWSPrivateURL)
	}
	if o.wsBusinessURL == DefaultWSBusinessURL {
		o.wsBusinessURL = resolveWSURL(o, DemoWSBusinessURL, AltWSBusinessURL, AltDemoWSBusinessURL, DefaultWSBusinessURL)
	}

	hc := o.httpClient
	if hc == nil {
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		}
		if o.proxyURL != "" {
			pu, err := url.Parse(o.proxyURL)
			if err != nil {
				return nil, fmt.Errorf("okx: invalid proxy url %q: %w", o.proxyURL, err)
			}
			transport.Proxy = http.ProxyURL(pu)
		}
		hc = &http.Client{Timeout: o.timeout, Transport: transport}
	}

	c := &Client{opt: o, http: hc}
	c.Account = &AccountService{c: c}
	c.Trade = &TradeService{c: c}
	c.Market = &MarketService{c: c}
	c.Public = &PublicService{c: c}
	return c, nil
}

// HasCredentials 报告是否配置了完整的 API 凭证。
func (c *Client) HasCredentials() bool {
	return c.opt.apiKey != "" && c.opt.secretKey != "" && c.opt.passphrase != ""
}

// Simulated 报告当前是否为模拟盘模式。
func (c *Client) Simulated() bool { return c.opt.simulated }

// RESTURL 返回当前使用的 REST 接入地址。
func (c *Client) RESTURL() string { return c.opt.restURL }

// HTTPClient 返回底层 *http.Client，便于复用连接池或调整设置。
func (c *Client) HTTPClient() *http.Client { return c.http }

// resolveWSURL 在 实盘/模拟盘 × 8443/443 四种组合里选出对应地址。
func resolveWSURL(o *options, demo, altLive, altDemo, live string) string {
	switch {
	case o.simulated && o.wsPort443:
		return altDemo
	case o.simulated:
		return demo
	case o.wsPort443:
		return altLive
	default:
		return live
	}
}
