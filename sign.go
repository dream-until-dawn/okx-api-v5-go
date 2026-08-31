package okx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"time"
)

// isoTimestamp 返回 OKX 要求的 ISO8601 毫秒 UTC 时间戳，例如 2020-12-08T09:08:57.715Z。
func isoTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// signHMAC 计算 Base64(HMAC-SHA256(secret, payload))，OKX REST 与 WS 登录共用。
func signHMAC(secret, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// signRequest 按官方规则签名：Base64(HMAC-SHA256(timestamp + method + requestPath + body))。
//
// requestPath 必须包含 query（如 /api/v5/account/balance?ccy=USDT），
// body 为 POST 的原始 JSON 字符串，GET 请求传空串。
func (c *Client) signRequest(timestamp, method, requestPath, body string) string {
	return signHMAC(c.opt.secretKey, timestamp+method+requestPath+body)
}

// applyAuthHeaders 写入私有接口所需的鉴权请求头。
func (c *Client) applyAuthHeaders(h http.Header, method, requestPath, body string) {
	ts := isoTimestamp(time.Now())
	h.Set("OK-ACCESS-KEY", c.opt.apiKey)
	h.Set("OK-ACCESS-SIGN", c.signRequest(ts, method, requestPath, body))
	h.Set("OK-ACCESS-TIMESTAMP", ts)
	h.Set("OK-ACCESS-PASSPHRASE", c.opt.passphrase)
}

// wsLoginSign 生成 WebSocket 登录签名：
// Base64(HMAC-SHA256(timestamp + "GET" + "/users/self/verify"))，timestamp 为秒级 Unix 时间。
func (c *Client) wsLoginSign(timestamp string) string {
	return signHMAC(c.opt.secretKey, timestamp+"GET"+"/users/self/verify")
}
