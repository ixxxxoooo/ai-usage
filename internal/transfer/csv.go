package transfer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/liwenjiao/ai-usage/internal/model"
)

// csvSafe 防御 CSV 公式注入：以 = + - @ 制表符/回车开头的文本前面加单引号。
//
// 参数 s：原始文本。
// 返回值：安全文本。
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// writeCSV 写入带 UTF-8 BOM 的 CSV（Excel 直接打开不乱码）。
func writeCSV(w io.Writer, header []string, rows [][]string) error {
	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// f 把浮点数格式化成最短的十进制文本。
func f(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// WriteCursorCSV 导出 Cursor 明细为 CSV。
//
// 参数 w：输出；evs：明细（按给定顺序输出）。
// 返回值：错误。
func WriteCursorCSV(w io.Writer, evs []model.CursorEvent) error {
	header := []string{"时间", "模型", "类型", "次数", "输入Token", "输出Token", "缓存写入Token", "缓存读取Token", "API估值(美元)", "实际扣费(美元)", "会话ID"}
	rows := make([][]string, 0, len(evs))
	for _, e := range evs {
		rows = append(rows, []string{
			e.Time.Local().Format("2006-01-02 15:04:05"), csvSafe(e.Model), e.KindLabel(), f(e.Requests),
			strconv.FormatInt(e.InputTokens, 10), strconv.FormatInt(e.OutputTokens, 10),
			strconv.FormatInt(e.CacheWrite, 10), strconv.FormatInt(e.CacheRead, 10),
			f(e.TotalCents / 100), f(e.ChargedCents / 100), csvSafe(e.ConversationID),
		})
	}
	return writeCSV(w, header, rows)
}

// WriteDeepSeekCSV 导出 DeepSeek 账单为 CSV。
//
// 参数 w：输出；rows：账单行。
// 返回值：错误。
func WriteDeepSeekCSV(w io.Writer, rows []model.DeepSeekDaily) error {
	header := []string{"日期", "模型", "币种", "请求数", "缓存命中Token", "缓存未命中Token", "输出Token", "总Token", "费用"}
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{
			r.Date, csvSafe(r.Model), r.Currency, strconv.FormatInt(r.Requests, 10),
			strconv.FormatInt(r.CacheHit, 10), strconv.FormatInt(r.CacheMiss, 10), strconv.FormatInt(r.Output, 10),
			strconv.FormatInt(r.Tokens(), 10), f(r.Cost),
		})
	}
	return writeCSV(w, header, out)
}

// SaveCursorCSV 把明细原子写入 CSV 文件。
func SaveCursorCSV(path string, evs []model.CursorEvent) error {
	var sb strings.Builder
	if err := WriteCursorCSV(&sb, evs); err != nil {
		return err
	}
	return atomicWrite(path, []byte(sb.String()), 0o644)
}

// SaveDeepSeekCSV 把账单原子写入 CSV 文件。
func SaveDeepSeekCSV(path string, rows []model.DeepSeekDaily) error {
	var sb strings.Builder
	if err := WriteDeepSeekCSV(&sb, rows); err != nil {
		return err
	}
	return atomicWrite(path, []byte(sb.String()), 0o644)
}

// SuggestName 生成默认文件名，如 ai-usage-backup-20261014.json。
//
// 参数 prefix：前缀；ext：扩展名（不含点）；day：日期文本 yyyyMMdd。
func SuggestName(prefix, ext, day string) string {
	return fmt.Sprintf("%s-%s.%s", prefix, day, ext)
}
