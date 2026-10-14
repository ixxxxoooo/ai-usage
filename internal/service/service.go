package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/provider/cursor"
	"github.com/ixxxxoooo/ai-usage/internal/provider/deepseek"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/store"
	"github.com/ixxxxoooo/ai-usage/internal/util"
)

// Config 是创建 Service 所需的依赖，便于测试时注入假实现。
//
// @author ygw
type Config struct {
	Store   *store.Store
	Secrets secret.Store
	Cursor  *cursor.Client
	DS      *deepseek.Client
	// CursorLocalCreds 读取本机 Cursor 凭据；默认读 state.vscdb。
	CursorLocalCreds func() (cursor.Credentials, error)
	// ChromeTokens 扫描浏览器里的 DeepSeek token；默认扫描本机各浏览器。
	ChromeTokens func() ([]deepseek.ChromeToken, error)
	// ChromeNotes 返回最近一次扫描浏览器的记录（仅诊断用）；默认重新扫描并返回记录。
	ChromeNotes func() []string
	// BrowserDenied 判断浏览器数据目录是否被 macOS 权限拒绝；默认检查本机各浏览器。
	BrowserDenied func() bool
	// Notify 发送系统通知；为 nil 则不通知。
	Notify func(id, title, body string)
	// OnChange 在状态变化后调用（可能在任意 goroutine，需自行切线程）。
	OnChange func()
	// Now 返回当前时间；默认 time.Now。
	Now func() time.Time
}

// Service 是后台刷新与状态管理的核心。
//
// @author ygw
type Service struct {
	cfg Config

	mu    sync.RWMutex
	state State

	refreshMu sync.Mutex // 同一时刻只允许一次刷新
	kick      chan struct{}
	cfgChange chan struct{}
	stopOnce  sync.Once
	stop      chan struct{}

	tokenMu    sync.Mutex
	chromeTok  string // 缓存的 Chrome token（内存中，不落盘）
	chromeSrc  string
	lastManual time.Time
}

// New 创建 Service，并从数据库恢复上次的用量/余额，使界面一启动就有数据。
//
// 参数 cfg：依赖配置。
// 返回值：*Service。
func New(cfg Config) (*Service, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Cursor == nil {
		cfg.Cursor = cursor.NewClient()
	}
	if cfg.DS == nil {
		cfg.DS = deepseek.NewClient()
	}
	if cfg.CursorLocalCreds == nil {
		cfg.CursorLocalCreds = func() (cursor.Credentials, error) { return cursor.ReadLocalCredentials("") }
	}
	if cfg.ChromeTokens == nil {
		cfg.ChromeTokens = func() ([]deepseek.ChromeToken, error) { return deepseek.FindChromeTokens("") }
	}
	if cfg.BrowserDenied == nil {
		cfg.BrowserDenied = func() bool { return deepseek.BrowserAccessDenied("") }
	}
	if cfg.ChromeNotes == nil {
		cfg.ChromeNotes = func() []string { _, n, _ := deepseek.ScanChromeTokens(""); return n }
	}
	s := &Service{cfg: cfg, kick: make(chan struct{}, 1), cfgChange: make(chan struct{}, 1), stop: make(chan struct{})}
	st, err := cfg.Store.GetSettings()
	if err != nil {
		return nil, err
	}
	s.state.Settings = st
	s.restore()
	return s, nil
}

// restore 从数据库恢复上次状态。
func (s *Service) restore() {
	var u model.CursorUsage
	if ok, _ := s.cfg.Store.GetJSON("cursor_usage", &u); ok {
		s.state.Cursor.Usage, s.state.Cursor.HasUsage = u, true
		s.state.Cursor.UpdatedAt = u.FetchedAt
	}
	if n, err := s.cfg.Store.CursorEventCount(); err == nil {
		s.state.Cursor.EventCount = n
	}
	s.state.Cursor.Status = StatusIdle
	var b model.DeepSeekBalance
	if ok, _ := s.cfg.Store.GetJSON("deepseek_balance", &b); ok {
		s.state.DeepSeek.Balance, s.state.DeepSeek.HasBalance = b, true
		s.state.DeepSeek.UpdatedAt = b.FetchedAt
	}
	s.state.DeepSeek.Status = StatusIdle
	s.deepseekDerived().apply(&s.state.DeepSeek)
	s.state.DeepSeek.HasAPIKey = s.secretExists(secret.KeyDeepSeekKey)
}

// secretExists 判断某凭据是否已保存。
func (s *Service) secretExists(key string) bool {
	if s.cfg.Secrets == nil {
		return false
	}
	v, err := s.cfg.Secrets.Get(key)
	return err == nil && strings.TrimSpace(v) != ""
}

// State 返回当前状态的拷贝。
//
// 返回值：State。
func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.state
	st.Settings.NotifyCursorPercent = append([]int(nil), s.state.Settings.NotifyCursorPercent...)
	return st
}

// Store 返回底层存储（UI 查询明细/账单用）。
//
// 返回值：*store.Store。
func (s *Service) Store() *store.Store { return s.cfg.Store }

// Secrets 返回凭据存储。
//
// 返回值：secret.Store。
func (s *Service) Secrets() secret.Store { return s.cfg.Secrets }

// changed 通知界面刷新。
func (s *Service) changed() {
	if s.cfg.OnChange != nil {
		s.cfg.OnChange()
	}
}

// update 在锁内修改状态，然后通知界面。
func (s *Service) update(fn func(*State)) {
	s.mu.Lock()
	fn(&s.state)
	s.mu.Unlock()
	s.changed()
}

// Settings 返回当前设置。
//
// 返回值：设置副本。
func (s *Service) Settings() model.Settings { return s.State().Settings }

// UpdateSettings 校验并保存设置，同时唤醒调度器让新间隔生效。
//
// 参数 st：新设置。
// 返回值：保存错误。
func (s *Service) UpdateSettings(st model.Settings) error {
	st = st.Normalize()
	if err := s.cfg.Store.SaveSettings(st); err != nil {
		return err
	}
	s.update(func(x *State) { x.Settings = st })
	select {
	case s.cfgChange <- struct{}{}:
	default:
	}
	return nil
}

// ReloadFromStore 在导入数据后，重新从数据库载入设置与上次状态。
func (s *Service) ReloadFromStore() {
	st, err := s.cfg.Store.GetSettings()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.state.Settings = st
	s.restore()
	s.mu.Unlock()
	s.changed()
	select {
	case s.cfgChange <- struct{}{}:
	default:
	}
}

// interval 返回当前刷新间隔。
func (s *Service) interval() time.Duration {
	return time.Duration(s.State().Settings.RefreshMinutes) * time.Minute
}

// Start 启动后台调度循环：启动即刷新一次，之后按间隔刷新；Kick 可立即触发。
//
// 参数 ctx：取消该 ctx 或调用 Stop 都会结束循环。
func (s *Service) Start(ctx context.Context) {
	go func() {
		s.RefreshAll(ctx)
		timer := time.NewTimer(s.nextWait())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-timer.C:
				s.RefreshAll(ctx)
			case <-s.kick:
				s.RefreshAll(ctx)
			case <-s.cfgChange:
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(s.nextWait())
		}
	}()
}

// nextWait 计算到下次自动刷新还需等待多久（取两个数据源中较早的计划时间）。
func (s *Service) nextWait() time.Duration {
	st := s.State()
	base := time.Duration(st.Settings.RefreshMinutes) * time.Minute
	now := s.cfg.Now()
	wait := base
	for _, p := range []ProviderState{st.Cursor.ProviderState, st.DeepSeek.ProviderState} {
		if !p.NextTry.IsZero() {
			if d := p.NextTry.Sub(now); d < wait {
				wait = d // 已过期的计划视为立即（后面统一抬到最小 5 秒）
			}
		}
	}
	if wait < 5*time.Second {
		wait = 5 * time.Second
	}
	return wait
}

// Stop 停止调度循环。
func (s *Service) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }

// Kick 请求尽快刷新一次（系统唤醒、网络恢复时调用）；多次调用会合并。
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// RefreshManual 响应用户的手动刷新；3 秒内的重复点击会被合并。
//
// 参数 ctx：上下文。
// 返回值：是否真正执行了刷新。
func (s *Service) RefreshManual(ctx context.Context) bool {
	s.tokenMu.Lock()
	if s.cfg.Now().Sub(s.lastManual) < 3*time.Second {
		s.tokenMu.Unlock()
		return false
	}
	s.lastManual = s.cfg.Now()
	s.tokenMu.Unlock()
	s.RefreshAll(ctx)
	return true
}

// RefreshAll 并发刷新两个数据源；同一时刻只会有一次刷新在跑。
//
// 参数 ctx：上下文。
func (s *Service) RefreshAll(ctx context.Context) {
	if !s.refreshMu.TryLock() {
		return
	}
	defer s.refreshMu.Unlock()
	s.update(func(x *State) { x.Refreshing = true })
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.refreshCursor(ctx) }()
	go func() { defer wg.Done(); s.refreshDeepSeek(ctx) }()
	wg.Wait()
	s.update(func(x *State) { x.Refreshing = false })
}

// markLoading 把数据源置为刷新中（保留旧数据）。
func (s *Service) markLoading(set func(*State)) {
	s.update(set)
}

// applyFailure 统一处理失败：归类状态、计算退避、保留旧数据。
func (s *Service) applyFailure(p *ProviderState, err error) {
	status, msg := classify(err)
	p.Status, p.Message = status, msg
	p.LastTry = s.cfg.Now()
	p.Failures++
	var rl *cursor.RateLimitError
	var retry time.Duration
	if errors.As(err, &rl) {
		retry = rl.RetryAfter
	}
	base := time.Duration(s.state.Settings.RefreshMinutes) * time.Minute
	if status == StatusNoCreds || status == StatusUnauthorized {
		// 凭据问题短期内不会自愈，按基础间隔轮询即可（用户修复后手动刷新）
		p.NextTry = p.LastTry.Add(base)
		return
	}
	p.NextTry = p.LastTry.Add(backoff(base, p.Failures-1, retry))
}

// applySuccess 统一处理成功。
func (s *Service) applySuccess(p *ProviderState) {
	now := s.cfg.Now()
	p.Status, p.Message = StatusOK, ""
	p.UpdatedAt, p.LastTry = now, now
	p.Failures = 0
	p.NextTry = now.Add(time.Duration(s.state.Settings.RefreshMinutes) * time.Minute)
}

// ---- Cursor ----

// cursorCredentials 按设置的方式取得 Cursor 凭据。
//
// 返回值：凭据、来源说明、错误。
func (s *Service) cursorCredentials() (cursor.Credentials, string, error) {
	mode := s.State().Settings.CursorAuthMode
	manual := func() (cursor.Credentials, string, error) {
		if s.cfg.Secrets == nil {
			return cursor.Credentials{}, "", cursor.ErrNoCredentials
		}
		v, err := s.cfg.Secrets.Get(secret.KeyCursorCookie)
		if err != nil || strings.TrimSpace(v) == "" {
			return cursor.Credentials{}, "", cursor.ErrNoCredentials
		}
		c, err := cursor.ParseManualCookie(v)
		if err != nil {
			return cursor.Credentials{}, "", fmt.Errorf("手动 Cookie 无效：%w", err)
		}
		return c, "手动 Cookie", nil
	}
	if mode == model.AuthManual {
		return manual()
	}
	c, err := s.cfg.CursorLocalCreds()
	if err == nil {
		return c, "本机 Cursor", nil
	}
	// 自动读取失败时，若用户保存过手动 Cookie 则退而求其次
	if mc, src, merr := manual(); merr == nil {
		return mc, src, nil
	}
	return cursor.Credentials{}, "", err
}

// refreshCursor 刷新 Cursor：用量 + 增量明细。
func (s *Service) refreshCursor(ctx context.Context) {
	s.markLoading(func(x *State) { x.Cursor.Status = StatusLoading })
	cred, src, err := s.cursorCredentials()
	if err != nil {
		s.update(func(x *State) { s.applyFailure(&x.Cursor.ProviderState, err) })
		return
	}
	usage, err := s.cfg.Cursor.FetchUsage(ctx, cred)
	if err != nil {
		s.update(func(x *State) { x.Cursor.CredSource = src; s.applyFailure(&x.Cursor.ProviderState, err) })
		return
	}
	usage.FetchedAt = s.cfg.Now()
	if prev := s.State().Cursor.Usage.Email; prev != "" {
		usage.Email = prev // 邮箱几乎不变，1 分钟刷新时不必每次都多请求一次
	} else if email, e := s.cfg.Cursor.FetchEmail(ctx, cred); e == nil {
		usage.Email = email
	}
	_ = s.cfg.Store.SetJSON("cursor_usage", usage)
	_, _ = s.cfg.Store.AddSnapshot("cursor", usage.FetchedAt, usage, 30*time.Minute)

	eventsErr := s.syncCursorEvents(ctx, cred, usage)
	count, _ := s.cfg.Store.CursorEventCount()
	s.update(func(x *State) {
		x.Cursor.Usage, x.Cursor.HasUsage = usage, true
		x.Cursor.CredSource, x.Cursor.EventCount = src, count
		x.Cursor.EventsErr = eventsErr
		s.applySuccess(&x.Cursor.ProviderState)
	})
	s.checkCursorThresholds(usage)
}

// syncCursorEvents 增量同步明细：首次拉全部历史，之后只拉最近一条之前 2 天的重叠区间。
//
// 返回值：失败说明（成功为空）。
func (s *Service) syncCursorEvents(ctx context.Context, cred cursor.Credentials, usage model.CursorUsage) string {
	latest, err := s.cfg.Store.LatestCursorEventTime()
	if err != nil {
		return "读取本地明细失败：" + err.Error()
	}
	var since time.Time
	if !latest.IsZero() {
		since = latest.Add(-48 * time.Hour)
		if !usage.CycleStart.IsZero() && usage.CycleStart.Before(since) {
			// 周期起点更早时，保证本周期完整
			since = usage.CycleStart
		}
	}
	save := func(since, until time.Time) string {
		evs, err := s.cfg.Cursor.FetchEvents(ctx, cred, since, until, nil)
		if len(evs) > 0 {
			if _, e := s.cfg.Store.UpsertCursorEvents(evs); e != nil {
				return "保存明细失败：" + e.Error()
			}
		}
		if err != nil {
			_, msg := classify(err)
			return msg
		}
		return ""
	}
	if since.IsZero() && !usage.CycleStart.IsZero() {
		// 首次同步：历史可能有几千条、要拉几十秒。先拉并落库本周期（界面马上可用），再补更早的历史。
		if msg := save(usage.CycleStart, time.Time{}); msg != "" {
			return msg
		}
		if msg := save(time.Time{}, usage.CycleStart); msg != "" {
			return "本周期明细已同步，更早的历史未拉完：" + msg
		}
		return ""
	}
	return save(since, time.Time{})
}

// checkCursorThresholds 在越过设定百分比时发送一次通知（同一周期同一阈值只通知一次）。
func (s *Service) checkCursorThresholds(u model.CursorUsage) {
	st := s.State().Settings
	if !st.NotifyEnabled || s.cfg.Notify == nil || u.Limit <= 0 {
		return
	}
	notified := map[string]bool{}
	_, _ = s.cfg.Store.GetJSON("notified", &notified)
	changed := false
	pct := float64(u.Used) * 100 / float64(u.Limit)
	for _, th := range st.NotifyCursorPercent {
		key := fmt.Sprintf("cursor:%d:%s", th, u.CycleStart.Format("20060102"))
		if pct >= float64(th) && !notified[key] {
			notified[key] = true
			changed = true
			s.cfg.Notify(key, "Cursor 用量提醒",
				fmt.Sprintf("本周期已用 %d / %d 次（%.0f%%），剩余 %d 次。", u.Used, u.Limit, pct, u.Remaining()))
		}
	}
	if changed {
		_ = s.cfg.Store.SetJSON("notified", notified)
	}
}

// ---- DeepSeek ----

// deepseekToken 按设置取得平台 token。
//
// 参数 forceRescan：true 时忽略缓存重新扫描浏览器。
// 返回值：token、来源说明（无 token 时为空串与 ErrNoCredentials）。
func (s *Service) deepseekToken(forceRescan bool) (string, string, error) {
	mode := s.State().Settings.DeepSeekAuthMode
	manual := func() (string, string, error) {
		if s.cfg.Secrets == nil {
			return "", "", deepseek.ErrNoCredentials
		}
		v, err := s.cfg.Secrets.Get(secret.KeyDeepSeekToken)
		if err != nil || strings.TrimSpace(v) == "" {
			return "", "", deepseek.ErrNoCredentials
		}
		return strings.TrimSpace(v), "手动粘贴", nil
	}
	if mode == model.AuthManual {
		return manual()
	}
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if !forceRescan && s.chromeTok != "" {
		return s.chromeTok, s.chromeSrc, nil
	}
	toks, _ := s.cfg.ChromeTokens()
	if len(toks) > 0 {
		s.chromeTok = toks[0].Token
		s.chromeSrc = toks[0].Browser + " / " + toks[0].Profile
		return s.chromeTok, s.chromeSrc, nil
	}
	s.chromeTok, s.chromeSrc = "", ""
	tok, src, err := manual()
	if err != nil && s.cfg.BrowserDenied != nil && s.cfg.BrowserDenied() {
		return "", "", deepseek.ErrBrowserDenied
	}
	return tok, src, err
}

// invalidateToken 清除缓存的浏览器 token（收到 401 时调用）。
func (s *Service) invalidateToken() {
	s.tokenMu.Lock()
	s.chromeTok, s.chromeSrc = "", ""
	s.tokenMu.Unlock()
}

// refreshDeepSeek 刷新 DeepSeek：余额、累计已用、账单。
func (s *Service) refreshDeepSeek(ctx context.Context) {
	s.markLoading(func(x *State) { x.DeepSeek.Status = StatusLoading })
	apiKey := ""
	if s.cfg.Secrets != nil {
		apiKey, _ = s.cfg.Secrets.Get(secret.KeyDeepSeekKey)
	}
	apiKey = strings.TrimSpace(apiKey)
	token, tokenSrc, tokErr := s.deepseekToken(false)
	hasToken := tokErr == nil && token != ""
	if apiKey == "" && !hasToken {
		s.update(func(x *State) {
			x.DeepSeek.HasAPIKey, x.DeepSeek.TokenSrc = false, "未配置"
			err := tokErr
			if err == nil {
				err = deepseek.ErrNoCredentials
			}
			s.applyFailure(&x.DeepSeek.ProviderState, err)
		})
		return
	}

	var bal model.DeepSeekBalance
	var haveBal bool
	var firstErr error
	var summary model.DeepSeekBalance
	var haveSummary bool
	if hasToken {
		b, err := s.cfg.DS.FetchSummary(ctx, token)
		if errors.Is(err, deepseek.ErrUnauthorized) {
			// 浏览器里的 token 可能刚刷新过，重扫一次再试
			s.invalidateToken()
			if t2, src2, b2, ok := s.tryOtherTokens(ctx, token); ok {
				token, tokenSrc, b, err = t2, src2, b2, nil
			}
		}
		if err != nil {
			firstErr = err
			hasToken = false
			if errors.Is(err, deepseek.ErrUnauthorized) {
				s.invalidateToken()
			}
		} else {
			summary, haveSummary = b, true
		}
	}
	if apiKey != "" {
		b, err := s.cfg.DS.FetchBalanceByKey(ctx, apiKey)
		if err != nil {
			if firstErr == nil || !haveSummary {
				firstErr = err
			}
		} else {
			bal, haveBal = b, true
			if haveSummary && summary.HasSpent && strings.EqualFold(summary.Currency, bal.Currency) {
				bal.TotalSpent, bal.HasSpent = summary.TotalSpent, true
			}
		}
	}
	if !haveBal && haveSummary {
		bal, haveBal = summary, true
	}
	if !haveBal {
		s.update(func(x *State) {
			x.DeepSeek.HasAPIKey = apiKey != ""
			x.DeepSeek.TokenSrc = tokenSrcText(tokenSrc, hasToken)
			s.applyFailure(&x.DeepSeek.ProviderState, firstErr)
		})
		return
	}
	bal.FetchedAt = s.cfg.Now()
	_ = s.cfg.Store.SetJSON("deepseek_balance", bal)
	_, _ = s.cfg.Store.AddSnapshot("deepseek", bal.FetchedAt, bal, 30*time.Minute)

	billErr := ""
	if hasToken {
		billErr = s.syncDeepSeekBill(ctx, token)
	}
	d := s.deepseekDerived()
	s.update(func(x *State) {
		x.DeepSeek.Balance, x.DeepSeek.HasBalance = bal, true
		x.DeepSeek.HasAPIKey = apiKey != ""
		x.DeepSeek.TokenSrc = tokenSrcText(tokenSrc, hasToken)
		x.DeepSeek.BillErr = billErr
		d.apply(&x.DeepSeek)
		s.applySuccess(&x.DeepSeek.ProviderState)
	})
	s.checkDeepSeekThreshold(bal)
}

// tokenSrcText 生成界面展示的 token 来源说明。
func tokenSrcText(src string, ok bool) string {
	if !ok || src == "" {
		return "未配置"
	}
	return src
}

// syncDeepSeekBill 同步账单：本月每次都拉；历史月份只拉未同步过的。
//
// 返回值：失败说明（成功为空）。
func (s *Service) syncDeepSeekBill(ctx context.Context, token string) string {
	now := s.cfg.Now()
	months := s.State().Settings.HistoryDays/30 + 1
	synced := map[string]bool{}
	_, _ = s.cfg.Store.GetJSON("ds_synced_months", &synced)
	var firstErr error
	for i := 0; i < months; i++ {
		t := time.Date(now.Year(), now.Month()-time.Month(i), 1, 0, 0, 0, 0, now.Location())
		key := t.Format("2006-01")
		if i > 0 && synced[key] {
			continue
		}
		rows, err := s.cfg.DS.FetchMonth(ctx, token, t.Year(), int(t.Month()))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if errors.Is(err, deepseek.ErrUnauthorized) || errors.Is(err, deepseek.ErrRateLimited) {
				break
			}
			continue
		}
		if err := s.cfg.Store.UpsertDeepSeekDaily(rows); err != nil {
			return "保存账单失败：" + err.Error()
		}
		if i > 0 {
			synced[key] = true
		}
	}
	_ = s.cfg.Store.SetJSON("ds_synced_months", synced)
	if firstErr != nil {
		_, msg := classify(firstErr)
		return msg
	}
	return ""
}

// dsDerived 是由本地账单推算出的 DeepSeek 指标。
type dsDerived struct {
	today     model.DeepSeekDayStat
	monthCost float64
	month     string
	hasBill   bool
}

// apply 把推算指标写入状态（调用方需持有写锁）。
func (d dsDerived) apply(x *DeepSeekState) {
	x.Today, x.MonthCost, x.Month, x.HasBill = d.today, d.monthCost, d.month, d.hasBill
}

// deepseekDerived 读取本地账单，计算今日与本月指标；不依赖也不修改状态，可在锁外调用。
//
// 返回值：推算指标。
func (s *Service) deepseekDerived() dsDerived {
	now := s.cfg.Now()
	today := now.Format("2006-01-02")
	month := now.Format("2006-01")
	d := dsDerived{month: month}
	rows, err := s.cfg.Store.DeepSeekDaily(month+"-01", month+"-31")
	if err != nil {
		return d
	}
	d.today = model.DeepSeekToday(rows, today)
	d.monthCost = model.DeepSeekMonthCost(rows, month)
	all, _ := s.cfg.Store.DeepSeekDaily("", "")
	d.hasBill = len(all) > 0
	return d
}

// checkDeepSeekThreshold 余额低于阈值时通知（同一天只通知一次）。
func (s *Service) checkDeepSeekThreshold(b model.DeepSeekBalance) {
	st := s.State().Settings
	if !st.NotifyEnabled || s.cfg.Notify == nil || st.NotifyDeepSeekBelow <= 0 {
		return
	}
	if b.Total >= st.NotifyDeepSeekBelow {
		return
	}
	notified := map[string]bool{}
	_, _ = s.cfg.Store.GetJSON("notified", &notified)
	key := "deepseek:low:" + s.cfg.Now().Format("20060102")
	if notified[key] {
		return
	}
	notified[key] = true
	_ = s.cfg.Store.SetJSON("notified", notified)
	s.cfg.Notify(key, "DeepSeek 余额不足",
		fmt.Sprintf("当前余额 %.2f %s，低于提醒线 %.2f。", b.Total, b.Currency, st.NotifyDeepSeekBelow))
}

// SaveSecret 保存手动凭据，并清除对应缓存。
//
// 参数 key：secret.Key*；value：内容（空串表示删除）。
// 返回值：错误。
func (s *Service) SaveSecret(key, value string) error {
	if s.cfg.Secrets == nil {
		return errors.New("凭据存储不可用")
	}
	value = strings.TrimSpace(value)
	if key == secret.KeyDeepSeekToken && value != "" {
		// 兼容粘贴 "Bearer xxx"、整行请求头、localStorage JSON；Cookie 会被拒绝
		tok, nerr := deepseek.NormalizeToken(value)
		if nerr != nil {
			return errors.New("这不是平台 token（看起来像 Cookie）。请粘贴请求头 authorization: Bearer 后面的那一串，或 localStorage 里 userToken 的 value")
		}
		value = tok
	}
	var err error
	if value == "" {
		err = s.cfg.Secrets.Delete(key)
	} else {
		err = s.cfg.Secrets.Set(key, value)
	}
	if err != nil {
		return err
	}
	if key == secret.KeyDeepSeekToken {
		s.invalidateToken()
	}
	s.update(func(x *State) { x.DeepSeek.HasAPIKey = s.secretExists(secret.KeyDeepSeekKey) })
	return nil
}

// TestCursor 用当前设置的凭据方式测试 Cursor 连接。
//
// 参数 ctx：上下文。
// 返回值：成功时的说明文字，失败返回错误。
func (s *Service) TestCursor(ctx context.Context) (string, error) {
	cred, src, err := s.cursorCredentials()
	if err != nil {
		return "", err
	}
	u, err := s.cfg.Cursor.FetchUsage(ctx, cred)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("连接成功（%s）：已用 %d / %d 次", src, u.Used, u.Limit), nil
}

// TestDeepSeek 测试 DeepSeek 连接（优先网页 token，其次 API Key）。
//
// 参数 ctx：上下文。
// 返回值：成功时的说明文字，失败返回错误。
func (s *Service) TestDeepSeek(ctx context.Context) (string, error) {
	token, src, terr := s.deepseekToken(true)
	var firstErr error
	if terr == nil {
		b, err := s.cfg.DS.FetchSummary(ctx, token)
		if err == nil {
			return fmt.Sprintf("网页 token 有效（%s）：余额 %.2f %s，累计已用 %.2f", src, b.Total, b.Currency, b.TotalSpent), nil
		}
		firstErr = err
	}
	key := ""
	if s.cfg.Secrets != nil {
		key, _ = s.cfg.Secrets.Get(secret.KeyDeepSeekKey)
	}
	if strings.TrimSpace(key) == "" {
		if firstErr != nil {
			return "", firstErr
		}
		return "", deepseek.ErrNoCredentials
	}
	b, err := s.cfg.DS.FetchBalanceByKey(ctx, key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("API Key 有效：余额 %.2f %s", b.Total, b.Currency), nil
}

// tryOtherTokens 重新扫描浏览器，依次尝试除 bad 之外的所有候选 token，返回第一个可用的。
//
// 多个浏览器/Profile 里可能残留已过期的旧登录，只取第一个会误判为"登录过期"，所以要逐个试。
// 成功时会把该 token 写入缓存，下次直接使用。
//
// 参数 ctx：上下文；bad：已确认失效的 token。
// 返回值：可用 token、来源说明、汇总数据、是否找到。
func (s *Service) tryOtherTokens(ctx context.Context, bad string) (string, string, model.DeepSeekBalance, bool) {
	if s.State().Settings.DeepSeekAuthMode == model.AuthManual {
		return "", "", model.DeepSeekBalance{}, false
	}
	toks, _ := s.cfg.ChromeTokens()
	for _, t := range toks {
		if t.Token == "" || t.Token == bad {
			continue
		}
		b, err := s.cfg.DS.FetchSummary(ctx, t.Token)
		if err != nil {
			continue
		}
		src := t.Browser + " / " + t.Profile
		s.tokenMu.Lock()
		s.chromeTok, s.chromeSrc = t.Token, src
		s.tokenMu.Unlock()
		return t.Token, src, b, true
	}
	return "", "", model.DeepSeekBalance{}, false
}

// DSCandidate 是一次诊断中某个候选登录凭据的检查结果（不含 token 原文）。
//
// @author ygw
type DSCandidate struct {
	Source string `json:"source"` // 来源，如 "Chrome / Default" 或 "钥匙串（手动粘贴）"
	Masked string `json:"masked"` // 脱敏后的 token，如 2Cn7…03P4
	Length int    `json:"length"` // token 长度
	Usable bool   `json:"usable"` // 用它调用接口是否成功
	Error  string `json:"error,omitempty"`
}

// DSDiagnosis 是 DeepSeek 凭据诊断结果。
//
// @author ygw
type DSDiagnosis struct {
	Mode       string        `json:"mode"`       // auto / manual
	ScanError  string        `json:"scanError"`  // 扫描浏览器时的错误
	ScanNotes  []string      `json:"scanNotes"`  // 每个浏览器 Profile 的扫描记录
	Candidates []DSCandidate `json:"candidates"` // 浏览器与手动 token 的检查结果
	APIKey     *DSCandidate  `json:"apiKey,omitempty"`
}

// DiagnoseDeepSeek 逐个检查所有候选凭据（浏览器 token、手动 token、API Key）并给出结果。
//
// 只发只读请求；返回内容不包含任何凭据原文。
//
// 参数 ctx：上下文。
// 返回值：诊断结果。
func (s *Service) DiagnoseDeepSeek(ctx context.Context) DSDiagnosis {
	d := DSDiagnosis{Mode: string(s.State().Settings.DeepSeekAuthMode)}
	check := func(source, tok string) DSCandidate {
		c := DSCandidate{Source: source, Masked: util.MaskSecret(tok), Length: len(tok)}
		if _, err := s.cfg.DS.FetchSummary(ctx, tok); err != nil {
			c.Error = err.Error()
		} else {
			c.Usable = true
		}
		return c
	}
	toks, err := s.cfg.ChromeTokens()
	if err != nil {
		d.ScanError = err.Error()
	}
	if s.cfg.ChromeNotes != nil {
		d.ScanNotes = s.cfg.ChromeNotes()
	}
	for _, t := range toks {
		d.Candidates = append(d.Candidates, check(t.Browser+" / "+t.Profile, t.Token))
	}
	if s.cfg.Secrets != nil {
		if v, _ := s.cfg.Secrets.Get(secret.KeyDeepSeekToken); strings.TrimSpace(v) != "" {
			d.Candidates = append(d.Candidates, check("钥匙串（手动粘贴）", strings.TrimSpace(v)))
		}
		if v, _ := s.cfg.Secrets.Get(secret.KeyDeepSeekKey); strings.TrimSpace(v) != "" {
			v = strings.TrimSpace(v)
			c := DSCandidate{Source: "钥匙串（API Key）", Masked: util.MaskSecret(v), Length: len(v)}
			if _, err := s.cfg.DS.FetchBalanceByKey(ctx, v); err != nil {
				c.Error = err.Error()
			} else {
				c.Usable = true
			}
			d.APIKey = &c
		}
	}
	return d
}
