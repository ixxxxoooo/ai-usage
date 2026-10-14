// Package store 用 SQLite（纯 Go 驱动 modernc）持久化设置、明细、账单与快照。
//
// @author ygw
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	_ "modernc.org/sqlite" // 驱动注册
)

// SchemaVersion 是当前数据库结构版本。
const SchemaVersion = 1

// Store 封装数据库访问；所有方法并发安全（底层单连接串行）。
//
// @author ygw
type Store struct {
	db   *sql.DB
	path string
}

// Open 打开（必要时创建并迁移）数据库。
//
// 参数 path：数据库文件路径；传 ":memory:" 使用内存库（测试用）。
// 返回值：*Store。
func Open(path string) (*Store, error) {
	dsnPath := path
	q := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if path == ":memory:" {
		dsnPath = ":memory:"
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录失败：%w", err)
		}
		q += "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	}
	u := url.URL{Scheme: "file", Opaque: dsnPath, RawQuery: q}
	if path != ":memory:" {
		u = url.URL{Scheme: "file", Path: dsnPath, RawQuery: q}
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写者，串行最简单也最安全
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
//
// 返回值：关闭错误。
func (s *Store) Close() error { return s.db.Close() }

// Path 返回数据库文件路径。
//
// 返回值：路径。
func (s *Store) Path() string { return s.path }

// migrate 创建表并按版本迁移。
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	var v int
	var raw sql.NullString
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key='schema_version'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if raw.Valid {
		fmt.Sscanf(raw.String, "%d", &v)
	}
	if v > SchemaVersion {
		return fmt.Errorf("数据库版本 %d 高于当前程序支持的 %d，请升级应用", v, SchemaVersion)
	}
	if v < 1 {
		stmts := []string{
			`CREATE TABLE IF NOT EXISTS cursor_events (
				key TEXT PRIMARY KEY, ts INTEGER NOT NULL, model TEXT NOT NULL, kind TEXT NOT NULL,
				requests REAL NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
				cache_write INTEGER NOT NULL, cache_read INTEGER NOT NULL, total_cents REAL NOT NULL,
				charged_cents REAL NOT NULL, is_token INTEGER NOT NULL, conversation_id TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_cursor_events_ts ON cursor_events(ts)`,
			`CREATE TABLE IF NOT EXISTS deepseek_daily (
				date TEXT NOT NULL, model TEXT NOT NULL, currency TEXT NOT NULL,
				cache_hit INTEGER NOT NULL, cache_miss INTEGER NOT NULL, output INTEGER NOT NULL,
				requests INTEGER NOT NULL, cost REAL NOT NULL, PRIMARY KEY (date, model, currency))`,
			`CREATE TABLE IF NOT EXISTS snapshots (
				id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, at INTEGER NOT NULL, payload TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_snapshots_provider_at ON snapshots(provider, at)`,
		}
		for _, st := range stmts {
			if _, err := s.db.Exec(st); err != nil {
				return fmt.Errorf("建表失败：%w", err)
			}
		}
	}
	_, err = s.db.Exec(`INSERT INTO kv(key,value) VALUES('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(SchemaVersion))
	return err
}

// ---- 键值与设置 ----

// SetJSON 把任意值序列化后存入 kv。
//
// 参数 key：键；v：值。
// 返回值：错误。
func (s *Store) SetJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(b))
	return err
}

// GetJSON 读取 kv 中的 JSON 到 out。
//
// 参数 key：键；out：目标指针。
// 返回值：是否存在、错误。
func (s *Store) GetJSON(key string, out any) (bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), out)
}

// GetSettings 读取设置，不存在时返回默认值；读到的值会被 Normalize 修正。
//
// 返回值：设置。
func (s *Store) GetSettings() (model.Settings, error) {
	var st model.Settings
	ok, err := s.GetJSON("settings", &st)
	if err != nil {
		return model.DefaultSettings(), err
	}
	if !ok {
		return model.DefaultSettings(), nil
	}
	return st.Normalize(), nil
}

// SaveSettings 保存设置（保存前会 Normalize）。
//
// 参数 st：设置。
// 返回值：错误。
func (s *Store) SaveSettings(st model.Settings) error {
	return s.SetJSON("settings", st.Normalize())
}

// ---- Cursor 明细 ----

// UpsertCursorEvents 批量写入明细，按 Key 去重（已存在的忽略）。
//
// 参数 evs：事件列表。
// 返回值：新插入的条数。
func (s *Store) UpsertCursorEvents(evs []model.CursorEvent) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	n, err := insertEvents(tx, evs)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	return n, tx.Commit()
}

// insertEvents 在事务内插入事件。
func insertEvents(tx *sql.Tx, evs []model.CursorEvent) (int, error) {
	st, err := tx.Prepare(`INSERT OR IGNORE INTO cursor_events
		(key,ts,model,kind,requests,input_tokens,output_tokens,cache_write,cache_read,total_cents,charged_cents,is_token,conversation_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	n := 0
	for _, e := range evs {
		tok := 0
		if e.IsTokenBased {
			tok = 1
		}
		r, err := st.Exec(e.Key, e.Time.UnixMilli(), e.Model, e.Kind, e.Requests, e.InputTokens, e.OutputTokens,
			e.CacheWrite, e.CacheRead, e.TotalCents, e.ChargedCents, tok, e.ConversationID)
		if err != nil {
			return n, err
		}
		if c, _ := r.RowsAffected(); c > 0 {
			n++
		}
	}
	return n, nil
}

// EventQuery 是明细查询条件。
type EventQuery struct {
	Since, Until time.Time // 零值表示不限
	Model        string    // 为空表示不限
	Limit        int       // <=0 表示不限
}

// CursorEvents 按条件查询明细，按时间倒序。
//
// 参数 q：查询条件。
// 返回值：事件列表。
func (s *Store) CursorEvents(q EventQuery) ([]model.CursorEvent, error) {
	sqlStr := `SELECT key,ts,model,kind,requests,input_tokens,output_tokens,cache_write,cache_read,total_cents,charged_cents,is_token,conversation_id FROM cursor_events WHERE 1=1`
	var args []any
	if !q.Since.IsZero() {
		sqlStr += ` AND ts >= ?`
		args = append(args, q.Since.UnixMilli())
	}
	if !q.Until.IsZero() {
		sqlStr += ` AND ts < ?`
		args = append(args, q.Until.UnixMilli())
	}
	if q.Model != "" {
		sqlStr += ` AND model = ?`
		args = append(args, q.Model)
	}
	sqlStr += ` ORDER BY ts DESC, key`
	if q.Limit > 0 {
		sqlStr += fmt.Sprintf(` LIMIT %d`, q.Limit)
	}
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// scanEvents 读取事件结果集。
func scanEvents(rows *sql.Rows) ([]model.CursorEvent, error) {
	var out []model.CursorEvent
	for rows.Next() {
		var e model.CursorEvent
		var ts int64
		var tok int
		if err := rows.Scan(&e.Key, &ts, &e.Model, &e.Kind, &e.Requests, &e.InputTokens, &e.OutputTokens,
			&e.CacheWrite, &e.CacheRead, &e.TotalCents, &e.ChargedCents, &tok, &e.ConversationID); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ts)
		e.IsTokenBased = tok == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// CursorEventCount 返回明细总条数。
//
// 返回值：条数。
func (s *Store) CursorEventCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM cursor_events`).Scan(&n)
	return n, err
}

// LatestCursorEventTime 返回最新一条明细的时间（没有则为零值）。
//
// 返回值：时间。
func (s *Store) LatestCursorEventTime() (time.Time, error) {
	var ts sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(ts) FROM cursor_events`).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	if !ts.Valid {
		return time.Time{}, nil
	}
	return time.UnixMilli(ts.Int64), nil
}

// CursorModels 返回出现过的模型名（字典序）。
//
// 返回值：模型列表。
func (s *Store) CursorModels() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT model FROM cursor_events ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- DeepSeek 账单 ----

// UpsertDeepSeekDaily 批量写入账单明细，同主键覆盖（以最新抓取为准）。
//
// 参数 rows：明细。
// 返回值：错误。
func (s *Store) UpsertDeepSeekDaily(rows []model.DeepSeekDaily) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := upsertDaily(tx, rows); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// upsertDaily 在事务内写入账单。
func upsertDaily(tx *sql.Tx, rows []model.DeepSeekDaily) error {
	st, err := tx.Prepare(`INSERT INTO deepseek_daily (date,model,currency,cache_hit,cache_miss,output,requests,cost)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(date,model,currency) DO UPDATE SET cache_hit=excluded.cache_hit, cache_miss=excluded.cache_miss,
		output=excluded.output, requests=excluded.requests, cost=excluded.cost`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range rows {
		if _, err := st.Exec(r.Date, r.Model, r.Currency, r.CacheHit, r.CacheMiss, r.Output, r.Requests, r.Cost); err != nil {
			return err
		}
	}
	return nil
}

// DeepSeekDaily 查询 [sinceDate, untilDate]（含，YYYY-MM-DD，空表示不限）的账单明细。
//
// 参数 sinceDate/untilDate：日期范围。
// 返回值：按日期、模型升序的明细。
func (s *Store) DeepSeekDaily(sinceDate, untilDate string) ([]model.DeepSeekDaily, error) {
	q := `SELECT date,model,currency,cache_hit,cache_miss,output,requests,cost FROM deepseek_daily WHERE 1=1`
	var args []any
	if sinceDate != "" {
		q += ` AND date >= ?`
		args = append(args, sinceDate)
	}
	if untilDate != "" {
		q += ` AND date <= ?`
		args = append(args, untilDate)
	}
	q += ` ORDER BY date, model, currency`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeepSeekDaily
	for rows.Next() {
		var r model.DeepSeekDaily
		if err := rows.Scan(&r.Date, &r.Model, &r.Currency, &r.CacheHit, &r.CacheMiss, &r.Output, &r.Requests, &r.Cost); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- 快照（用于趋势与历史） ----

// Snapshot 是某数据源在某时刻的原始快照。
type Snapshot struct {
	Provider string          `json:"provider"` // cursor / deepseek
	At       time.Time       `json:"at"`
	Payload  json.RawMessage `json:"payload"`
}

// AddSnapshot 追加快照；为控制体积，同一数据源在 minGap 内且内容未变时不重复写入。
//
// 参数 provider：数据源；at：时间；payload：快照对象；minGap：去重窗口（0 表示总是写入）。
// 返回值：是否实际写入。
func (s *Store) AddSnapshot(provider string, at time.Time, payload any, minGap time.Duration) (bool, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	if minGap > 0 {
		var lastAt int64
		var lastPayload string
		err := s.db.QueryRow(`SELECT at,payload FROM snapshots WHERE provider=? ORDER BY at DESC LIMIT 1`, provider).Scan(&lastAt, &lastPayload)
		if err == nil && at.UnixMilli()-lastAt < minGap.Milliseconds() && sameSnapshot(lastPayload, string(b)) {
			return false, nil
		}
	}
	_, err = s.db.Exec(`INSERT INTO snapshots(provider,at,payload) VALUES(?,?,?)`, provider, at.UnixMilli(), string(b))
	return err == nil, err
}

// sameSnapshot 比较两个快照 JSON 是否等价（忽略 fetchedAt 字段）。
func sameSnapshot(a, b string) bool {
	strip := func(s string) string {
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) != nil {
			return s
		}
		delete(m, "fetchedAt")
		out, _ := json.Marshal(m)
		return string(out)
	}
	return strip(a) == strip(b)
}

// Snapshots 查询某数据源 since 之后的快照，按时间升序。
//
// 参数 provider：数据源；since：起点（零值不限）。
// 返回值：快照列表。
func (s *Store) Snapshots(provider string, since time.Time) ([]Snapshot, error) {
	q := `SELECT provider,at,payload FROM snapshots WHERE provider=?`
	args := []any{provider}
	if !since.IsZero() {
		q += ` AND at >= ?`
		args = append(args, since.UnixMilli())
	}
	q += ` ORDER BY at, id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnapshots(rows)
}

// AllSnapshots 返回全部快照（导出用）。
//
// 返回值：快照列表。
func (s *Store) AllSnapshots() ([]Snapshot, error) {
	rows, err := s.db.Query(`SELECT provider,at,payload FROM snapshots ORDER BY at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnapshots(rows)
}

// scanSnapshots 读取快照结果集。
func scanSnapshots(rows *sql.Rows) ([]Snapshot, error) {
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		var at int64
		var p string
		if err := rows.Scan(&sn.Provider, &at, &p); err != nil {
			return nil, err
		}
		sn.At = time.UnixMilli(at)
		sn.Payload = json.RawMessage(p)
		out = append(out, sn)
	}
	return out, rows.Err()
}

// PruneSnapshots 删除早于 before 的快照。
//
// 参数 before：时间界限。
// 返回值：删除条数。
func (s *Store) PruneSnapshots(before time.Time) (int64, error) {
	r, err := s.db.Exec(`DELETE FROM snapshots WHERE at < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// ---- 全量导入导出支撑 ----

// Dataset 是可备份的全部数据。
type Dataset struct {
	Settings      model.Settings
	CursorEvents  []model.CursorEvent
	DeepSeekDaily []model.DeepSeekDaily
	Snapshots     []Snapshot
	// KV 保存最近一次的用量/余额等状态（键：cursor_usage / deepseek_balance）。
	KV map[string]json.RawMessage
}

// backupKVKeys 是随备份导出的 kv 键。
var backupKVKeys = []string{"cursor_usage", "deepseek_balance"}

// ReadDataset 读取全部可备份数据。
//
// 返回值：数据集。
func (s *Store) ReadDataset() (Dataset, error) {
	var ds Dataset
	var err error
	if ds.Settings, err = s.GetSettings(); err != nil {
		return ds, err
	}
	if ds.CursorEvents, err = s.CursorEvents(EventQuery{}); err != nil {
		return ds, err
	}
	if ds.DeepSeekDaily, err = s.DeepSeekDaily("", ""); err != nil {
		return ds, err
	}
	if ds.Snapshots, err = s.AllSnapshots(); err != nil {
		return ds, err
	}
	ds.KV = map[string]json.RawMessage{}
	for _, k := range backupKVKeys {
		var raw string
		err := s.db.QueryRow(`SELECT value FROM kv WHERE key=?`, k).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return ds, err
		}
		ds.KV[k] = json.RawMessage(raw)
	}
	return ds, nil
}

// ImportResult 汇总导入结果。
type ImportResult struct {
	EventsAdded    int  `json:"eventsAdded"`    // 新增的 Cursor 明细
	EventsSkipped  int  `json:"eventsSkipped"`  // 已存在而跳过的 Cursor 明细
	DailyUpserted  int  `json:"dailyUpserted"`  // 写入/更新的 DeepSeek 账单行
	SnapshotsAdded int  `json:"snapshotsAdded"` // 新增快照
	SettingsReplac bool `json:"settingsReplaced"`
}

// ImportDataset 在单个事务内导入数据集；任何一步失败整体回滚。
//
// 参数 ds：数据集；replace：true 先清空现有数据再导入（覆盖），false 合并去重。
// 返回值：导入结果。
func (s *Store) ImportDataset(ds Dataset, replace bool) (ImportResult, error) {
	var res ImportResult
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	fail := func(err error) (ImportResult, error) {
		tx.Rollback()
		return ImportResult{}, err
	}
	if replace {
		for _, t := range []string{"cursor_events", "deepseek_daily", "snapshots"} {
			if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
				return fail(err)
			}
		}
	}
	n, err := insertEvents(tx, ds.CursorEvents)
	if err != nil {
		return fail(err)
	}
	res.EventsAdded = n
	res.EventsSkipped = len(ds.CursorEvents) - n
	if err := upsertDaily(tx, ds.DeepSeekDaily); err != nil {
		return fail(err)
	}
	res.DailyUpserted = len(ds.DeepSeekDaily)
	existing := map[string]bool{}
	if !replace {
		rows, err := tx.Query(`SELECT provider,at,payload FROM snapshots`)
		if err != nil {
			return fail(err)
		}
		for rows.Next() {
			var p, pl string
			var at int64
			if err := rows.Scan(&p, &at, &pl); err != nil {
				rows.Close()
				return fail(err)
			}
			existing[fmt.Sprintf("%s|%d|%s", p, at, pl)] = true
		}
		rows.Close()
	}
	sort.SliceStable(ds.Snapshots, func(i, j int) bool { return ds.Snapshots[i].At.Before(ds.Snapshots[j].At) })
	for _, sn := range ds.Snapshots {
		k := fmt.Sprintf("%s|%d|%s", sn.Provider, sn.At.UnixMilli(), string(sn.Payload))
		if existing[k] {
			continue
		}
		existing[k] = true
		if _, err := tx.Exec(`INSERT INTO snapshots(provider,at,payload) VALUES(?,?,?)`, sn.Provider, sn.At.UnixMilli(), string(sn.Payload)); err != nil {
			return fail(err)
		}
		res.SnapshotsAdded++
	}
	// 设置：覆盖模式替换；合并模式也采用导入文件里的设置（用户明确选择导入）
	b, _ := json.Marshal(ds.Settings.Normalize())
	if _, err := tx.Exec(`INSERT INTO kv(key,value) VALUES('settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(b)); err != nil {
		return fail(err)
	}
	res.SettingsReplac = true
	for k, v := range ds.KV {
		ok := false
		for _, allowed := range backupKVKeys {
			if k == allowed {
				ok = true
			}
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, string(v)); err != nil {
			return fail(err)
		}
	}
	return res, tx.Commit()
}

// Wipe 清空所有业务数据（保留 schema 与版本）。
//
// 返回值：错误。
func (s *Store) Wipe() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, t := range []string{"cursor_events", "deepseek_daily", "snapshots"} {
		if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM kv WHERE key != 'schema_version'`); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
