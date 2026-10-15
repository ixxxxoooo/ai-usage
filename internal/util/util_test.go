package util

import (
	"testing"
	"time"
)

// TestIsEmpty 覆盖各种类型的空值判断。
func TestIsEmpty(t *testing.T) {
	var np *int
	n := 0
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"nil", nil, true},
		{"空串", "", true},
		{"纯空白", "  \t\n", true},
		{"非空串", "a", false},
		{"空切片", []int{}, true},
		{"nil切片", []string(nil), true},
		{"非空切片", []int{1}, false},
		{"空map", map[string]int{}, true},
		{"非空map", map[string]int{"a": 1}, false},
		{"nil指针", np, true},
		{"非nil指针", &n, false},
		{"数字0不算空", 0, false},
		{"false不算空", false, false},
		{"空字节", []byte{}, true},
	}
	for _, c := range cases {
		if got := IsEmpty(c.v); got != c.want {
			t.Errorf("%s: IsEmpty=%v want %v", c.name, got, c.want)
		}
	}
}

// TestMaskSecret 验证脱敏不泄露中间内容。
func TestMaskSecret(t *testing.T) {
	if got := MaskSecret(""); got != "（未设置）" {
		t.Errorf("空值: %q", got)
	}
	if got := MaskSecret("abcd"); got != "••••" {
		t.Errorf("短值应全遮: %q", got)
	}
	got := MaskSecret("sk-abcdefghijklmnop1234")
	if got != "sk-a…1234（长度 23）" {
		t.Errorf("长值: %q", got)
	}
}

// TestFormat 验证数字与金额格式化。
func TestFormat(t *testing.T) {
	if FormatInt(1234567) != "1,234,567" || FormatInt(-1000) != "-1,000" || FormatInt(12) != "12" {
		t.Error("FormatInt 错误")
	}
	tokCases := map[int64]string{0: "0", 999: "999", 1000: "1K", 1234: "1.2K", 1_500_000: "1.5M", 34941184: "34.94M", 2_000_000_000: "2B"}
	for in, want := range tokCases {
		if got := FormatTokens(in); got != want {
			t.Errorf("FormatTokens(%d)=%s want %s", in, got, want)
		}
	}
	if FormatMoney("CNY", 12.345) != "¥12.35" && FormatMoney("CNY", 12.345) != "¥12.34" {
		t.Errorf("CNY: %s", FormatMoney("CNY", 12.345))
	}
	if FormatMoney("USD", 0.0005) != "$0.0005" {
		t.Errorf("小额: %s", FormatMoney("USD", 0.0005))
	}
	if FormatMoney("EUR", 1) != "EUR 1.00" {
		t.Errorf("其他币种: %s", FormatMoney("EUR", 1))
	}
}

// TestFormatRelative 验证相对时间。
func TestFormatRelative(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.Local)
	cases := []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, "从未"},
		{now.Add(-10 * time.Second), "刚刚"},
		{now.Add(-5 * time.Minute), "5 分钟前"},
		{now.Add(-2 * time.Hour), "2 小时前"},
		{time.Date(2026, 10, 13, 9, 30, 0, 0, time.Local), "昨天 09:30"},
		{time.Date(2026, 10, 1, 9, 30, 0, 0, time.Local), "10-01 09:30"},
	}
	for _, c := range cases {
		if got := FormatRelative(c.t, now); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

// TestClampAndStartOfDay 验证区间限制与当日零点。
func TestClampAndStartOfDay(t *testing.T) {
	if Clamp(5, 0, 3) != 3 || Clamp(-1, 0, 3) != 0 || Clamp(2, 0, 3) != 2 {
		t.Error("Clamp 错误")
	}
	d := StartOfDay(time.Date(2026, 10, 14, 15, 4, 5, 6, time.Local))
	if d.Hour() != 0 || d.Minute() != 0 || d.Day() != 14 {
		t.Errorf("StartOfDay: %v", d)
	}
}

// TestFormatCountdown 倒计时精确到分钟，随距离远近切换单位。
func TestFormatCountdown(t *testing.T) {
	cases := map[time.Duration]string{
		-time.Minute:                  "0分",
		30 * time.Second:              "不到1分",
		42 * time.Minute:              "42分",
		12*time.Hour + 5*time.Minute:  "12时5分",
		27*time.Hour + 59*time.Minute: "1天3时",
		3*24*time.Hour + 23*time.Hour: "3天23时",
		time.Hour:                     "1时0分",
	}
	for d, want := range cases {
		if got := FormatCountdown(d); got != want {
			t.Errorf("FormatCountdown(%v) = %q，期望 %q", d, got, want)
		}
	}
}
