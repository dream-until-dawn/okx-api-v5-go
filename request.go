package okx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Response 是 OKX V5 统一响应结构。data 恒为数组，即便逻辑上只有一条记录。
type Response[T any] struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []T    `json:"data"`
	// InTime / OutTime 仅部分交易接口返回，单位微秒。
	InTime  string `json:"inTime,omitempty"`
	OutTime string `json:"outTime,omitempty"`
}

// itemStatus 用于在 code != "0" 时提取批量接口里单条记录的错误信息。
type itemStatus struct {
	SCode string `json:"sCode"`
	SMsg  string `json:"sMsg"`
}

// Do 发起一次任意 OKX 接口调用，用于 SDK 尚未封装的接口。
//
// method 为 HTTP 方法，path 为不含 query 的接口路径（如 /api/v5/account/balance），
// query 可为 nil，body 会被序列化为 JSON（GET 请求应传 nil），
// private 为 true 时附加签名头。
func Do[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any, private bool) (*Response[T], error) {
	return doRequest[T](ctx, c, method, path, query, body, private)
}

// request 是所有接口的统一入口，返回 data 数组。
func request[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any, private bool) ([]T, error) {
	resp, err := doRequest[T](ctx, c, method, path, query, body, private)
	if resp == nil {
		return nil, err
	}
	return resp.Data, err
}

// requestOne 用于语义上只返回一条记录的接口；data 为空时返回 [ErrNoData]。
func requestOne[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any, private bool) (T, error) {
	var zero T
	list, err := request[T](ctx, c, method, path, query, body, private)
	if err != nil {
		if len(list) > 0 {
			return list[0], err
		}
		return zero, err
	}
	if len(list) == 0 {
		return zero, ErrNoData
	}
	return list[0], nil
}

func doRequest[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any, private bool) (*Response[T], error) {
	if private && !c.HasCredentials() {
		return nil, ErrNoCredentials
	}

	requestPath := path
	if len(query) > 0 {
		requestPath += "?" + query.Encode()
	}

	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("okx: marshal request body: %w", err)
		}
		// OKX 对 body 的签名是逐字节的，因此签名与发送必须使用同一份字节。
		bodyBytes = b
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		if c.opt.limiter != nil {
			if err := c.opt.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		resp, retryable, err := c.roundTrip(ctx, method, requestPath, bodyBytes, private)
		if err == nil {
			return parseResponse[T](resp)
		}
		lastErr = err

		if !retryable || attempt >= c.opt.retryTimes {
			return nil, lastErr
		}
		c.opt.logger.Debugf("okx: %s %s failed (attempt %d/%d): %v", method, path, attempt+1, c.opt.retryTimes, err)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.opt.retryDelay):
		}
	}
}

// roundTrip 执行单次请求，返回响应体、是否可重试、错误。
func (c *Client) roundTrip(ctx context.Context, method, requestPath string, body []byte, private bool) ([]byte, bool, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.opt.restURL+requestPath, reader)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.opt.simulated {
		req.Header.Set("x-simulated-trading", "1")
	}
	if private {
		c.applyAuthHeaders(req.Header, method, requestPath, string(body))
	}

	res, err := c.http.Do(req)
	if err != nil {
		// 网络层错误（超时、连接重置等）通常是瞬时的，可以重试。
		return nil, true, fmt.Errorf("okx: %s %s: %w", method, requestPath, err)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, true, fmt.Errorf("okx: read response body: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		he := &HTTPError{StatusCode: res.StatusCode, Body: string(data)}
		// OKX 的鉴权 / 限频失败会用非 2xx 返回，但 body 仍是标准信封，
		// 里面的 code 才是可编程处理的原因（如 401 + 50120），优先把它暴露出来。
		if apiErr := parseEnvelopeError(data); apiErr != nil {
			apiErr.HTTPStatus = res.StatusCode
			apiErr.httpErr = he
			return nil, he.Temporary(), apiErr
		}
		return nil, he.Temporary(), he
	}
	return data, false, nil
}

// parseEnvelopeError 尝试把响应体当作 OKX 标准信封解析出业务错误；
// 不是这个形状（例如网关返回的 HTML）时返回 nil。
func parseEnvelopeError(data []byte) *APIError {
	var env struct {
		Code string       `json:"code"`
		Msg  string       `json:"msg"`
		Data []itemStatus `json:"data"`
	}
	if json.Unmarshal(data, &env) != nil || env.Code == "" || env.Code == "0" {
		return nil
	}
	e := &APIError{Code: env.Code, Msg: env.Msg, Body: truncate(string(data), 1024)}
	for _, it := range env.Data {
		if it.SCode != "" && it.SCode != "0" {
			e.SCode, e.SMsg = it.SCode, it.SMsg
			break
		}
	}
	return e
}

func parseResponse[T any](data []byte) (*Response[T], error) {
	var resp Response[T]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("okx: decode response %s: %w", truncate(string(data), 512), err)
	}
	if resp.Code == "0" {
		return &resp, nil
	}

	apiErr := &APIError{Code: resp.Code, Msg: resp.Msg, Body: truncate(string(data), 1024)}
	// code 为 "1"（全部失败）/ "2"（部分成功）时，真正的原因在每条记录的 sCode 上。
	var detail struct {
		Data []itemStatus `json:"data"`
	}
	if json.Unmarshal(data, &detail) == nil {
		for _, it := range detail.Data {
			if it.SCode != "" && it.SCode != "0" {
				apiErr.SCode, apiErr.SMsg = it.SCode, it.SMsg
				break
			}
		}
	}
	// 部分成功时仍把 data 返回给调用方，便于识别哪几笔成功。
	return &resp, apiErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// params 是构造 query 的小工具：空值自动忽略，避免签名串里出现无意义的参数。
type params url.Values

func newParams() params { return params{} }

func (p params) set(key, value string) params {
	if value != "" {
		url.Values(p).Set(key, value)
	}
	return p
}

func (p params) setNum(key string, value Num) params { return p.set(key, string(value)) }

func (p params) setInt(key string, value int) params {
	if value != 0 {
		url.Values(p).Set(key, strconv.Itoa(value))
	}
	return p
}

func (p params) setInt64(key string, value int64) params {
	if value != 0 {
		url.Values(p).Set(key, strconv.FormatInt(value, 10))
	}
	return p
}

func (p params) setBool(key string, value bool) params {
	if value {
		url.Values(p).Set(key, "true")
	}
	return p
}

// setList 以逗号分隔写入多值参数（OKX 的 instId、ordId 等批量查询用法）。
func (p params) setList(key string, values []string) params {
	if len(values) > 0 {
		url.Values(p).Set(key, strings.Join(values, ","))
	}
	return p
}

func (p params) values() url.Values { return url.Values(p) }
