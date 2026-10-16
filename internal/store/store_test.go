package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
)

// 固定测试时间。
var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// ev 构造测试事件。
func ev(i int, mdl string) model.CursorEvent {
	return model.CursorEvent{Key: "k" + string(rune('a'+i)), Time: t0.Add(time.Duration(-i) * time.Hour), Model: mdl, Kind: "K", Requests: 1,
		InputTokens: int64(i), OutputTokens: 2, CacheWrite: 3, CacheRead: 4, TotalCents: 1.5, ChargedCents: 4, ConversationID: "c"}
}

// openTemp 打开临时文件库。
func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "data.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}

// TestSettingsRoundTrip 验证设置读写与默认值。
func TestSettingsRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	d, err := s.GetSettings()
	if err != nil || d.RefreshMinutes != 1 {
		t.Fatalf("默认: %+v %v", d, err)
	}
	st := model.DefaultSettings()
	st.RefreshMinutes, st.TrayDisplay, st.NotifyCursorPercent = 15, model.TrayBoth, []int{50, 95}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSettings()
	if got.RefreshMinutes != 15 || got.TrayDisplay != model.TrayBoth || len(got.NotifyCursorPercent) != 2 {
		t.Errorf("%+v", got)
	}
	// 非法值保存时被修正
	st.RefreshMinutes = 99999
	s.SaveSettings(st)
	if got, _ := s.GetSettings(); got.RefreshMinutes != 1 {
		t.Errorf("应修正: %d", got.RefreshMinutes)
	}
}

// TestCursorEvents 验证去重、过滤、排序与统计。
func TestCursorEvents(t *testing.T) {
	s, _ := openTemp(t)
	evs := []model.CursorEvent{ev(0, "a"), ev(1, "b"), ev(2, "a")}
	n, err := s.UpsertCursorEvents(evs)
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	n, _ = s.UpsertCursorEvents(append(evs, ev(3, "b")))
	if n != 1 {
		t.Errorf("重复写入只应新增 1 条，实际 %d", n)
	}
	if c, _ := s.CursorEventCount(""); c != 4 {
		t.Errorf("count=%d", c)
	}
	all, _ := s.CursorEvents(EventQuery{})
	if len(all) != 4 || !all[0].Time.Equal(t0) || all[0].Key != "ka" {
		t.Errorf("倒序: %+v", all[0])
	}
	if all[0].InputTokens != 0 || all[1].InputTokens != 1 || all[0].TotalCents != 1.5 || all[0].ConversationID != "c" {
		t.Errorf("字段回读: %+v", all[:2])
	}
	got, _ := s.CursorEvents(EventQuery{Model: "a"})
	if len(got) != 2 {
		t.Errorf("按模型: %d", len(got))
	}
	got, _ = s.CursorEvents(EventQuery{Since: t0.Add(-90 * time.Minute), Until: t0.Add(time.Minute)})
	if len(got) != 2 {
		t.Errorf("时间范围: %d", len(got))
	}
	got, _ = s.CursorEvents(EventQuery{Limit: 1})
	if len(got) != 1 {
		t.Errorf("limit: %d", len(got))
	}
	if l, _ := s.LatestCursorEventTime(""); !l.Equal(t0) {
		t.Errorf("latest: %v", l)
	}
	if ms, _ := s.CursorModels(""); len(ms) != 2 || ms[0] != "a" {
		t.Errorf("models: %v", ms)
	}
	empty, _ := openTemp(t)
	if l, _ := empty.LatestCursorEventTime(""); !l.IsZero() {
		t.Error("空库 latest 应为零值")
	}
}

// TestDeepSeekDaily 验证账单覆盖更新与范围查询。
func TestDeepSeekDaily(t *testing.T) {
	s, _ := openTemp(t)
	rows := []model.DeepSeekDaily{
		{Date: "2026-10-13", Model: "m", Currency: "CNY", CacheHit: 1, Cost: 0.1},
		{Date: "2026-10-14", Model: "m", Currency: "CNY", CacheHit: 2, Cost: 0.2},
		{Date: "2026-09-30", Model: "m", Currency: "CNY", Cost: 9},
	}
	if err := s.UpsertDeepSeekDaily(rows); err != nil {
		t.Fatal(err)
	}
	rows[1].CacheHit, rows[1].Cost = 99, 0.5 // 同主键以最新为准
	s.UpsertDeepSeekDaily(rows[1:2])
	all, _ := s.DeepSeekDaily("", "")
	if len(all) != 3 || all[0].Date != "2026-09-30" {
		t.Fatalf("全部: %+v", all)
	}
	oct, _ := s.DeepSeekDaily("2026-10-01", "2026-10-31")
	if len(oct) != 2 || oct[1].CacheHit != 99 || oct[1].Cost != 0.5 {
		t.Errorf("10 月: %+v", oct)
	}
}

// TestSnapshots 验证快照去重窗口与裁剪。
func TestSnapshots(t *testing.T) {
	s, _ := openTemp(t)
	u := model.CursorUsage{Used: 1, Limit: 500, FetchedAt: t0}
	if ok, _ := s.AddSnapshot("cursor", t0, u, time.Hour); !ok {
		t.Fatal("首次应写入")
	}
	u.FetchedAt = t0.Add(time.Minute) // 仅 fetchedAt 变化
	if ok, _ := s.AddSnapshot("cursor", t0.Add(time.Minute), u, time.Hour); ok {
		t.Error("内容未变且在窗口内应跳过")
	}
	u.Used = 2
	if ok, _ := s.AddSnapshot("cursor", t0.Add(2*time.Minute), u, time.Hour); !ok {
		t.Error("内容变化应写入")
	}
	if ok, _ := s.AddSnapshot("cursor", t0.Add(3*time.Hour), u, time.Hour); !ok {
		t.Error("超出窗口应写入")
	}
	s.AddSnapshot("deepseek", t0, model.DeepSeekBalance{Total: 1}, 0)
	sn, _ := s.Snapshots("cursor", time.Time{})
	if len(sn) != 3 {
		t.Fatalf("cursor 快照数 %d", len(sn))
	}
	var back model.CursorUsage
	json.Unmarshal(sn[1].Payload, &back)
	if back.Used != 2 {
		t.Errorf("payload: %+v", back)
	}
	if sn2, _ := s.Snapshots("cursor", t0.Add(time.Hour)); len(sn2) != 1 {
		t.Errorf("since: %d", len(sn2))
	}
	n, _ := s.PruneSnapshots(t0.Add(time.Hour))
	if n != 3 { // 2 个 cursor + 1 个 deepseek
		t.Errorf("裁剪 %d", n)
	}
}

// TestPersistenceAcrossReopen 验证关闭后重新打开数据仍在（持久化）。
func TestPersistenceAcrossReopen(t *testing.T) {
	s, path := openTemp(t)
	st := model.DefaultSettings()
	st.RefreshMinutes = 30
	s.SaveSettings(st)
	s.UpsertCursorEvents([]model.CursorEvent{ev(0, "a"), ev(1, "b")})
	s.UpsertDeepSeekDaily([]model.DeepSeekDaily{{Date: "2026-10-14", Model: "m", Currency: "CNY", Cost: 1}})
	s.SetJSON("cursor_usage", model.CursorUsage{Used: 359, Limit: 500})
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if g, _ := s2.GetSettings(); g.RefreshMinutes != 30 {
		t.Errorf("设置丢失: %+v", g)
	}
	if c, _ := s2.CursorEventCount(""); c != 2 {
		t.Errorf("明细丢失: %d", c)
	}
	if r, _ := s2.DeepSeekDaily("", ""); len(r) != 1 || r[0].Cost != 1 {
		t.Errorf("账单丢失: %+v", r)
	}
	var u model.CursorUsage
	if ok, _ := s2.GetJSON("cursor_usage", &u); !ok || u.Used != 359 {
		t.Errorf("状态丢失: %+v", u)
	}
}

// TestSchemaTooNew 验证库版本高于程序时拒绝打开，避免误改数据。
func TestSchemaTooNew(t *testing.T) {
	s, path := openTemp(t)
	if err := s.SetJSON("schema_version", 99); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil {
		t.Error("应拒绝高版本库")
	}
}

// sampleDataset 构造导入用数据集。
func sampleDataset() Dataset {
	st := model.DefaultSettings()
	st.RefreshMinutes = 20
	return Dataset{
		Settings:      st,
		CursorEvents:  []model.CursorEvent{ev(0, "a"), ev(1, "b")},
		DeepSeekDaily: []model.DeepSeekDaily{{Date: "2026-10-14", Model: "m", Currency: "CNY", Output: 5, Cost: 2}},
		Snapshots:     []Snapshot{{Provider: "cursor", At: t0, Payload: json.RawMessage(`{"used":1}`)}},
		KV:            map[string]json.RawMessage{"cursor_usage": json.RawMessage(`{"used":359,"limit":500}`), "evil": json.RawMessage(`1`)},
	}
}

// TestImportDatasetMerge 验证合并导入：幂等、不重复、KV 白名单。
func TestImportDatasetMerge(t *testing.T) {
	s, _ := openTemp(t)
	res, err := s.ImportDataset(sampleDataset(), false)
	if err != nil || res.EventsAdded != 2 || res.EventsSkipped != 0 || res.DailyUpserted != 1 || res.SnapshotsAdded != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = s.ImportDataset(sampleDataset(), false)
	if err != nil || res.EventsAdded != 0 || res.EventsSkipped != 2 || res.SnapshotsAdded != 0 {
		t.Fatalf("重复导入应不新增: %+v %v", res, err)
	}
	if c, _ := s.CursorEventCount(""); c != 2 {
		t.Errorf("明细数 %d", c)
	}
	if r, _ := s.DeepSeekDaily("", ""); len(r) != 1 {
		t.Errorf("账单数 %d", len(r))
	}
	if g, _ := s.GetSettings(); g.RefreshMinutes != 20 {
		t.Errorf("设置: %+v", g)
	}
	var u model.CursorUsage
	if ok, _ := s.GetJSON("cursor_usage", &u); !ok || u.Used != 359 {
		t.Errorf("KV: %+v", u)
	}
	var evil int
	if ok, _ := s.GetJSON("evil", &evil); ok {
		t.Error("非白名单 KV 不应被导入")
	}
}

// TestImportDatasetReplace 验证覆盖导入会先清空。
func TestImportDatasetReplace(t *testing.T) {
	s, _ := openTemp(t)
	s.UpsertCursorEvents([]model.CursorEvent{ev(5, "old"), ev(6, "old")})
	s.UpsertDeepSeekDaily([]model.DeepSeekDaily{{Date: "2020-01-01", Model: "old", Currency: "CNY"}})
	if _, err := s.ImportDataset(sampleDataset(), true); err != nil {
		t.Fatal(err)
	}
	all, _ := s.CursorEvents(EventQuery{})
	if len(all) != 2 || all[0].Model == "old" {
		t.Errorf("覆盖后: %+v", all)
	}
	if r, _ := s.DeepSeekDaily("", ""); len(r) != 1 || r[0].Model != "m" {
		t.Errorf("账单覆盖: %+v", r)
	}
}

// TestImportRollback 验证导入中途失败整体回滚（旧数据保持不变）。
func TestImportRollback(t *testing.T) {
	s, _ := openTemp(t)
	s.UpsertCursorEvents([]model.CursorEvent{ev(5, "old")})
	bad := sampleDataset()
	// 删除 snapshots 表，让覆盖导入在事务中途失败（此时明细表已被清空，必须回滚恢复）。
	s.db.Exec(`DROP TABLE snapshots`)
	if _, err := s.ImportDataset(bad, true); err == nil {
		t.Fatal("应当失败")
	}
	all, _ := s.CursorEvents(EventQuery{})
	if len(all) != 1 || all[0].Model != "old" {
		t.Errorf("回滚失败，旧数据被破坏: %+v", all)
	}
}

// TestReadDatasetRoundTrip 验证读出再导入到新库后内容一致。
func TestReadDatasetRoundTrip(t *testing.T) {
	a, _ := openTemp(t)
	a.ImportDataset(sampleDataset(), true)
	ds, err := a.ReadDataset()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := openTemp(t)
	if _, err := b.ImportDataset(ds, true); err != nil {
		t.Fatal(err)
	}
	ds2, _ := b.ReadDataset()
	j1, _ := json.Marshal(ds)
	j2, _ := json.Marshal(ds2)
	if string(j1) != string(j2) {
		t.Errorf("往返不一致:\n%s\n%s", j1, j2)
	}
}

// TestWipe 验证清空保留版本。
func TestWipe(t *testing.T) {
	s, path := openTemp(t)
	s.ImportDataset(sampleDataset(), true)
	if err := s.Wipe(); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.CursorEventCount(""); c != 0 {
		t.Error("明细未清空")
	}
	s.Close()
	if _, err := Open(path); err != nil {
		t.Errorf("清空后应仍能打开: %v", err)
	}
}

// TestMemoryStore 验证内存库可用。
func TestMemoryStore(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.UpsertCursorEvents([]model.CursorEvent{ev(0, "a")}); err != nil || n != 1 {
		t.Errorf("%d %v", n, err)
	}
}

// TestMigrateV1ToV2ClearsMixedEvents v1 库升级到 v2：加 account 列，并清掉无法归属账号的旧明细。
func TestMigrateV1ToV2ClearsMixedEvents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v1.db")
	old, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO kv VALUES('schema_version','1')`,
		`CREATE TABLE cursor_events (key TEXT PRIMARY KEY, ts INTEGER NOT NULL, model TEXT NOT NULL, kind TEXT NOT NULL,
			requests REAL NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, cache_write INTEGER NOT NULL,
			cache_read INTEGER NOT NULL, total_cents REAL NOT NULL, charged_cents REAL NOT NULL, is_token INTEGER NOT NULL, conversation_id TEXT NOT NULL)`,
		`INSERT INTO cursor_events VALUES('old1',1,'m','K',1,0,0,0,0,0,0,0,'c')`,
		`CREATE TABLE deepseek_daily (date TEXT NOT NULL, model TEXT NOT NULL, currency TEXT NOT NULL, cache_hit INTEGER NOT NULL,
			cache_miss INTEGER NOT NULL, output INTEGER NOT NULL, requests INTEGER NOT NULL, cost REAL NOT NULL, PRIMARY KEY (date, model, currency))`,
		`CREATE TABLE snapshots (id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, at INTEGER NOT NULL, payload TEXT NOT NULL)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.CursorEventCount(""); n != 0 {
		t.Errorf("旧明细应被清掉，剩 %d", n)
	}
	e := ev(1, "m")
	e.Account = "user_A"
	if _, err := s.UpsertCursorEvents([]model.CursorEvent{e}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CursorEventCount("user_A"); n != 1 {
		t.Errorf("升级后应能按账号写入: %d", n)
	}
	// 重复打开（已是 v2）不应再清数据
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if n, _ := s2.CursorEventCount("user_A"); n != 1 {
		t.Errorf("v2 重开不应丢数据: %d", n)
	}
}

// TestEventsScopedByAccount 明细查询、条数、最新时间、模型列表都按账号隔离；导入旧备份（无账号）归到当前账号。
func TestEventsScopedByAccount(t *testing.T) {
	s, _ := openTemp(t)
	a, b := ev(1, "ma"), ev(2, "mb")
	a.Account, b.Account = "A", "B"
	if _, err := s.UpsertCursorEvents([]model.CursorEvent{a, b}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CursorEvents(EventQuery{Account: "A"}); len(got) != 1 || got[0].Model != "ma" || got[0].Account != "A" {
		t.Errorf("A: %+v", got)
	}
	if all, _ := s.CursorEvents(EventQuery{}); len(all) != 2 {
		t.Errorf("不限账号应返回全部: %d", len(all))
	}
	if n, _ := s.CursorEventCount("B"); n != 1 {
		t.Errorf("B 条数 %d", n)
	}
	if m, _ := s.CursorModels("B"); len(m) != 1 || m[0] != "mb" {
		t.Errorf("B 模型 %v", m)
	}
	if ts, _ := s.LatestCursorEventTime("A"); !ts.Equal(a.Time.Truncate(time.Millisecond)) {
		t.Errorf("A 最新时间 %v", ts)
	}
	// 旧备份里的事件没有账号 -> 归到 kv 里记录的当前账号
	_ = s.SetJSON("cursor_account", "A")
	legacy := ev(3, "mold")
	if _, err := s.ImportDataset(Dataset{CursorEvents: []model.CursorEvent{legacy}}, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CursorEvents(EventQuery{Account: "A", Model: "mold"}); len(got) != 1 {
		t.Errorf("无账号事件应归当前账号 A: %d", len(got))
	}
}

// TestSyncCursorEventsReconciles 完整拉取后与远端对齐：更新已有、清掉区间内多余的、区间外与端点不动、其他账号不动。
func TestSyncCursorEventsReconciles(t *testing.T) {
	s, _ := openTemp(t)
	mk := func(key string, h int, acc string, cache int64) model.CursorEvent {
		e := ev(0, "m")
		e.Key, e.Time, e.Account, e.CacheRead = key, t0.Add(time.Duration(h)*time.Hour), acc, cache
		return e
	}
	seed := []model.CursorEvent{
		mk("old", -100, "A", 1), mk("edge-lo", -10, "A", 1), mk("stale", -5, "A", 1), mk("live", -3, "A", 10),
		mk("edge-hi", 10, "A", 1), mk("other-acc", -5, "B", 1),
	}
	if _, err := s.UpsertCursorEvents(seed); err != nil {
		t.Fatal(err)
	}
	fetched := []model.CursorEvent{mk("live", -3, "", 999), mk("new", -4, "", 5)}
	added, removed, err := s.SyncCursorEvents("A", fetched, t0.Add(-10*time.Hour), t0.Add(10*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || removed != 1 {
		t.Errorf("应新增 1、清理 1：added=%d removed=%d", added, removed)
	}
	got, _ := s.CursorEvents(EventQuery{})
	keys := map[string]model.CursorEvent{}
	for _, e := range got {
		keys[e.Key] = e
	}
	if _, ok := keys["stale"]; ok {
		t.Error("区间内远端已不存在的记录应被清理")
	}
	for _, k := range []string{"old", "edge-lo", "edge-hi", "other-acc", "new"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("%s 不应被清理", k)
		}
	}
	if keys["live"].CacheRead != 999 || keys["live"].Account != "A" {
		t.Errorf("已有事件应被更新为最新值并归属账号 A: %+v", keys["live"])
	}
}

// TestMigrateV2ToV3DedupesSnapshots v2 库里同一事件的多份快照升级后合并成一行（保留 Token 最多的），并换成新的去重键。
func TestMigrateV2ToV3DedupesSnapshots(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v2.db")
	old, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO kv VALUES('schema_version','2')`,
		`CREATE TABLE cursor_events (key TEXT PRIMARY KEY, ts INTEGER NOT NULL, model TEXT NOT NULL, kind TEXT NOT NULL,
			requests REAL NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, cache_write INTEGER NOT NULL,
			cache_read INTEGER NOT NULL, total_cents REAL NOT NULL, charged_cents REAL NOT NULL, is_token INTEGER NOT NULL,
			conversation_id TEXT NOT NULL, account TEXT NOT NULL DEFAULT '')`,
		// 同一事件（相同时间/模型/会话/类型）的三份快照，Token 逐步增长
		`INSERT INTO cursor_events VALUES('s1',1000,'m','K',1,1,1,1,100,1,4,0,'c','A')`,
		`INSERT INTO cursor_events VALUES('s2',1000,'m','K',1,1,1,1,900,9,4,0,'c','A')`,
		`INSERT INTO cursor_events VALUES('s3',1000,'m','K',1,1,1,1,500,5,4,0,'c','A')`,
		// 另一个不同事件
		`INSERT INTO cursor_events VALUES('x1',2000,'m','K',1,1,1,1,7,1,4,0,'c','A')`,
		`CREATE TABLE deepseek_daily (date TEXT NOT NULL, model TEXT NOT NULL, currency TEXT NOT NULL, cache_hit INTEGER NOT NULL,
			cache_miss INTEGER NOT NULL, output INTEGER NOT NULL, requests INTEGER NOT NULL, cost REAL NOT NULL, PRIMARY KEY (date, model, currency))`,
		`CREATE TABLE snapshots (id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, at INTEGER NOT NULL, payload TEXT NOT NULL)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, _ := s.CursorEvents(EventQuery{Account: "A"})
	if len(got) != 2 {
		t.Fatalf("三份快照应合并为一行，共 2 条，实际 %d", len(got))
	}
	for _, e := range got {
		if e.Key != model.EventKey(e) {
			t.Errorf("应换成新的去重键: %+v", e)
		}
		if e.Time.UnixMilli() == 1000 && e.CacheRead != 900 {
			t.Errorf("应保留 Token 最多的快照: %+v", e)
		}
	}
	// 之后用新键拉到的同一事件应更新这一行，而不是新增
	again := got[0]
	again.CacheRead += 1
	if n, _ := s.UpsertCursorEvents([]model.CursorEvent{again}); n != 0 {
		t.Errorf("同一事件不应再新增行: %d", n)
	}
	if n, _ := s.CursorEventCount("A"); n != 2 {
		t.Errorf("总数应仍为 2: %d", n)
	}
}
