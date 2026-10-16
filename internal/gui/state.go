package gui

import (
	"github.com/egoist/mygo/ui"
	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/transfer"
)

// allModels 是模型筛选里「不限」的显示名。
const allModels = "全部模型"

// uiState 保存界面本身的状态（当前页、筛选、表格状态、设置草稿等），只在主线程读写。
//
// @author ixxxxoooo
type uiState struct {
	page string

	// 提示：视图里消费一次的 toast 文案。
	toast string

	// ---- Cursor 明细页 ----
	cRange   int // 0 今天 / 1 近 7 天 / 2 本周期 / 3 全部
	cModel   string
	cList    ui.ListState
	cSel     int
	cSort    ui.SortOrder
	cCache   cursorView
	cCacheOK string

	// ---- DeepSeek 账单页 ----
	dRange   int // 0 近 7 天 / 1 近 30 天 / 2 本月 / 3 全部
	dMetric  int // 0 花费 / 1 Token
	dList    ui.ListState
	dSel     int
	dSort    ui.SortOrder
	dCache   deepseekView
	dCacheOK string

	// ---- 设置页 ----
	set         model.Settings // 设置草稿（与服务保持同步）
	lastApplied model.Settings // 最近一次已提交给服务的设置，用于判断草稿是否有改动
	setLoaded   bool
	cursorAuth  int // 0 自动 / 1 手动（Segmented 绑定用）
	dsAuth      int
	trayMode    int
	interval    string
	notifyPct   map[int]bool
	dsBelow     float64
	loginItem   bool
	agOn        bool // 查询 Antigravity 额度（设置里是 AntigravityOff 的反面）

	cookieInput string
	keyInput    string
	tokenInput  string
	hasCookie   bool
	hasKey      bool
	hasToken    bool
	maskCookie  string
	maskKey     string
	maskToken   string

	testCursor, testDS, testAG   string // 连接测试结果文案
	testingC, testingD, testingA bool

	// ---- 数据管理 ----
	exportCreds   bool
	importMode    int // 0 合并 / 1 覆盖
	importCreds   bool
	importSet     bool
	confirmWipe   bool
	confirmImport bool
	importBackup  *transfer.Backup
	importPrev    transfer.Preview
	importPath    string
	importInfo    string
	dataMsg       string
	dataMsgBad    bool
	busy          bool
}

// newUIState 创建默认界面状态。
func newUIState() uiState {
	return uiState{
		page: PageOverview, cRange: 2, cModel: allModels, cSel: -1,
		cSort:  ui.SortOrder{Column: "time", Descending: true},
		dRange: 1, dSel: -1, dSort: ui.SortOrder{Column: "date", Descending: true},
	}
}
