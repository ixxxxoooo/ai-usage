package transfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/provider/cursor"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/store"
)

var base = time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)

// mkEvents 生成 n 条不同的 Cursor 明细。
func mkEvents(n int) []model.CursorEvent {
	out := make([]model.CursorEvent, n)
	for i := range out {
		e := model.CursorEvent{
			Time: base.Add(time.Duration(i) * time.Minute), Model: fmt.Sprintf("model-%d", i%3),
			Kind: "USAGE_EVENT_KIND_INCLUDED_IN_PRO", Requests: 1 + float64(i%2), InputTokens: int64(100 + i),
			OutputTokens: int64(10 + i), CacheRead: int64(i), TotalCents: 1.5 + float64(i)/10, ChargedCents: 0.5,
			ConversationID: fmt.Sprintf("conv-%d", i),
		}
		e.Key = cursor.EventKey(e)
		out[i] = e
	}
	return out
}

// mkDaily 生成 n 天的 DeepSeek 账单（每天两个模型）。
func mkDaily(n int) []model.DeepSeekDaily {
	var out []model.DeepSeekDaily
	for i := 0; i < n; i++ {
		d := base.AddDate(0, 0, i).Format("2006-01-02")
		for _, m := range []string{"deepseek-chat", "deepseek-reasoner"} {
			out = append(out, model.DeepSeekDaily{Date: d, Model: m, Currency: "CNY", CacheHit: 1000, CacheMiss: 200, Output: 300, Requests: 5, Cost: 0.0123 + float64(i)/100})
		}
	}
	return out
}

// newStore 打开内存库。
func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fill 往存储里灌入一整套数据（明细、账单、快照、设置、最近状态）。
func fill(t *testing.T, st *store.Store, events, days int) {
	t.Helper()
	if _, err := st.UpsertCursorEvents(mkEvents(events)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDeepSeekDaily(mkDaily(days)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSnapshot("cursor", base, map[string]int{"used": 7}, 0); err != nil {
		t.Fatal(err)
	}
	s := model.DefaultSettings()
	s.RefreshMinutes, s.TrayDisplay = 15, model.TrayBoth
	if err := st.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	st.SetJSON("cursor_usage", model.CursorUsage{Used: 7, Limit: 500, FetchedAt: base})
	st.SetJSON("deepseek_balance", model.DeepSeekBalance{Currency: "CNY", Total: 12.5, FetchedAt: base})
}

// snapshotOf 把存储内容规整成可比较的文本。
func snapshotOf(t *testing.T, st *store.Store) string {
	t.Helper()
	ds, err := st.ReadDataset()
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range ds.CursorEvents {
		lines = append(lines, fmt.Sprintf("E|%s|%d|%s|%g|%d|%d|%d|%d|%g|%g|%s", e.Key, e.Time.UnixMilli(), e.Model, e.Requests, e.InputTokens, e.OutputTokens, e.CacheRead, e.CacheWrite, e.TotalCents, e.ChargedCents, e.ConversationID))
	}
	for _, d := range ds.DeepSeekDaily {
		lines = append(lines, fmt.Sprintf("D|%s|%s|%s|%d|%d|%d|%d|%g", d.Date, d.Model, d.Currency, d.CacheHit, d.CacheMiss, d.Output, d.Requests, d.Cost))
	}
	for _, s := range ds.Snapshots {
		lines = append(lines, fmt.Sprintf("S|%s|%d|%s", s.Provider, s.At.UnixMilli(), s.Payload))
	}
	b, _ := json.Marshal(ds.Settings)
	lines = append(lines, "SET|"+string(b))
	var ks []string
	for k, v := range ds.KV {
		var m map[string]any
		json.Unmarshal(v, &m)
		nb, _ := json.Marshal(m)
		ks = append(ks, "KV|"+k+"|"+string(nb))
	}
	sort.Strings(ks)
	lines = append(lines, ks...)
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// exportTo 导出到临时文件并返回路径。
func exportTo(t *testing.T, st *store.Store, opt ExportOptions) string {
	t.Helper()
	b, err := BuildBackup(st, opt)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "backup.json")
	if err := WriteFile(p, b); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRoundTrip 导出 → 解析 → 导入到全新库，内容必须逐项一致（合并与覆盖两种模式）。
func TestRoundTrip(t *testing.T) {
	src := newStore(t)
	fill(t, src, 120, 12)
	want := snapshotOf(t, src)
	path := exportTo(t, src, ExportOptions{AppVersion: "1.0.0"})

	for _, replace := range []bool{false, true} {
		b, pv, err := ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if pv.Events != 120 || pv.Daily != 24 || pv.Snapshots != 1 || pv.Legacy || pv.Version != SchemaVersion || pv.AppVersion != "1.0.0" || !pv.HasSettings {
			t.Errorf("预览: %+v", pv)
		}
		dst := newStore(t)
		res, err := Import(dst, b, ImportOptions{Replace: replace, ApplySettings: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.EventsAdded != 120 || res.EventsSkipped != 0 || res.DailyUpserted != 24 || res.SnapshotsAdded != 1 {
			t.Errorf("replace=%v 结果: %+v", replace, res.ImportResult)
		}
		if got := snapshotOf(t, dst); got != want {
			gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
			wm := map[string]bool{}
			for _, l := range wl {
				wm[l] = true
			}
			for _, l := range gl {
				if !wm[l] {
					t.Errorf("replace=%v 多出/不同: %s", replace, l)
				}
				delete(wm, l)
			}
			for l := range wm {
				t.Errorf("replace=%v 缺少: %s", replace, l)
			}
		}
	}
}

// TestImportTwiceNoDuplicates 重复导入同一份文件不产生重复数据。
func TestImportTwiceNoDuplicates(t *testing.T) {
	src := newStore(t)
	fill(t, src, 50, 5)
	path := exportTo(t, src, ExportOptions{})
	dst := newStore(t)
	for i := 0; i < 3; i++ {
		b, _, err := ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Import(dst, b, ImportOptions{ApplySettings: true})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && res.EventsAdded != 50 {
			t.Errorf("第一次应新增 50: %+v", res)
		}
		if i > 0 && (res.EventsAdded != 0 || res.EventsSkipped != 50 || res.SnapshotsAdded != 0) {
			t.Errorf("第 %d 次不应新增: %+v", i+1, res.ImportResult)
		}
	}
	if n, _ := dst.CursorEventCount(); n != 50 {
		t.Errorf("明细数 %d，应为 50", n)
	}
	if rows, _ := dst.DeepSeekDaily("", ""); len(rows) != 10 {
		t.Errorf("账单行 %d，应为 10（按日期+模型 upsert 不重复）", len(rows))
	}
}

// TestReimportIntoSameStore 把自己导出的备份再合并导入回同一个库：任何数据都不应变化（含快照、KV）。
func TestReimportIntoSameStore(t *testing.T) {
	st := newStore(t)
	fill(t, st, 40, 6)
	before := snapshotOf(t, st)
	path := exportTo(t, st, ExportOptions{})
	b, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Import(st, b, ImportOptions{ApplySettings: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.EventsAdded != 0 || res.SnapshotsAdded != 0 {
		t.Errorf("同库重导入不应新增: %+v", res.ImportResult)
	}
	if after := snapshotOf(t, st); after != before {
		t.Errorf("同库重导入后数据发生变化")
	}
}

// TestMergeOverlap 合并导入：本地有一部分，导入全部，最终是并集且无重复。
func TestMergeOverlap(t *testing.T) {
	src := newStore(t)
	fill(t, src, 100, 4)
	path := exportTo(t, src, ExportOptions{})
	dst := newStore(t)
	dst.UpsertCursorEvents(mkEvents(60)) // 本地已有前 60 条
	extra := mkEvents(100)[0]
	extra.Key, extra.Model = "local-only", "local-model"
	dst.UpsertCursorEvents([]model.CursorEvent{extra}) // 本地独有 1 条
	b, _, _ := ReadFile(path)
	res, err := Import(dst, b, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.EventsAdded != 40 || res.EventsSkipped != 60 {
		t.Errorf("%+v", res.ImportResult)
	}
	if n, _ := dst.CursorEventCount(); n != 101 {
		t.Errorf("并集应为 101，实际 %d", n)
	}
}

// TestReplaceKeepsSafetyBackup 覆盖导入：旧数据被替换，同时自动留下可恢复的安全备份。
func TestReplaceKeepsSafetyBackup(t *testing.T) {
	src := newStore(t)
	fill(t, src, 30, 3)
	path := exportTo(t, src, ExportOptions{})
	dst := newStore(t)
	dst.UpsertCursorEvents(func() []model.CursorEvent {
		e := mkEvents(5)
		for i := range e {
			e[i].Key = fmt.Sprintf("old-%d", i)
		}
		return e
	}())
	oldSnap := snapshotOf(t, dst)
	b, _, _ := ReadFile(path)
	dir := t.TempDir()
	res, err := Import(dst, b, ImportOptions{Replace: true, BackupDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.SafetyBackup == "" {
		t.Fatal("覆盖导入应生成安全备份")
	}
	if n, _ := dst.CursorEventCount(); n != 30 {
		t.Errorf("覆盖后应只有 30 条，实际 %d", n)
	}
	// 用安全备份可以把旧数据完整恢复。
	rb, _, err := ReadFile(res.SafetyBackup)
	if err != nil {
		t.Fatal(err)
	}
	restored := newStore(t)
	if _, err := Import(restored, rb, ImportOptions{Replace: true, ApplySettings: true}); err != nil {
		t.Fatal(err)
	}
	if got := snapshotOf(t, restored); !strings.Contains(got, "old-0") || strings.Count(got, "E|") != 5 {
		t.Errorf("安全备份无法恢复旧数据:\n%s\n旧数据:\n%s", got, oldSnap)
	}
}

// TestSafetyBackupPrune 安全备份只保留最近 5 份。
func TestSafetyBackupPrune(t *testing.T) {
	st := newStore(t)
	fill(t, st, 3, 1)
	dir := t.TempDir()
	for i := 0; i < 8; i++ {
		if _, err := SafetyBackup(st, dir, "1.0"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 5 {
		t.Errorf("应保留 5 份，实际 %d", len(ents))
	}
}

// parseStr 解析字符串形式的备份。
func parseStr(s string) (*Backup, Preview, error) { return Parse(strings.NewReader(s)) }

// TestBadFiles 各种坏文件都应被拒绝，并给出明确错误，且不会 panic。
func TestBadFiles(t *testing.T) {
	good := exportTo(t, func() *store.Store { st := newStore(t); fill(t, st, 5, 1); return st }(), ExportOptions{})
	goodBytes, _ := os.ReadFile(good)
	cases := []struct {
		name string
		data string
		want error
	}{
		{"空文件", "", ErrEmptyFile},
		{"只有空白", " \n\t ", ErrEmptyFile},
		{"不是 JSON", "hello world", ErrNotBackup},
		{"二进制", "\x00\x01\x02\xff\xfe", ErrNotBackup},
		{"JSON 数组", `[1,2,3]`, ErrNotBackup},
		{"别的应用 JSON", `{"name":"x","items":[1]}`, ErrNotBackup},
		{"别的 format", `{"format":"other","schemaVersion":1}`, ErrNotBackup},
		{"截断的备份", string(goodBytes[:len(goodBytes)/2]), ErrNotBackup},
		{"负版本号", `{"format":"ai-usage-backup","schemaVersion":-1}`, ErrNotBackup},
		{"空壳旧版", `{}`, ErrNotBackup},
		{"没有任何数据", `{"format":"ai-usage-backup","schemaVersion":1,"cursorEvents":[],"deepseekDaily":[]}`, ErrNothingNew},
	}
	for _, c := range cases {
		_, _, err := parseStr(c.data)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: 错误 %v，期望 %v", c.name, err, c.want)
		}
	}
	// 字段类型错误：版本号是字符串。
	if _, _, err := parseStr(`{"format":"ai-usage-backup","schemaVersion":"1"}`); err == nil {
		t.Error("类型错误应报错")
	}
	if _, _, err := parseStr(`{"format":"ai-usage-backup","schemaVersion":1,"cursorEvents":"oops"}`); err == nil {
		t.Error("cursorEvents 类型错误应报错")
	}
}

// TestFileProblems 文件系统层面的问题：不存在、是目录、超大。
func TestFileProblems(t *testing.T) {
	if _, _, err := ReadFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("不存在应报错")
	}
	if _, _, err := ReadFile(t.TempDir()); err == nil {
		t.Error("目录应报错")
	}
	old := maxFileSize
	maxFileSize = 64
	defer func() { maxFileSize = old }()
	_, _, err := parseStr(strings.Repeat(" ", 200) + `{"format":"ai-usage-backup"}`)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("超大文件应被拒绝: %v", err)
	}
}

// TestNewerVersionRejected 更新版本的备份要拒绝并提示升级。
func TestNewerVersionRejected(t *testing.T) {
	_, _, err := parseStr(fmt.Sprintf(`{"format":"ai-usage-backup","schemaVersion":%d}`, SchemaVersion+5))
	var nv ErrNewerVersion
	if !errors.As(err, &nv) || nv.Version != SchemaVersion+5 || !strings.Contains(err.Error(), "升级") {
		t.Errorf("%v", err)
	}
}

// TestLegacyFile 旧版（无版本标记、缺字段）备份能导入并自动补默认值。
func TestLegacyFile(t *testing.T) {
	legacy := `{
	  "exportedAt": "2026-01-05T10:00:00Z",
	  "settings": {"refreshMinutes": 0, "trayDisplay": "weird", "notifyCursorPercent": [90, 80, 80, 500, 0]},
	  "cursorEvents": [{"time":"2026-01-04T08:00:00Z","model":"gpt","requests":1}],
	  "deepseekDaily": [{"date":"2026-01-04","model":"deepseek-chat","currency":"CNY","cost":0.5,"requests":2}]
	}`
	b, pv, err := parseStr(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Legacy || b.SchemaVersion != SchemaVersion || b.Format != Format {
		t.Errorf("应迁移到当前版本: %+v %+v", pv, b)
	}
	d := model.DefaultSettings()
	if b.Settings.RefreshMinutes != d.RefreshMinutes || b.Settings.TrayDisplay != d.TrayDisplay {
		t.Errorf("设置没有被规范化: %+v", b.Settings)
	}
	if fmt.Sprint(b.Settings.NotifyCursorPercent) != "[80 90]" {
		t.Errorf("阈值应去重排序并过滤非法值: %v", b.Settings.NotifyCursorPercent)
	}
	if b.CursorEvents[0].Key == "" {
		t.Error("缺失的去重键应被补全")
	}
	dst := newStore(t)
	if _, err := Import(dst, b, ImportOptions{ApplySettings: true}); err != nil {
		t.Fatal(err)
	}
	if n, _ := dst.CursorEventCount(); n != 1 {
		t.Error("旧版明细应导入")
	}
	if !strings.Contains(pv.Summary(), "旧版备份") {
		t.Errorf("摘要应提示旧版: %s", pv.Summary())
	}
}

// TestInvalidRowsFiltered 无效记录被剔除并计数，有效记录照常导入。
func TestInvalidRowsFiltered(t *testing.T) {
	data := `{"format":"ai-usage-backup","schemaVersion":1,
	 "cursorEvents":[
	   {"key":"ok","time":"2026-10-01T00:00:00Z","model":"m","requests":1},
	   {"key":"zero-time","model":"m","requests":1},
	   {"key":"neg","time":"2026-10-01T00:00:00Z","model":"m","requests":-3},
	   {"key":"ancient","time":"1970-01-01T00:00:00Z","model":"m","requests":1},
	   {"key":"future","time":"2999-01-01T00:00:00Z","model":"m","requests":1},
	   {"key":"negtok","time":"2026-10-01T00:00:00Z","model":"m","requests":1,"inputTokens":-5}],
	 "deepseekDaily":[
	   {"date":"2026-10-01","model":"deepseek-chat","cost":1},
	   {"date":"2026-13-45","model":"deepseek-chat","cost":1},
	   {"date":"not-a-date","model":"deepseek-chat","cost":1},
	   {"date":"2026-10-02","model":"","cost":1},
	   {"date":"2026-10-03","model":"x","cost":-1},
	   {"date":"2026-10-04","model":"x","cost":1,"requests":-1}]}`
	b, pv, err := parseStr(data)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Events != 1 || pv.InvalidEvents != 5 || pv.Daily != 1 || pv.InvalidDaily != 5 {
		t.Errorf("%+v", pv)
	}
	if len(b.CursorEvents) != 1 || len(b.DeepSeekDaily) != 1 {
		t.Error("只应保留有效记录")
	}
	if !strings.Contains(pv.Summary(), "10 条无效记录将被忽略") {
		t.Errorf("%s", pv.Summary())
	}
}

// TestCredentials 默认导出不含凭据；显式勾选才写入；导入时同样要显式同意才恢复。
func TestCredentials(t *testing.T) {
	st := newStore(t)
	fill(t, st, 3, 1)
	sec := secret.NewMemory()
	sec.Set(secret.KeyCursorCookie, "COOKIE-SECRET-123")
	sec.Set(secret.KeyDeepSeekKey, "sk-SECRET-456")
	sec.Set(secret.KeyDeepSeekToken, "TOKEN-SECRET-789")

	plain := exportTo(t, st, ExportOptions{Secrets: sec}) // 没勾选
	raw, _ := os.ReadFile(plain)
	for _, s := range []string{"COOKIE-SECRET", "sk-SECRET", "TOKEN-SECRET", "credentials"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("默认备份不应包含 %q", s)
		}
	}
	if _, pv, _ := ReadFile(plain); pv.HasCredentials {
		t.Error("预览不应显示含凭据")
	}

	withCred := exportTo(t, st, ExportOptions{IncludeCredentials: true, Secrets: sec})
	info, _ := os.Stat(withCred)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("备份文件权限应为 0600: %v", info.Mode().Perm())
	}
	b, pv, err := ReadFile(withCred)
	if err != nil || !pv.HasCredentials || len(b.Credentials) != 3 {
		t.Fatalf("%v %+v", err, pv)
	}
	dst1 := secret.NewMemory()
	if _, err := Import(newStore(t), b, ImportOptions{Secrets: dst1}); err != nil { // 未同意恢复
		t.Fatal(err)
	}
	if _, err := dst1.Get(secret.KeyCursorCookie); !errors.Is(err, secret.ErrNotFound) {
		t.Error("未同意时不应恢复凭据")
	}
	dst2 := secret.NewMemory()
	res, err := Import(newStore(t), b, ImportOptions{RestoreCredentials: true, Secrets: dst2})
	if err != nil || res.CredentialsRestored != 3 {
		t.Fatalf("%v %+v", err, res)
	}
	if v, _ := dst2.Get(secret.KeyDeepSeekKey); v != "sk-SECRET-456" {
		t.Errorf("恢复值: %q", v)
	}
}

// TestSettingsApply 默认保留本机设置；勾选才采用备份设置，并经过规范化。
func TestSettingsApply(t *testing.T) {
	src := newStore(t)
	fill(t, src, 3, 1) // 备份里 RefreshMinutes=15, TrayBoth
	path := exportTo(t, src, ExportOptions{})
	dst := newStore(t)
	local := model.DefaultSettings()
	local.RefreshMinutes = 30
	dst.SaveSettings(local)
	b, _, _ := ReadFile(path)
	if _, err := Import(dst, b, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := dst.GetSettings(); got.RefreshMinutes != 30 {
		t.Errorf("默认应保留本机设置: %+v", got)
	}
	res, err := Import(dst, b, ImportOptions{ApplySettings: true})
	if err != nil || !res.SettingsReplac {
		t.Fatal(err, res)
	}
	if got, _ := dst.GetSettings(); got.RefreshMinutes != 15 || got.TrayDisplay != model.TrayBoth {
		t.Errorf("应采用备份设置: %+v", got)
	}
}

// TestMergeKeepsNewerState 合并导入不会用旧的「最近状态」覆盖本地更新的状态；覆盖模式则以备份为准。
func TestMergeKeepsNewerState(t *testing.T) {
	src := newStore(t)
	fill(t, src, 3, 1) // 备份里的 usage FetchedAt = base
	path := exportTo(t, src, ExportOptions{})
	dst := newStore(t)
	dst.SetJSON("cursor_usage", model.CursorUsage{Used: 99, Limit: 500, FetchedAt: base.Add(time.Hour)})
	b, _, _ := ReadFile(path)
	if _, err := Import(dst, b, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	var u model.CursorUsage
	dst.GetJSON("cursor_usage", &u)
	if u.Used != 99 {
		t.Errorf("本地更新的状态被旧备份覆盖: %+v", u)
	}
	if _, err := Import(dst, b, ImportOptions{Replace: true}); err != nil {
		t.Fatal(err)
	}
	dst.GetJSON("cursor_usage", &u)
	if u.Used != 7 {
		t.Errorf("覆盖模式应以备份为准: %+v", u)
	}
}

// TestAtomicWrite 原子写：覆盖已有文件、不遗留临时文件、目录不可写时报错且不破坏原文件。
func TestAtomicWrite(t *testing.T) {
	st := newStore(t)
	fill(t, st, 10, 1)
	dir := t.TempDir()
	p := filepath.Join(dir, "b.json")
	os.WriteFile(p, []byte("OLD"), 0o600)
	b, _ := BuildBackup(st, ExportOptions{})
	if err := WriteFile(p, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(p); err != nil {
		t.Errorf("覆盖后的文件应有效: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("不应遗留临时文件: %v", ents)
	}
	// 目标目录不可写
	ro := filepath.Join(t.TempDir(), "ro")
	os.Mkdir(ro, 0o500)
	defer os.Chmod(ro, 0o700)
	if os.Getuid() != 0 {
		if err := WriteFile(filepath.Join(ro, "x.json"), b); err == nil {
			t.Error("不可写目录应报错")
		}
	}
	// 目标是个目录
	if err := WriteFile(dir, b); err == nil {
		t.Error("目标是目录应报错")
	}
	ents, _ = os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("失败后遗留了临时文件 %s", e.Name())
		}
	}
}

// TestEmptyStoreExport 空库也能导出，并能再导入（不报错）。
func TestEmptyStoreExport(t *testing.T) {
	st := newStore(t)
	path := exportTo(t, st, ExportOptions{})
	b, pv, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Events != 0 || pv.Daily != 0 {
		t.Errorf("%+v", pv)
	}
	if _, err := Import(newStore(t), b, ImportOptions{}); err != nil {
		t.Error(err)
	}
	if _, err := Import(newStore(t), nil, ImportOptions{}); !errors.Is(err, ErrNothingNew) {
		t.Error("nil 备份应报 ErrNothingNew")
	}
}

// TestLargeRoundTrip 2 万条明细的往返：数量一致、耗时合理。
func TestLargeRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	src := newStore(t)
	fill(t, src, 20000, 30)
	start := time.Now()
	path := exportTo(t, src, ExportOptions{})
	b, pv, err := ReadFile(path)
	if err != nil || pv.Events != 20000 {
		t.Fatal(err, pv)
	}
	dst := newStore(t)
	if _, err := Import(dst, b, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := dst.CursorEventCount(); n != 20000 {
		t.Errorf("%d", n)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("耗时过长: %v", d)
	}
}

// TestBuildBackupJSONShape 备份是人类可读且字段稳定的 JSON（防止无意改名破坏兼容）。
func TestBuildBackupJSONShape(t *testing.T) {
	st := newStore(t)
	fill(t, st, 2, 1)
	b, _ := BuildBackup(st, ExportOptions{Now: base})
	raw, _ := json.Marshal(b)
	var m map[string]any
	json.Unmarshal(raw, &m)
	for _, k := range []string{"format", "schemaVersion", "exportedAt", "settings", "cursorEvents", "deepseekDaily", "snapshots", "kv"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少顶层字段 %q", k)
		}
	}
	if _, ok := m["credentials"]; ok {
		t.Error("不应有 credentials 字段")
	}
	if m["format"] != Format || int(m["schemaVersion"].(float64)) != SchemaVersion {
		t.Error("格式标记不对")
	}
}

// TestCSV 验证 CSV：BOM、表头、转义、公式注入防护、数值格式。
func TestCSV(t *testing.T) {
	evs := mkEvents(2)
	evs[0].Model = `=HYPERLINK("http://x")`
	evs[1].Model = "a,b\"c\nd"
	var buf bytes.Buffer
	if err := WriteCursorCSV(&buf, evs); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.HasPrefix(s, "\ufeff时间,模型,类型,次数") {
		t.Errorf("BOM/表头: %q", s[:40])
	}
	if !strings.Contains(s, `'=HYPERLINK`) || strings.Contains(s, ",=HYPERLINK") {
		t.Errorf("公式注入未防护: %s", s)
	}
	if !strings.Contains(s, `"a,b""c`+"\n"+`d"`) {
		t.Errorf("逗号/引号/换行未转义: %s", s)
	}
	if !strings.Contains(s, "套餐内") {
		t.Error("类型应转成中文")
	}
	buf.Reset()
	WriteCursorCSV(&buf, nil)
	if strings.Count(buf.String(), "\n") != 1 {
		t.Error("空列表只应有表头")
	}
	buf.Reset()
	rows := mkDaily(1)
	if err := WriteDeepSeekCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "deepseek-chat,CNY,5,1000,200,300,1500,0.0123") {
		t.Errorf("DeepSeek CSV: %s", buf.String())
	}
	p := filepath.Join(t.TempDir(), "a.csv")
	if err := SaveCursorCSV(p, evs); err != nil {
		t.Fatal(err)
	}
	if err := SaveDeepSeekCSV(filepath.Join(t.TempDir(), "b.csv"), rows); err != nil {
		t.Fatal(err)
	}
	if got := SuggestName("ai-usage", "json", "20261014"); got != "ai-usage-20261014.json" {
		t.Error(got)
	}
}

// TestErrNewerVersionMessage 版本错误文案包含版本号。
func TestErrNewerVersionMessage(t *testing.T) {
	if !strings.Contains((ErrNewerVersion{Version: 9}).Error(), "v9") {
		t.Error("应包含版本号")
	}
}
