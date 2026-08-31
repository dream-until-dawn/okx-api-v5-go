package okx

import (
	"strconv"
	"time"
)

// Num 表示 OKX 返回的数值字段。
//
// OKX 的 JSON 里所有数值都是字符串（且可能为空串），直接用 float64 解析会丢精度、
// 空串还会报错。Num 底层就是 string，因此可以无损承载原始值，同时提供转换方法。
type Num string

// String 返回原始字符串，空值返回 ""。
func (n Num) String() string { return string(n) }

// IsEmpty 判断字段是否为空（OKX 用空串表示"无此值"）。
func (n Num) IsEmpty() bool { return n == "" }

// Float64 转 float64，空串或非法值返回 0。
func (n Num) Float64() float64 {
	f, _ := n.Float64E()
	return f
}

// Float64E 转 float64 并返回错误，空串返回 (0, nil)。
func (n Num) Float64E() (float64, error) {
	if n == "" {
		return 0, nil
	}
	return strconv.ParseFloat(string(n), 64)
}

// Int64 转 int64，空串或非法值返回 0。
func (n Num) Int64() int64 {
	i, _ := n.Int64E()
	return i
}

// Int64E 转 int64 并返回错误，空串返回 (0, nil)。
func (n Num) Int64E() (int64, error) {
	if n == "" {
		return 0, nil
	}
	return strconv.ParseInt(string(n), 10, 64)
}

// Bool 按 OKX 约定解析布尔值（"true" / "1" 为真）。
func (n Num) Bool() bool { return n == "true" || n == "1" }

// Time 把毫秒时间戳字段转为 time.Time，空串返回零值。
func (n Num) Time() time.Time {
	ms := n.Int64()
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// NumOf 由 float64 构造 Num，保留最少必要位数。
func NumOf(f float64) Num {
	return Num(strconv.FormatFloat(f, 'f', -1, 64))
}

// NumOfInt 由整数构造 Num。
func NumOfInt(i int64) Num { return Num(strconv.FormatInt(i, 10)) }
