package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liwenjiao/ai-usage/internal/model"
	"github.com/liwenjiao/ai-usage/internal/secret"
	"github.com/liwenjiao/ai-usage/internal/store"
)

// counts 返回当前库里 Cursor 明细与 DeepSeek 账单的条数。
func (e *panelEnv) counts(t *testing.T) (int, int) {
	t.Helper()
	n, err := e.st.CursorEventCount()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.st.DeepSeekDaily("", "")
	if err != nil {
		t.Fatal(err)
	}
	return n, len(rows)
}

// exportTo 导出备份到 dir 下的指定文件，返回路径。
func (e *panelEnv) exportTo(t *testing.T, name string, withCreds bool) string {
	t.Helper()
	e.save = filepath.Join(e.dir, name)
	e.a.exportBackup(withCreds)
	if e.a.ui.dataMsgBad || e.a.ui.busy {
		t.Fatalf("导出失败: %s busy=%v", e.a.ui.dataMsg, e.a.ui.busy)
	}
	return e.save
}

// importFrom 选择并导入备份。
func (e *panelEnv) importFrom(path string, mode int, setting, creds bool) {
	e.open = path
	e.a.pickImportFile()
	e.a.ui.importMode, e.a.ui.importSet, e.a.ui.importCreds = mode, setting, creds
	if e.a.ui.importBackup != nil {
		e.a.doImport()
	}
}

// TestSettingsApplyAndPersist 修改设置立即生效并写入数据库；重新载入后保持。
func TestSettingsApplyAndPersist(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageSettings)
	if err := tt.Click("两者"); err != nil {
		t.Fatal(err)
	}
	if got := e.a.svc.Settings().TrayDisplay; got != model.TrayBoth {
		t.Fatalf("托盘显示 %q", got)
	}
	e.a.ui.interval = "15 分钟"
	e.a.ui.notifyPct[90] = false
	e.a.ui.notifyPct[50] = true
	e.a.ui.dsBelow = 8
	tt.Frame()
	got, err := e.st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.TrayDisplay != model.TrayBoth || got.RefreshMinutes != 15 || got.NotifyDeepSeekBelow != 8 || fmt.Sprint(got.NotifyCursorPercent) != "[50 80]" {
		t.Errorf("数据库里的设置不对: %+v", got)
	}
	// 切换页面再回来：草稿应从服务重新载入，值保持。
	e.a.ui.page = PageOverview
	tt = e.view(PageSettings)
	if e.a.ui.trayMode != 3 || e.a.ui.interval != "15 分钟" || !e.a.ui.notifyPct[50] || e.a.ui.notifyPct[90] {
		t.Errorf("重新载入后的草稿: tray=%d interval=%s pct=%v", e.a.ui.trayMode, e.a.ui.interval, e.a.ui.notifyPct)
	}
	// 重复帧不应重复写库：lastApplied 与草稿一致。
	before := e.a.ui.lastApplied
	tt.Frame()
	if fmt.Sprint(before) != fmt.Sprint(e.a.ui.lastApplied) {
		t.Error("无改动时不应重复提交")
	}
}

// TestSettingsLoginItem 开机自启开关调用系统接口，失败时回退并提示。
func TestSettingsLoginItem(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageSettings)
	e.a.ui.loginItem = true
	e.a.setLoginItem(true)
	if !e.login {
		t.Error("应已开启开机自启")
	}
	e.a.hk.setLogin = func(on bool) error { return fmt.Errorf("系统拒绝") }
	e.a.ui.loginItem = false
	e.a.setLoginItem(false)
	if !e.a.ui.loginItem || e.a.ui.toast == "" {
		t.Errorf("失败应回退到真实状态并提示: login=%v toast=%q", e.a.ui.loginItem, e.a.ui.toast)
	}
	tt.Frame()
}

// TestCredentialsSaveMaskAndClear 凭据只进钥匙串（内存实现），界面只显示脱敏文本，库文件里没有明文。
func TestCredentialsSaveMaskAndClear(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageSettings)
	const key = "sk-live-abcdefghijklmnopqrstuvwxyz0123456789"
	e.a.saveCredential(secret.KeyDeepSeekKey, "  "+key+"  ")
	if v, err := e.secrets.Get(secret.KeyDeepSeekKey); err != nil || v != key {
		t.Fatalf("钥匙串里的值 %q %v（应去掉首尾空白）", v, err)
	}
	if !e.a.ui.hasKey || e.a.ui.maskKey == "" || e.a.ui.maskKey == key {
		t.Fatalf("mask=%q", e.a.ui.maskKey)
	}
	tt.Frame()
	if !hasPart(tt, "已保存到钥匙串") || hasPart(tt, key) {
		t.Errorf("界面应只显示脱敏文本: %q", tt.Texts())
	}
	// 数据目录里任何文件都不应出现明文。
	_ = filepath.Walk(e.dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), key) {
				t.Errorf("明文出现在文件 %s 中", p)
			}
		}
		return nil
	})
	e.a.saveCredential(secret.KeyDeepSeekKey, "")
	if _, err := e.secrets.Get(secret.KeyDeepSeekKey); err == nil || e.a.ui.hasKey {
		t.Error("清除后应不存在")
	}
}

// TestCredentialsFieldsShowByMode 切到手动方式才显示 Cookie / token 输入框。
func TestCredentialsFieldsShowByMode(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageSettings)
	if hasPart(tt, "WorkosCursorSessionToken") {
		t.Error("自动模式不应显示 Cookie 输入说明")
	}
	if err := tt.Click("手动粘贴 Cookie"); err != nil {
		t.Fatal(err)
	}
	if !hasPart(tt, "WorkosCursorSessionToken") || e.a.svc.Settings().CursorAuthMode != model.AuthManual {
		t.Errorf("切到手动后应显示输入并保存模式: %q", tt.Texts())
	}
	if err := tt.Click("手动粘贴 token"); err != nil {
		t.Fatal(err)
	}
	if !hasPart(tt, "userToken") || e.a.svc.Settings().DeepSeekAuthMode != model.AuthManual {
		t.Errorf("DeepSeek 手动模式: %q", tt.Texts())
	}
}

// TestExportImportRoundTrip 导出 → 清空 → 导入，数据逐条一致；重复导入不产生重复。
func TestExportImportRoundTrip(t *testing.T) {
	e := newPanelEnv(t)
	wantEv, wantDs := e.counts(t)
	before, _ := e.st.ReadDataset()
	path := e.exportTo(t, "full.json", false)

	raw, _ := os.ReadFile(path)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("备份不是合法 JSON: %v", err)
	}
	if _, has := probe["credentials"]; has {
		t.Error("默认导出不应包含凭据")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 && fi.Mode().Perm() != 0o644 {
		t.Errorf("文件权限 %v", fi.Mode())
	}

	if err := e.st.Wipe(); err != nil {
		t.Fatal(err)
	}
	if n, d := e.counts(t); n != 0 || d != 0 {
		t.Fatalf("清空后仍有数据 %d/%d", n, d)
	}
	e.importFrom(path, 0, false, false)
	if e.a.ui.dataMsgBad {
		t.Fatalf("导入失败: %s", e.a.ui.dataMsg)
	}
	if n, d := e.counts(t); n != wantEv || d != wantDs {
		t.Fatalf("导入后 %d/%d，期望 %d/%d", n, d, wantEv, wantDs)
	}
	after, _ := e.st.ReadDataset()
	if fmt.Sprint(after.CursorEvents) != fmt.Sprint(before.CursorEvents) || fmt.Sprint(after.DeepSeekDaily) != fmt.Sprint(before.DeepSeekDaily) {
		t.Error("往返后明细内容不一致")
	}
	// 再导入一次（合并）：不重复。
	e.importFrom(path, 0, false, false)
	if n, d := e.counts(t); n != wantEv || d != wantDs {
		t.Errorf("重复导入产生了重复数据 %d/%d", n, d)
	}
	if !strings.Contains(e.a.ui.dataMsg, "跳过重复") {
		t.Errorf("应提示跳过重复: %q", e.a.ui.dataMsg)
	}
	// 界面立刻反映导入后的数据。
	if got := len(e.a.Data().Events); got != wantEv {
		t.Errorf("界面数据没有刷新: %d", got)
	}
}

// TestImportMergeAndReplace 合并保留本机独有数据；覆盖以备份为准并留安全备份。
func TestImportMergeAndReplace(t *testing.T) {
	e := newPanelEnv(t)
	path := e.exportTo(t, "base.json", false)
	extra := model.CursorEvent{Key: "local-only", Time: testNow, Model: "local-model", Requests: 1}
	if _, err := e.st.UpsertCursorEvents([]model.CursorEvent{extra}); err != nil {
		t.Fatal(err)
	}
	e.importFrom(path, 0, false, false)
	if n, _ := e.counts(t); n != 7 {
		t.Errorf("合并应保留本机独有记录，得到 %d", n)
	}
	e.importFrom(path, 1, false, false)
	if n, _ := e.counts(t); n != 6 {
		t.Errorf("覆盖应以备份为准，得到 %d", n)
	}
	if !strings.Contains(e.a.ui.dataMsg, "自动备份") {
		t.Errorf("覆盖应提示安全备份: %q", e.a.ui.dataMsg)
	}
	files, _ := filepath.Glob(filepath.Join(e.dir, "backups", "*.json"))
	if len(files) == 0 {
		t.Error("覆盖导入前应留有安全备份文件")
	}
}

// TestImportBadFilesKeepData 各种坏文件：给出明确错误，本机数据保持不变。
func TestImportBadFilesKeepData(t *testing.T) {
	e := newPanelEnv(t)
	wantEv, wantDs := e.counts(t)
	cases := map[string]string{
		"empty.json":   "",
		"garbage.json": "not json at all",
		"array.json":   "[1,2,3]",
		"other.json":   `{"format":"someone-else"}`,
		"newer.json":   `{"format":"ai-usage-backup","schemaVersion":99}`,
		"nodata.json":  `{"format":"ai-usage-backup","schemaVersion":1,"cursorEvents":[],"deepseekDaily":[]}`,
		"trunc.json":   `{"format":"ai-usage-backup","schemaVersion":1,"cursorEvents":[{"key":"a"`,
	}
	for name, content := range cases {
		p := filepath.Join(e.dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		e.a.ui.dataMsg, e.a.ui.dataMsgBad = "", false
		e.importFrom(p, 1, true, true) // 即使选了覆盖也不应动数据
		if !e.a.ui.dataMsgBad || e.a.ui.dataMsg == "" || e.a.ui.importBackup != nil {
			t.Errorf("%s: 应报错且不进入导入流程: bad=%v msg=%q", name, e.a.ui.dataMsgBad, e.a.ui.dataMsg)
		}
		if n, d := e.counts(t); n != wantEv || d != wantDs {
			t.Errorf("%s: 本机数据被改动 %d/%d", name, n, d)
		}
	}
	// 文件不存在。
	e.importFrom(filepath.Join(e.dir, "missing.json"), 0, false, false)
	if !e.a.ui.dataMsgBad {
		t.Error("不存在的文件应报错")
	}
	// 用户取消选择：没有任何变化。
	e.a.ui.dataMsg, e.a.ui.dataMsgBad = "", false
	e.importFrom("", 0, false, false)
	if e.a.ui.dataMsg != "" || e.a.ui.importBackup != nil {
		t.Errorf("取消选择不应有提示: %q", e.a.ui.dataMsg)
	}
}

// TestImportOldVersionAndNewerMessage 旧版文件可导入；新版给出升级提示。
func TestImportOldVersionAndNewerMessage(t *testing.T) {
	e := newPanelEnv(t)
	legacy := `{"exportedAt":"2026-01-05T10:00:00Z",
	 "cursorEvents":[{"time":"2026-01-04T08:00:00Z","model":"legacy-model","requests":1}],
	 "deepseekDaily":[{"date":"2026-01-04","model":"deepseek-chat","currency":"CNY","cost":0.5,"requests":2}]}`
	p := filepath.Join(e.dir, "legacy.json")
	_ = os.WriteFile(p, []byte(legacy), 0o600)
	e.open = p
	e.a.pickImportFile()
	if e.a.ui.importBackup == nil || !e.a.ui.importPrev.Legacy || !strings.Contains(e.a.ui.importInfo, "旧版") {
		t.Fatalf("旧版应可预览: %q", e.a.ui.dataMsg)
	}
	e.a.doImport()
	if n, d := e.counts(t); n != 7 || d != 5 {
		t.Errorf("旧版导入后 %d/%d", n, d)
	}
	nv := filepath.Join(e.dir, "newer.json")
	_ = os.WriteFile(nv, []byte(`{"format":"ai-usage-backup","schemaVersion":42}`), 0o600)
	e.importFrom(nv, 0, false, false)
	if !strings.Contains(e.a.ui.dataMsg, "升级") {
		t.Errorf("应提示升级应用: %q", e.a.ui.dataMsg)
	}
}

// TestCredentialsInBackup 默认不含凭据；勾选才导出；导入时勾选才恢复。
func TestCredentialsInBackup(t *testing.T) {
	e := newPanelEnv(t)
	e.a.saveCredential(secret.KeyDeepSeekKey, "sk-secret-123456789")
	plain := e.exportTo(t, "plain.json", false)
	if b, _ := os.ReadFile(plain); strings.Contains(string(b), "sk-secret-123456789") {
		t.Fatal("默认导出泄露了凭据")
	}
	withCreds := e.exportTo(t, "creds.json", true)
	if b, _ := os.ReadFile(withCreds); !strings.Contains(string(b), "sk-secret-123456789") {
		t.Fatal("勾选后应包含凭据")
	}
	_ = e.secrets.Delete(secret.KeyDeepSeekKey)
	e.importFrom(withCreds, 0, false, false) // 文件里有凭据但没勾选恢复
	if _, err := e.secrets.Get(secret.KeyDeepSeekKey); err == nil {
		t.Error("未勾选时不应恢复凭据")
	}
	e.importFrom(withCreds, 0, false, true)
	if v, err := e.secrets.Get(secret.KeyDeepSeekKey); err != nil || v != "sk-secret-123456789" {
		t.Errorf("勾选后应恢复凭据: %q %v", v, err)
	}
}

// TestImportApplySettings 默认保留本机设置；勾选才采用备份里的设置。
func TestImportApplySettings(t *testing.T) {
	e := newPanelEnv(t)
	s := e.a.svc.Settings()
	s.RefreshMinutes = 30
	if err := e.a.svc.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	path := e.exportTo(t, "set.json", false)
	s.RefreshMinutes = 10
	_ = e.a.svc.UpdateSettings(s)
	e.importFrom(path, 0, false, false)
	if got := e.a.svc.Settings().RefreshMinutes; got != 10 {
		t.Errorf("默认应保留本机设置，得到 %d", got)
	}
	e.importFrom(path, 0, true, false)
	if got := e.a.svc.Settings().RefreshMinutes; got != 30 {
		t.Errorf("勾选后应采用备份设置，得到 %d", got)
	}
	if e.a.ui.lastApplied.RefreshMinutes != 30 || e.a.ui.interval != "30 分钟" {
		t.Errorf("设置页草稿应同步: %v %q", e.a.ui.lastApplied.RefreshMinutes, e.a.ui.interval)
	}
}

// TestExportCancelAndErrors 取消保存不报错；写入失败有明确提示且不残留。
func TestExportCancelAndErrors(t *testing.T) {
	e := newPanelEnv(t)
	e.save = ""
	e.a.exportBackup(false)
	if e.a.ui.dataMsg != "" || e.a.ui.busy {
		t.Errorf("取消保存应静默: %q busy=%v", e.a.ui.dataMsg, e.a.ui.busy)
	}
	blocker := filepath.Join(e.dir, "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	e.save = filepath.Join(blocker, "backup.json") // 父级是文件，必然写不进去
	e.a.exportBackup(false)
	if !e.a.ui.dataMsgBad || e.a.ui.busy {
		t.Errorf("写入失败应提示: %q", e.a.ui.dataMsg)
	}
}

// TestImportViaUI 通过界面点击完成：选择文件 → 预览 → 覆盖需确认 → 导入。
func TestImportViaUI(t *testing.T) {
	e := newPanelEnv(t)
	path := e.exportTo(t, "ui.json", false)
	_ = e.st.Wipe()
	e.a.reloadNow()
	e.open = path
	tt := e.view(PageSettings)
	if err := tt.Click("选择备份文件…"); err != nil {
		t.Fatal(err)
	}
	if !hasPart(tt, "Cursor 明细 6 条") || !hasPart(tt, "开始导入") {
		t.Fatalf("应显示预览: %q", tt.Texts())
	}
	if err := tt.Click("覆盖（以备份为准）"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("开始导入"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.counts(t); n != 0 {
		t.Fatal("覆盖导入必须先确认")
	}
	if !hasPart(tt, "覆盖本机数据？") {
		t.Fatalf("应出现确认框: %q", tt.Texts())
	}
	if err := tt.Click("覆盖导入"); err != nil {
		t.Fatal(err)
	}
	if n, d := e.counts(t); n != 6 || d != 4 {
		t.Errorf("确认后应导入 %d/%d", n, d)
	}
	if e.a.ui.importBackup != nil {
		t.Error("导入后应退出导入流程")
	}
}

// TestWipeViaUI 清除本地数据：需确认；清除前自动备份；凭据保留。
func TestWipeViaUI(t *testing.T) {
	e := newPanelEnv(t)
	e.a.saveCredential(secret.KeyDeepSeekKey, "sk-keep-me-123456")
	tt := e.view(PageSettings)
	if err := tt.Click("清除本地数据…"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.counts(t); n == 0 {
		t.Fatal("确认前不应清除")
	}
	if !hasPart(tt, "清除全部本地数据？") {
		t.Fatalf("应出现确认框: %q", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.counts(t); n == 0 {
		t.Fatal("取消不应清除")
	}
	_ = tt.Click("清除本地数据…")
	if err := tt.Click("清除"); err != nil {
		t.Fatal(err)
	}
	if n, d := e.counts(t); n != 0 || d != 0 {
		t.Errorf("清除后仍有 %d/%d", n, d)
	}
	if v, _ := e.secrets.Get(secret.KeyDeepSeekKey); v != "sk-keep-me-123456" {
		t.Error("凭据应保留")
	}
	if files, _ := filepath.Glob(filepath.Join(e.dir, "backups", "*.json")); len(files) == 0 {
		t.Error("清除前应自动备份")
	}
	// 清除后能用自动备份恢复。
	files, _ := filepath.Glob(filepath.Join(e.dir, "backups", "*.json"))
	e.importFrom(files[0], 0, false, false)
	if n, d := e.counts(t); n != 6 || d != 4 {
		t.Errorf("用自动备份恢复后 %d/%d", n, d)
	}
}

// TestCSVExports 导出 CSV：范围内数据、含表头，取消不写文件。
func TestCSVExports(t *testing.T) {
	e := newPanelEnv(t)
	view := e.a.cursorViewCached(e.a.state(), e.a.Data(), testNow)
	e.save = filepath.Join(e.dir, "cursor.csv")
	e.a.exportCursorCSV(view.Rows)
	b, err := os.ReadFile(e.save)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) != len(view.Rows)+1 {
		t.Errorf("CSV 行数 %d，期望 %d", len(lines), len(view.Rows)+1)
	}
	dv := e.a.deepseekViewCached(e.a.Data(), testNow)
	e.save = filepath.Join(e.dir, "ds.csv")
	e.a.exportDeepSeekCSV(dv.Rows)
	if b, _ := os.ReadFile(e.save); !strings.Contains(string(b), "deepseek-reasoner") {
		t.Error("DeepSeek CSV 内容不对")
	}
	e.save = ""
	cancelled := filepath.Join(e.dir, "never.csv")
	e.a.exportCursorCSV(view.Rows)
	if _, err := os.Stat(cancelled); err == nil {
		t.Error("取消不应写文件")
	}
	e.a.exportCursorCSV(nil) // 空数据：不弹窗不报错
}

// TestPersistAcrossRestart 关闭并重新打开数据库：明细、设置、最近状态都还在。
func TestPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.UpsertCursorEvents(seedEvents())
	_ = st.UpsertDeepSeekDaily(seedDaily())
	s := model.DefaultSettings()
	s.RefreshMinutes, s.TrayDisplay = 30, model.TrayBoth
	_ = st.SaveSettings(s)
	_ = st.SetJSON("cursor_usage", model.CursorUsage{Used: 359, Limit: 500})
	_ = st.Close()

	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if n, _ := st2.CursorEventCount(); n != 6 {
		t.Errorf("重启后明细 %d 条", n)
	}
	if rows, _ := st2.DeepSeekDaily("", ""); len(rows) != 4 {
		t.Errorf("重启后账单 %d 行", len(rows))
	}
	got, _ := st2.GetSettings()
	if got.RefreshMinutes != 30 || got.TrayDisplay != model.TrayBoth {
		t.Errorf("重启后设置 %+v", got)
	}
	var u model.CursorUsage
	if ok, _ := st2.GetJSON("cursor_usage", &u); !ok || u.Used != 359 {
		t.Errorf("重启后最近用量 %+v", u)
	}
}
