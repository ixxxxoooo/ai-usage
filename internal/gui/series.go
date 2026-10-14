package gui

import (
	"fmt"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/util"
)

// dayKeys 返回以 now 所在日结尾的连续 n 天的日期键（YYYY-MM-DD，升序）。
//
// 参数 now：结束日；n：天数。
// 返回值：日期键切片。
func dayKeys(now time.Time, n int) []string {
	out := make([]string, n)
	d := util.StartOfDay(now)
	for i := 0; i < n; i++ {
		out[n-1-i] = d.AddDate(0, 0, -i).Format("2006-01-02")
	}
	return out
}

// dayLabel 把 YYYY-MM-DD 转成 "10/14"。
func dayLabel(key string) string {
	if t, err := time.Parse("2006-01-02", key); err == nil {
		return fmt.Sprintf("%d/%d", int(t.Month()), t.Day())
	}
	return key
}

// dayLong 把 YYYY-MM-DD 转成 "10月14日"。
func dayLong(key string) string {
	if t, err := time.Parse("2006-01-02", key); err == nil {
		return fmt.Sprintf("%d月%d日", int(t.Month()), t.Day())
	}
	return key
}

// CursorBars 把按日汇总补齐成连续 n 天的柱状数据（没有记录的日子为 0）。
//
// 参数 days：按日汇总；now：当前时间；n：天数。
// 返回值：柱状数据。
func CursorBars(days []model.CursorDayStat, now time.Time, n int) []barDatum {
	m := map[string]model.CursorDayStat{}
	for _, d := range days {
		m[d.Date] = d
	}
	var out []barDatum
	for _, k := range dayKeys(now, n) {
		d := m[k]
		out = append(out, barDatum{
			Label: dayLabel(k), Value: d.Requests,
			Tip: fmt.Sprintf("%s：%s 次 · %d 条", dayLong(k), trimFloat(d.Requests), d.Events),
		})
	}
	return out
}

// DeepSeekBars 把按日汇总补齐成连续 n 天的柱状数据。
//
// 参数 days：按日汇总；currency：币种；now：当前时间；n：天数；metric：0 花费 / 1 Token。
// 返回值：柱状数据。
func DeepSeekBars(days []model.DeepSeekDayStat, currency string, now time.Time, n, metric int) []barDatum {
	m := map[string]model.DeepSeekDayStat{}
	for _, d := range days {
		m[d.Date] = d
	}
	var out []barDatum
	for _, k := range dayKeys(now, n) {
		d := m[k]
		b := barDatum{Label: dayLabel(k)}
		if metric == 1 {
			b.Value = float64(d.Tokens)
			b.Tip = fmt.Sprintf("%s：%s Token · %s 次请求", dayLong(k), util.FormatTokens(d.Tokens), util.FormatInt(d.Requests))
		} else {
			b.Value = d.Cost
			b.Tip = fmt.Sprintf("%s：%s · %s Token", dayLong(k), util.FormatMoney(currency, d.Cost), util.FormatTokens(d.Tokens))
		}
		out = append(out, b)
	}
	return out
}

// trimFloat 把浮点数格式化成去掉多余 0 的文本（1 → "1"，1.5 → "1.5"）。
func trimFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}

// currencyOf 返回账单币种；没有数据时默认人民币。
func currencyOf(b model.DeepSeekBalance, rows []model.DeepSeekDaily) string {
	if b.Currency != "" {
		return b.Currency
	}
	for _, r := range rows {
		if r.Currency != "" {
			return r.Currency
		}
	}
	return "CNY"
}
