// Package transfer 负责数据的导出与导入：JSON 完整备份、CSV 导出、校验与迁移。
//
// 设计要点：
//   - 备份默认不含任何凭据；只有显式勾选才会写入凭据，且导入时也要显式同意才会恢复。
//   - 写文件走「临时文件 + 原子改名」，导出过程中断不会留下半个文件。
//   - 导入先完整解析与校验，再在单个事务里写库（失败整体回滚）；覆盖导入前自动留一份安全备份。
//   - 版本：当前为 SchemaVersion；缺少版本号的旧文件按 v0 迁移（补默认值）；更新版本的文件拒绝并提示升级。
//
// @author ygw
package transfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/provider/cursor"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/store"
	"github.com/ixxxxoooo/ai-usage/internal/util"
)

// 备份格式常量。
const (
	Format        = "ai-usage-backup" // 文件标识
	SchemaVersion = 1                 // 当前备份格式版本
	MaxFileSize   = 256 << 20         // 允许导入的最大文件（256MB）
)

// maxFileSize 是实际生效的大小上限（测试里可调小）。
var maxFileSize int64 = MaxFileSize

// 可备份/恢复的凭据键。
var credentialKeys = []string{secret.KeyCursorCookie, secret.KeyDeepSeekKey, secret.KeyDeepSeekToken}

// 校验与导入过程中的可识别错误。
var (
	ErrNotBackup  = errors.New("这不是 AI Usage 的备份文件")
	ErrTooLarge   = errors.New("备份文件过大（超过 256MB），已拒绝导入")
	ErrEmptyFile  = errors.New("备份文件是空的")
	ErrNothingNew = errors.New("备份中没有可导入的有效数据")
)

// ErrNewerVersion 表示备份来自更新版本的应用。
type ErrNewerVersion struct{ Version int }

// Error 实现 error。
func (e ErrNewerVersion) Error() string {
	return fmt.Sprintf("备份来自更新版本的 AI Usage（格式 v%d，当前支持到 v%d），请先升级应用", e.Version, SchemaVersion)
}

// SnapshotRec 是备份里的一条原始快照。
type SnapshotRec struct {
	Provider string          `json:"provider"`
	At       time.Time       `json:"at"`
	Payload  json.RawMessage `json:"payload"`
}

// Backup 是完整备份文件的结构。
//
// @author ygw
type Backup struct {
	Format        string                     `json:"format"`
	SchemaVersion int                        `json:"schemaVersion"`
	ExportedAt    time.Time                  `json:"exportedAt"`
	AppVersion    string                     `json:"appVersion,omitempty"`
	Settings      *model.Settings            `json:"settings,omitempty"`
	CursorEvents  []model.CursorEvent        `json:"cursorEvents"`
	DeepSeekDaily []model.DeepSeekDaily      `json:"deepseekDaily"`
	Snapshots     []SnapshotRec              `json:"snapshots,omitempty"`
	KV            map[string]json.RawMessage `json:"kv,omitempty"`
	Credentials   map[string]string          `json:"credentials,omitempty"`
}

// ExportOptions 是导出选项。
type ExportOptions struct {
	AppVersion         string
	IncludeCredentials bool         // 是否把凭据（明文）写进备份
	Secrets            secret.Store // IncludeCredentials 时读取凭据
	Now                time.Time    // 导出时间（零值取当前）
}

// BuildBackup 从存储读取全部数据，组装成备份结构。
//
// 参数 st：存储；opt：导出选项。
// 返回值：Backup。
func BuildBackup(st *store.Store, opt ExportOptions) (*Backup, error) {
	ds, err := st.ReadDataset()
	if err != nil {
		return nil, fmt.Errorf("读取本地数据失败：%w", err)
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	settings := ds.Settings
	b := &Backup{
		Format: Format, SchemaVersion: SchemaVersion, ExportedAt: now.UTC(), AppVersion: opt.AppVersion,
		Settings: &settings, CursorEvents: ds.CursorEvents, DeepSeekDaily: ds.DeepSeekDaily, KV: ds.KV,
	}
	if b.CursorEvents == nil {
		b.CursorEvents = []model.CursorEvent{}
	}
	if b.DeepSeekDaily == nil {
		b.DeepSeekDaily = []model.DeepSeekDaily{}
	}
	for _, s := range ds.Snapshots {
		b.Snapshots = append(b.Snapshots, SnapshotRec{Provider: s.Provider, At: s.At, Payload: s.Payload})
	}
	if opt.IncludeCredentials && opt.Secrets != nil {
		b.Credentials = map[string]string{}
		for _, k := range credentialKeys {
			if v, err := opt.Secrets.Get(k); err == nil && !util.IsEmpty(v) {
				b.Credentials[k] = v
			}
		}
	}
	return b, nil
}

// WriteFile 把备份以 JSON 原子写入 path（先写同目录临时文件，再改名）。
//
// 参数 path：目标文件；b：备份。
// 返回值：错误。
func WriteFile(path string, b *Backup) error {
	data, err := json.MarshalIndent(b, "", " ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o600)
}

// atomicWrite 原子写文件：写临时文件、fsync、改名。
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败：%w", err)
	}
	tmp, err := os.CreateTemp(dir, ".aiusage-*.tmp")
	if err != nil {
		return fmt.Errorf("无法写入目标位置：%w", err)
	}
	name := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("写入失败：%w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("保存失败：%w", err)
	}
	return nil
}

// Preview 是导入前给用户确认的摘要。
//
// @author ygw
type Preview struct {
	Version        int       `json:"version"`
	Legacy         bool      `json:"legacy"` // 旧版（无版本号）文件，已自动迁移
	ExportedAt     time.Time `json:"exportedAt"`
	AppVersion     string    `json:"appVersion"`
	Events         int       `json:"events"`
	Daily          int       `json:"daily"`
	Snapshots      int       `json:"snapshots"`
	InvalidEvents  int       `json:"invalidEvents"`
	InvalidDaily   int       `json:"invalidDaily"`
	HasCredentials bool      `json:"hasCredentials"`
	HasSettings    bool      `json:"hasSettings"`
	Earliest       time.Time `json:"earliest"`
	Latest         time.Time `json:"latest"`
}

// Summary 返回给用户看的中文摘要。
//
// 返回值：多行文案。
func (p Preview) Summary() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Cursor 明细 %s 条，DeepSeek 账单 %s 行", util.FormatInt(int64(p.Events)), util.FormatInt(int64(p.Daily))))
	if !p.Earliest.IsZero() {
		parts = append(parts, fmt.Sprintf("时间范围 %s ~ %s", p.Earliest.Local().Format("2006-01-02"), p.Latest.Local().Format("2006-01-02")))
	}
	if !p.ExportedAt.IsZero() {
		parts = append(parts, "导出于 "+p.ExportedAt.Local().Format("2006-01-02 15:04"))
	}
	if p.InvalidEvents+p.InvalidDaily > 0 {
		parts = append(parts, fmt.Sprintf("另有 %d 条无效记录将被忽略", p.InvalidEvents+p.InvalidDaily))
	}
	if p.Legacy {
		parts = append(parts, "（旧版备份，已自动升级格式）")
	}
	if p.HasCredentials {
		parts = append(parts, "文件包含登录凭据")
	}
	return strings.Join(parts, "\n")
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// finite 判断浮点数是否是有限值。
func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// validEvent 判断明细是否合法，并补全缺失的去重键。
func validEvent(e *model.CursorEvent) bool {
	if e.Time.IsZero() || e.Time.Year() < 2000 || e.Time.After(time.Now().AddDate(1, 0, 0)) {
		return false
	}
	if !finite(e.Requests) || e.Requests < 0 || !finite(e.TotalCents) || !finite(e.ChargedCents) {
		return false
	}
	if e.InputTokens < 0 || e.OutputTokens < 0 || e.CacheRead < 0 || e.CacheWrite < 0 {
		return false
	}
	if e.Key == "" {
		e.Key = cursor.EventKey(*e)
	}
	return true
}

// validDaily 判断账单行是否合法。
func validDaily(d model.DeepSeekDaily) bool {
	if !dateRe.MatchString(d.Date) {
		return false
	}
	if _, err := time.Parse("2006-01-02", d.Date); err != nil {
		return false
	}
	if util.IsEmpty(d.Model) || !finite(d.Cost) || d.Cost < 0 {
		return false
	}
	return d.CacheHit >= 0 && d.CacheMiss >= 0 && d.Output >= 0 && d.Requests >= 0
}

// Parse 读取并校验备份内容，返回清洗后的备份与预览。
// 无效记录会被剔除并计数；格式不对、版本太新、内容为空都会返回错误。
//
// 参数 r：数据来源；
// 返回值：清洗后的 Backup、Preview、错误。
func Parse(r io.Reader) (*Backup, Preview, error) {
	var pv Preview
	data, err := io.ReadAll(io.LimitReader(r, maxFileSize+1))
	if err != nil {
		return nil, pv, fmt.Errorf("读取文件失败：%w", err)
	}
	if int64(len(data)) > maxFileSize {
		return nil, pv, ErrTooLarge
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, pv, ErrEmptyFile
	}
	var probe struct {
		Format        string `json:"format"`
		SchemaVersion int    `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, pv, fmt.Errorf("%w（不是有效的 JSON：%v）", ErrNotBackup, shortErr(err))
	}
	switch {
	case probe.Format == Format:
	case probe.Format == "" && probe.SchemaVersion == 0:
		pv.Legacy = true // 旧版文件：没有 format/schemaVersion 标记
	default:
		return nil, pv, ErrNotBackup
	}
	if probe.SchemaVersion > SchemaVersion {
		return nil, pv, ErrNewerVersion{Version: probe.SchemaVersion}
	}
	if probe.SchemaVersion < 0 {
		return nil, pv, ErrNotBackup
	}
	var b Backup
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, pv, fmt.Errorf("备份内容损坏：%v", shortErr(err))
	}
	if pv.Legacy && len(b.CursorEvents) == 0 && len(b.DeepSeekDaily) == 0 && b.Settings == nil {
		return nil, pv, ErrNotBackup // 旧版也要至少长得像备份
	}
	migrate(&b, pv.Legacy)
	pv.Version, pv.ExportedAt, pv.AppVersion = b.SchemaVersion, b.ExportedAt, b.AppVersion
	pv.HasCredentials = len(b.Credentials) > 0
	pv.HasSettings = b.Settings != nil

	evs := make([]model.CursorEvent, 0, len(b.CursorEvents))
	for _, e := range b.CursorEvents {
		if validEvent(&e) {
			evs = append(evs, e)
			if pv.Earliest.IsZero() || e.Time.Before(pv.Earliest) {
				pv.Earliest = e.Time
			}
			if e.Time.After(pv.Latest) {
				pv.Latest = e.Time
			}
		} else {
			pv.InvalidEvents++
		}
	}
	b.CursorEvents = evs
	rows := make([]model.DeepSeekDaily, 0, len(b.DeepSeekDaily))
	for _, d := range b.DeepSeekDaily {
		if validDaily(d) {
			rows = append(rows, d)
		} else {
			pv.InvalidDaily++
		}
	}
	b.DeepSeekDaily = rows
	snaps := b.Snapshots[:0]
	for _, s := range b.Snapshots {
		if (s.Provider == "cursor" || s.Provider == "deepseek") && !s.At.IsZero() && json.Valid(s.Payload) {
			// 备份文件是缩进的 JSON，库里存的是紧凑 JSON：统一压缩，否则合并导入会因空白不同而重复。
			var cb bytes.Buffer
			if json.Compact(&cb, s.Payload) == nil {
				s.Payload = cb.Bytes()
			}
			snaps = append(snaps, s)
		}
	}
	b.Snapshots = snaps
	pv.Events, pv.Daily, pv.Snapshots = len(b.CursorEvents), len(b.DeepSeekDaily), len(b.Snapshots)
	if pv.Events == 0 && pv.Daily == 0 && len(b.Snapshots) == 0 && b.Settings == nil {
		return nil, pv, ErrNothingNew
	}
	return &b, pv, nil
}

// shortErr 截断过长的底层错误文案。
func shortErr(err error) string {
	s := err.Error()
	if r := []rune(s); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

// migrate 把旧版本的备份升级到当前结构：补齐标记、规范设置。
func migrate(b *Backup, legacy bool) {
	if legacy || b.SchemaVersion == 0 {
		b.Format, b.SchemaVersion = Format, SchemaVersion
	}
	if b.Settings != nil {
		n := b.Settings.Normalize()
		b.Settings = &n
	}
}

// ReadFile 打开并解析备份文件。
//
// 参数 path：文件路径。
// 返回值：Backup、Preview、错误。
func ReadFile(path string) (*Backup, Preview, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, Preview{}, fmt.Errorf("无法打开文件：%w", err)
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.IsDir() {
		return nil, Preview{}, errors.New("选择的是文件夹，请选择备份文件")
	}
	return Parse(f)
}

// ImportOptions 是导入选项。
type ImportOptions struct {
	Replace            bool         // true 覆盖（先清空本地数据），false 合并去重
	ApplySettings      bool         // 是否采用备份里的设置（默认保留本机设置）
	RestoreCredentials bool         // 是否恢复备份里的凭据（需用户明确同意）
	Secrets            secret.Store // RestoreCredentials 时写入
	BackupDir          string       // 覆盖导入前把现有数据备份到此目录；为空则不做
	AppVersion         string
}

// Result 是导入结果。
type Result struct {
	store.ImportResult
	CredentialsRestored int    `json:"credentialsRestored"`
	SafetyBackup        string `json:"safetyBackup,omitempty"` // 覆盖前自动留的安全备份路径
}

// Import 把备份写入存储。覆盖模式会先做一份安全备份；整个写库在一个事务里，失败整体回滚。
//
// 参数 st：存储；b：已通过 Parse 校验的备份；opt：导入选项。
// 返回值：导入结果与错误。
func Import(st *store.Store, b *Backup, opt ImportOptions) (Result, error) {
	var res Result
	if b == nil {
		return res, ErrNothingNew
	}
	if opt.Replace && opt.BackupDir != "" {
		path, err := SafetyBackup(st, opt.BackupDir, opt.AppVersion)
		if err != nil {
			return res, fmt.Errorf("覆盖前自动备份失败，已取消导入：%w", err)
		}
		res.SafetyBackup = path
	}
	cur, err := st.GetSettings()
	if err != nil {
		return res, err
	}
	ds := store.Dataset{
		Settings: cur, CursorEvents: b.CursorEvents, DeepSeekDaily: b.DeepSeekDaily, KV: map[string]json.RawMessage{},
	}
	if opt.ApplySettings && b.Settings != nil {
		ds.Settings = b.Settings.Normalize()
	}
	for _, s := range b.Snapshots {
		ds.Snapshots = append(ds.Snapshots, store.Snapshot{Provider: s.Provider, At: s.At, Payload: s.Payload})
	}
	for k, v := range b.KV {
		if opt.Replace || newerState(st, k, v) {
			ds.KV[k] = v
		}
	}
	ir, err := st.ImportDataset(ds, opt.Replace)
	if err != nil {
		return res, fmt.Errorf("写入本地数据库失败（已回滚，本机数据没有变化）：%w", err)
	}
	res.ImportResult = ir
	res.SettingsReplac = opt.ApplySettings && b.Settings != nil
	if opt.RestoreCredentials && opt.Secrets != nil {
		for _, k := range credentialKeys {
			if v := b.Credentials[k]; !util.IsEmpty(v) {
				if err := opt.Secrets.Set(k, v); err == nil {
					res.CredentialsRestored++
				}
			}
		}
	}
	return res, nil
}

// newerState 判断备份里的某个「最近状态」是否比本地更新（合并时避免用旧状态覆盖新状态）。
func newerState(st *store.Store, key string, raw json.RawMessage) bool {
	var in struct {
		FetchedAt time.Time `json:"fetchedAt"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return false
	}
	var cur struct {
		FetchedAt time.Time `json:"fetchedAt"`
	}
	ok, _ := st.GetJSON(key, &cur)
	return !ok || in.FetchedAt.After(cur.FetchedAt)
}

// SafetyBackup 把当前全部数据备份到 dir（不含凭据），并只保留最近 5 份。
//
// 参数 st：存储；dir：备份目录；appVersion：应用版本。
// 返回值：备份文件路径与错误。
func SafetyBackup(st *store.Store, dir, appVersion string) (string, error) {
	b, err := BuildBackup(st, ExportOptions{AppVersion: appVersion})
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "pre-import-"+time.Now().Format("20060102-150405.000")+".json")
	if err := WriteFile(path, b); err != nil {
		return "", err
	}
	pruneBackups(dir, 5)
	return path, nil
}

// pruneBackups 只保留目录里最新的 keep 份 pre-import 备份。
func pruneBackups(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "pre-import-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
