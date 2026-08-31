package okx

import (
	"errors"
	"fmt"
	"net/http"
)

// APIError 表示 OKX 返回的业务错误。
//
// 多数情况下 OKX 用 HTTP 200 + code != "0" 表达业务错误，但鉴权、限频一类失败
// 会用非 2xx 状态码返回（如 401 + code 50120「API key 无此权限」）。两种情况
// 都会得到 *APIError，HTTPStatus 非零即表示后者；此时 [AsHTTPError] 也能从
// 错误链中取到对应的 *HTTPError。
type APIError struct {
	Code string // OKX 业务错误码，如 "51000"
	Msg  string // OKX 错误描述
	// SCode / SMsg 来自 data 数组中单条记录的 sCode / sMsg（下单、撤单等批量接口）。
	// 顶层 code 为 "1"（全部失败）或 "2"（部分成功）时才有意义。
	SCode string
	SMsg  string
	Body  string // 原始响应体，便于排查
	// HTTPStatus 仅在 OKX 用非 2xx 状态码返回业务错误时非零。
	HTTPStatus int

	httpErr *HTTPError
}

func (e *APIError) Error() string {
	var status string
	if e.HTTPStatus != 0 {
		status = fmt.Sprintf(" http=%d", e.HTTPStatus)
	}
	if e.SCode != "" && e.SCode != "0" {
		return fmt.Sprintf("okx api error%s: code=%s msg=%s sCode=%s sMsg=%s", status, e.Code, e.Msg, e.SCode, e.SMsg)
	}
	return fmt.Sprintf("okx api error%s: code=%s msg=%s", status, e.Code, e.Msg)
}

// Unwrap 让非 2xx 场景下的 *APIError 仍能被 [AsHTTPError] 取到底层 HTTP 错误。
func (e *APIError) Unwrap() error {
	if e.httpErr == nil {
		return nil
	}
	return e.httpErr
}

// HTTPError 表示非 2xx 的 HTTP 响应。
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("okx http error: status=%d body=%s", e.StatusCode, e.Body)
}

// Temporary 表示该 HTTP 错误可以重试（5xx 与 429）。
func (e *HTTPError) Temporary() bool {
	return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
}

// ErrNoData 表示接口调用成功但 data 数组为空，
// 由那些语义上必然返回一条记录的方法（如 Ticker、Balance）抛出。
var ErrNoData = errors.New("okx: empty data in response")

// ErrNoCredentials 表示调用私有接口但未配置 API Key。
var ErrNoCredentials = errors.New("okx: api credentials are required for private endpoints")

// AsAPIError 从 error 链中提取 *APIError，便于按错误码分支处理。
func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	ok := errors.As(err, &e)
	return e, ok
}

// AsHTTPError 从 error 链中提取 *HTTPError。
func AsHTTPError(err error) (*HTTPError, bool) {
	var e *HTTPError
	ok := errors.As(err, &e)
	return e, ok
}

// IsCode 判断错误是否为指定 OKX 业务错误码（顶层 code 或单条 sCode）。
func IsCode(err error, code string) bool {
	e, ok := AsAPIError(err)
	if !ok {
		return false
	}
	return e.Code == code || e.SCode == code
}
