// Package util 提供项目通用的小工具：空值判断、脱敏、时间与数字格式化。
//
// @author ygw
package util

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
)

// IsEmpty 判断任意值是否为空（对应项目规范里的 ObjectUtils.isEmpty）。
//
// 规则：nil、空字符串（含纯空白）、长度为 0 的切片/Map/数组/通道、零值指针均视为空；
// 数字零与 false 不视为空（它们是有意义的业务值）。
//
// 参数 v：待判断的值。
// 返回值：为空返回 true。
func IsEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []byte:
		return len(x) == 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.Chan:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface, reflect.Func:
		return rv.IsNil()
	case reflect.String:
		return strings.TrimSpace(rv.String()) == ""
	}
	return false
}

// MaskSecret 把敏感串脱敏，只保留首尾少量字符，用于日志与界面提示。
//
// 参数 s：原始敏感串。
// 返回值：形如 "sk-a…9f2c（长度 35）" 的脱敏文本；为空返回 "（未设置）"。
func MaskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "（未设置）"
	}
	r := []rune(s)
	if len(r) <= 8 {
		return strings.Repeat("•", len(r))
	}
	return fmt.Sprintf("%s…%s（长度 %d）", string(r[:4]), string(r[len(r)-4:]), len(r))
}

// FormatInt 把整数按千分位格式化，如 1234567 -> "1,234,567"。
//
// 参数 n：整数。
// 返回值：带千分位的字符串。
func FormatInt(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// FormatTokens 把 Token 数量格式化成紧凑形式：999、1.2K、3.45M、1.2B。
//
// 参数 n：Token 数量。
// 返回值：紧凑文本。
func FormatTokens(n int64) string {
	a := math.Abs(float64(n))
	switch {
	case a >= 1e9:
		return trimZero(fmt.Sprintf("%.2f", float64(n)/1e9)) + "B"
	case a >= 1e6:
		return trimZero(fmt.Sprintf("%.2f", float64(n)/1e6)) + "M"
	case a >= 1e3:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1e3)) + "K"
	}
	return fmt.Sprintf("%d", n)
}

// trimZero 去掉小数末尾多余的 0 与小数点。
func trimZero(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s
}

// FormatMoney 格式化金额，如 ("CNY", 12.3) -> "¥12.30"。
//
// 参数 currency：币种代码（CNY/USD，其他原样前缀）；amount：金额。
// 返回值：带符号的金额文本，保留两位小数（小额保留四位以免显示成 0）。
func FormatMoney(currency string, amount float64) string {
	sym := currency + " "
	switch strings.ToUpper(currency) {
	case "CNY", "RMB":
		sym = "¥"
	case "USD":
		sym = "$"
	case "":
		sym = ""
	}
	if amount != 0 && math.Abs(amount) < 0.01 {
		return fmt.Sprintf("%s%.4f", sym, amount)
	}
	return fmt.Sprintf("%s%.2f", sym, amount)
}

// FormatRelative 把时间格式化成相对描述，如“刚刚”“3 分钟前”“昨天 12:30”。
//
// 参数 t：目标时间；now：当前时间（便于测试注入）。
// 返回值：中文相对时间文本；零值时间返回“从未”。
func FormatRelative(t, now time.Time) string {
	if t.IsZero() {
		return "从未"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "刚刚"
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour && t.Day() == now.Day():
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	}
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	if time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location()).Sub(time.Date(y2, m2, d2, 0, 0, 0, 0, t.Location())) == 24*time.Hour {
		return "昨天 " + t.Format("15:04")
	}
	return t.Format("01-02 15:04")
}

// StartOfDay 返回 t 所在本地日的零点。
//
// 参数 t：时间。
// 返回值：当日 00:00:00（同时区）。
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Clamp 把 v 限制在 [lo, hi] 区间。
//
// 参数 v：值；lo/hi：下上界。
// 返回值：限制后的值。
func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// FormatCountdown 把剩余时长格式化成精确到分钟的倒计时：
// 1 天以上 "1天3时"，1 小时以上 "12时5分"，不足 1 小时 "42分"，不足 1 分钟 "不到1分"。
//
// 参数 d：剩余时长；小于等于 0 返回 "0分"。
// 返回值：倒计时文本。
func FormatCountdown(d time.Duration) string {
	if d <= 0 {
		return "0分"
	}
	mins := int(d / time.Minute)
	switch {
	case mins >= 24*60:
		return fmt.Sprintf("%d天%d时", mins/(24*60), mins%(24*60)/60)
	case mins >= 60:
		return fmt.Sprintf("%d时%d分", mins/60, mins%60)
	case mins >= 1:
		return fmt.Sprintf("%d分", mins)
	}
	return "不到1分"
}
