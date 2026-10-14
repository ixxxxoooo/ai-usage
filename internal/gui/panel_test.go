package gui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/liwenjiao/ai-usage/internal/model"
	"github.com/liwenjiao/ai-usage/internal/secret"
	"github.com/liwenjiao/ai-usage/internal/service"
	"github.com/liwenjiao/ai-usage/internal/store"
)

// panelEnv 是面板测试的运行环境：真实 SQLite（临时目录）+ 内存凭据 + 注入的状态与文件对话框。
type panelEnv struct {
	a       *App
	st      *store.Store
	secrets *secret.Memory
	dir     string
	save    string // 下一次「保存对话框」返回的路径
	open    string // 下一次「打开对话框」返回的路径
	login   bool   // 模拟的开机自启状态
}

// seedEvents 生成覆盖多天、多模型的 Cursor 明细。
func seedEvents() []model.CursorEvent {
	mk := func(i int, at time.Time, m string, req float64, tokens int64, cents float64) model.CursorEvent {
		return model.CursorEvent{
			Key: fmt.Sprintf("k%d", i), Time: at, Model: m, Kind: "USAGE_EVENT_KIND_INCLUDED_IN_PRO",
			Requests: req, InputTokens: tokens, OutputTokens: tokens / 10, TotalCents: cents,
		}
	}
	return []model.CursorEvent{
		mk(1, testNow.Add(-5*time.Hour), "claude-4.5-sonnet", 1, 20000, 12),
		mk(2, testNow.Add(-4*time.Hour), "gpt-5", 2, 8000, 30),
		mk(3, testNow.Add(-26*time.Hour), "claude-4.5-sonnet", 1, 15000, 9),
		mk(4, testNow.Add(-5*24*time.Hour), "gpt-5", 1, 5000, 4),
		mk(5, testNow.Add(-20*24*time.Hour), "claude-4.5-sonnet", 1, 3000, 2), // 周期（9/16）之前
		mk(6, testNow.Add(-40*24*time.Hour), "gpt-5", 1, 1000, 1),
	}
}

// seedDaily 生成 DeepSeek 账单。
func seedDaily() []model.DeepSeekDaily {
	return []model.DeepSeekDaily{
		{Date: "2026-10-14", Model: "deepseek-chat", Currency: "CNY", CacheHit: 1000, CacheMiss: 200000, Output: 30000, Requests: 20, Cost: 0.55},
		{Date: "2026-10-13", Model: "deepseek-chat", Currency: "CNY", CacheHit: 5000, CacheMiss: 100000, Output: 10000, Requests: 10, Cost: 0.31},
		{Date: "2026-10-13", Model: "deepseek-reasoner", Currency: "CNY", CacheMiss: 50000, Output: 20000, Requests: 5, Cost: 0.9},
		{Date: "2026-09-01", Model: "deepseek-chat", Currency: "CNY", CacheMiss: 90000, Output: 9000, Requests: 7, Cost: 0.2},
	}
}

// newPanelEnv 创建面板测试环境，所有外部交互（对话框/异步）改为同步可控。
func newPanelEnv(t *testing.T) *panelEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.UpsertCursorEvents(seedEvents()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDeepSeekDaily(seedDaily()); err != nil {
		t.Fatal(err)
	}
	e := &panelEnv{st: st, secrets: secret.NewMemory(), dir: dir}
	svc, err := service.New(service.Config{Store: st, Secrets: e.secrets, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	a := New()
	a.SetClock(func() time.Time { return testNow })
	a.Attach(svc)
	a.stateFn = func() service.State { s := fullState(); s.Settings = svc.Settings(); return s }
	a.hk = hooks{
		saveFile: func(title, name string) (string, error) {
			if e.save == "" {
				return "", nil
			}
			return e.save, nil
		},
		openFile: func(title string) (string, error) { return e.open, nil },
		async:    func(a *App, work func() func()) { work()() },
		getLogin: func() bool { return e.login },
		setLogin: func(on bool) error { e.login = on; return nil },
	}
	e.a = a
	return e
}

// view 创建面板的测试视口并切到指定页面。
func (e *panelEnv) view(page string) *ui.Tester {
	e.a.ui.page = page
	if page == PageSettings {
		e.a.loadSettingsDraft()
	}
	h := PanelHeight
	if page == PageSettings {
		h = 2600 // 设置页较长，测试视口加高以免点击落在可视区域之外
	}
	return ui.NewTester(e.a.panelView, PanelWidth, h)
}

// TestPanelOverview 概览页展示两张卡片与图表标题。
func TestPanelOverview(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageOverview)
	for _, want := range []string{"概览", "Cursor", "359", "/ 500 次", "剩余 141 次", "DeepSeek", "¥12.61", "今日 Token", "查看使用明细", "查看账单", "近 14 天"} {
		if !hasPart(tt, want) {
			t.Errorf("概览缺少 %q；实际: %q", want, tt.Texts())
		}
	}
}

// hasPart 是测试辅助：界面上任一文本包含 s 即为真。
func hasPart(tt *ui.Tester, s string) bool {
	for _, x := range tt.Texts() {
		if strings.Contains(x, s) {
			return true
		}
	}
	return false
}

// TestPanelNavigation 点击侧边栏切换页面，点击卡片按钮也能跳转。
func TestPanelNavigation(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageOverview)
	if err := tt.Click("Cursor 明细"); err != nil {
		t.Fatal(err)
	}
	if e.a.ui.page != PageCursor || !tt.HasText("导出 CSV") {
		t.Errorf("应切到 Cursor 明细页: page=%s texts=%q", e.a.ui.page, tt.Texts())
	}
	if err := tt.Click("DeepSeek 账单"); err != nil {
		t.Fatal(err)
	}
	if e.a.ui.page != PageDeepSeek {
		t.Errorf("page=%s", e.a.ui.page)
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	if e.a.ui.page != PageSettings || !e.a.ui.setLoaded || !tt.HasText("测试连接") {
		t.Errorf("page=%s texts=%q", e.a.ui.page, tt.Texts())
	}
}

// TestPanelDarkAndScale 深色与 2 倍缩放下四个页面都能构建。
func TestPanelDarkAndScale(t *testing.T) {
	e := newPanelEnv(t)
	for _, p := range []string{PageOverview, PageCursor, PageDeepSeek, PageSettings} {
		tt := e.view(p)
		tt.SetDark(true)
		tt.SetScale(2)
		tt.Frame()
		if len(tt.Texts()) == 0 {
			t.Errorf("%s 页深色/2x 下没有内容", p)
		}
	}
}

// TestCursorViewFilter 范围、模型筛选与汇总。
func TestCursorViewFilter(t *testing.T) {
	e := newPanelEnv(t)
	data := e.a.Data()
	if len(data.Events) != 6 {
		t.Fatalf("加载明细 %d 条", len(data.Events))
	}
	sortTime := ui.SortOrder{Column: "time", Descending: true}
	cycle := time.Date(2026, 9, 16, 8, 0, 0, 0, time.Local)
	cases := []struct {
		name  string
		rng   int
		model string
		rows  int
		total float64
	}{
		{"今天", 0, allModels, 2, 3},
		{"近 7 天", 1, allModels, 4, 5},
		{"本周期", 2, allModels, 5, 6},
		{"全部", 3, allModels, 6, 7},
		{"本周期+gpt", 2, "gpt-5", 2, 3},
		{"全部+不存在的模型", 3, "nope", 0, 0},
	}
	for _, c := range cases {
		v := buildCursorView(data, cursorSince(c.rng, testNow, cycle), c.model, sortTime, testNow)
		if len(v.Rows) != c.rows || v.Total != c.total {
			t.Errorf("%s: rows=%d total=%v，期望 %d / %v", c.name, len(v.Rows), v.Total, c.rows, c.total)
		}
	}
	v := buildCursorView(data, cursorSince(0, testNow, cycle), allModels, sortTime, testNow)
	if v.Hours[10] != 1 || v.Hours[11] != 2 {
		t.Errorf("今日按小时: %v", v.Hours)
	}
}

// TestCursorViewSort 各列升降序，且排序稳定。
func TestCursorViewSort(t *testing.T) {
	e := newPanelEnv(t)
	data := e.a.Data()
	for _, col := range []string{"time", "model", "kind", "requests", "tokens", "cost"} {
		for _, desc := range []bool{false, true} {
			v := buildCursorView(data, time.Time{}, allModels, ui.SortOrder{Column: col, Descending: desc}, testNow)
			if len(v.Rows) != 6 {
				t.Fatalf("%s: %d 行", col, len(v.Rows))
			}
			for i := 1; i < len(v.Rows); i++ {
				a, b := v.Rows[i-1], v.Rows[i]
				var ok bool
				switch col {
				case "time":
					ok = a.Time.Before(b.Time) != desc || a.Time.Equal(b.Time)
				case "model":
					ok = (a.Model <= b.Model) != desc || a.Model == b.Model
				case "kind":
					ok = true
				case "requests":
					ok = (a.Requests <= b.Requests) != desc || a.Requests == b.Requests
				case "tokens":
					ok = (a.TotalTokens() <= b.TotalTokens()) != desc || a.TotalTokens() == b.TotalTokens()
				case "cost":
					ok = (a.TotalCents <= b.TotalCents) != desc || a.TotalCents == b.TotalCents
				}
				if !ok {
					t.Errorf("%s desc=%v 第 %d 行顺序错误", col, desc, i)
				}
			}
		}
	}
}

// TestCursorPageRender 页面显示汇总、模型名、行数，并随筛选变化。
func TestCursorPageRender(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageCursor)
	for _, want := range []string{"Cursor 明细", "claude-4.5-sonnet", "gpt-5", "共 5 条", "筛选范围内次数", "6 次"} {
		if !hasPart(tt, want) {
			t.Errorf("缺少 %q；实际: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("今天"); err != nil {
		t.Fatal(err)
	}
	if !hasPart(tt, "共 2 条") || !hasPart(tt, "今日按小时请求次数") {
		t.Errorf("切到「今天」后: %q", tt.Texts())
	}
}

// TestCursorPageEmpty 没有任何数据时显示引导而不是空表。
func TestCursorPageEmpty(t *testing.T) {
	e := newPanelEnv(t)
	if err := e.st.Wipe(); err != nil {
		t.Fatal(err)
	}
	e.a.reloadNow()
	e.a.stateFn = func() service.State {
		s := service.State{Settings: model.DefaultSettings()}
		s.Cursor.Status, s.Cursor.Message = service.StatusNoCreds, "未找到本机 Cursor 登录"
		return s
	}
	tt := e.view(PageCursor)
	if !hasPart(tt, "未找到本机 Cursor 登录") || hasPart(tt, "共 0 条") {
		t.Errorf("实际: %q", tt.Texts())
	}
}

// TestDeepSeekView 范围筛选、按模型汇总与排序。
func TestDeepSeekView(t *testing.T) {
	e := newPanelEnv(t)
	data := e.a.Data()
	byDate := ui.SortOrder{Column: "date", Descending: true}
	for rng, want := range []int{3, 3, 3, 4} { // 近7天/近30天/本月/全部
		v := buildDeepSeekView(data, deepseekSince(rng, testNow), byDate)
		if len(v.Rows) != want {
			t.Errorf("范围 %d: %d 行，期望 %d", rng, len(v.Rows), want)
		}
	}
	v := buildDeepSeekView(data, "", ui.SortOrder{Column: "cost", Descending: true})
	if v.Rows[0].Cost != 0.9 || v.Cost < 1.95 || v.Cost > 1.97 || v.Requests != 42 {
		t.Errorf("汇总: cost=%v req=%d first=%v", v.Cost, v.Requests, v.Rows[0])
	}
	if len(v.ByModel) != 2 || v.ByModel[0].Model != "deepseek-reasoner" && v.ByModel[0].Model != "deepseek-chat" {
		t.Errorf("按模型: %+v", v.ByModel)
	}
	for _, col := range []string{"date", "model", "requests", "hit", "miss", "output", "tokens", "cost"} {
		for _, desc := range []bool{false, true} {
			if got := buildDeepSeekView(data, "", ui.SortOrder{Column: col, Descending: desc}); len(got.Rows) != 4 {
				t.Errorf("%s 排序后行数 %d", col, len(got.Rows))
			}
		}
	}
}

// TestDeepSeekPageRender 页面显示余额、花费汇总与表格内容。
func TestDeepSeekPageRender(t *testing.T) {
	e := newPanelEnv(t)
	tt := e.view(PageDeepSeek)
	for _, want := range []string{"DeepSeek 账单", "¥12.61", "范围内花费", "deepseek-chat", "deepseek-reasoner", "2026-10-14", "共 3 行"} {
		if !hasPart(tt, want) {
			t.Errorf("缺少 %q；实际: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("全部"); err != nil {
		t.Fatal(err)
	}
	if !hasPart(tt, "共 4 行") {
		t.Errorf("切到「全部」后: %q", tt.Texts())
	}
}

// TestDeepSeekPageNeedToken 只有余额、没有账单时给出明确指引。
func TestDeepSeekPageNeedToken(t *testing.T) {
	e := newPanelEnv(t)
	if err := e.st.Wipe(); err != nil {
		t.Fatal(err)
	}
	e.a.reloadNow()
	e.a.stateFn = func() service.State {
		s := fullState()
		s.DeepSeek.HasBill = false
		return s
	}
	tt := e.view(PageDeepSeek)
	if !hasPart(tt, "只配置了 API Key") || !tt.HasText("去设置") || !hasPart(tt, "¥12.61") {
		t.Errorf("实际: %q", tt.Texts())
	}
}
