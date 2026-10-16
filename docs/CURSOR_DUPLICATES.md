# Cursor 明细重复问题（v1.2.0 修复）

> 作者：ixxxxoooo

## 现象

「Cursor 明细」里出现大量时间完全相同（精确到秒）、模型相同、次数都是 1，但 Token 与 API 估值各不相同的行；
本地统计的请求数、Token、API 估值也比官网 Dashboard 上的更大。

## 根因

Cursor 一次 Agent 运行（一个会话里连续调用工具）对应服务端的一条用量事件。这条事件的**时间戳在运行开始时就固定了**，
但它的 Token（尤其是缓存读）会随运行不断累加，直到运行结束。

旧版本的去重键是「时间 + 模型 + 会话 + 类型 + 各项 Token」的摘要。应用每分钟刷新一次、每次都会重新拉最近 2 天的明细，
于是同一条事件在运行过程中的每一个瞬时快照（Token 依次为 190、196、198…346）都被当成「新事件」写进库里：

| | 官网 / 接口 | 本机库（修复前） |
| --- | --- | --- |
| 同一次运行 | 1 条（最终 Token） | 34 条（每分钟一个快照） |
| 全量条数 | 8596 | 9847（多 1243 条） |
| API 估值合计 | $6850.72 | $10036.78 |

用真实账号核对：远端 8596 条事件里没有任何两条的（时间, 会话, 模型, 类型）相同，所以这四项足以唯一标识一条事件。

## 修复

1. **去重键只用「时间(毫秒) + 模型 + 会话 + 类型」**（`model.EventKey`），不含 Token。
2. **同一事件原地更新**：已存在的事件用最新值覆盖（`store.UpsertCursorEvents`），Token 增长时只改同一行。
3. **同步时与远端对齐**（`store.SyncCursorEvents`）：一次**完整**拉取后，删除拉取区间内本地有、远端没有的记录，清理历史重复与服务端修正。
   区间两端为开区间，不动端点；拉取不完整（条数少于接口总数的 99.5%）、结果为空、或中途失败时**绝不删除**本地数据。
4. **数据库 v3 迁移**：把旧库里同一事件的多份快照合并为一行（保留 Token 最多的，即最终状态），并换成新键；不丢数据、不需要重新同步。
5. **导入备份**：旧版本备份里的键含 Token，导入时一律重算键并合并同一事件的多份快照。

## 如何验证

```bash
# 远端条数与拉取条数一致、无重复事件（需要本机 Cursor 已登录，只读）
go test -tags live -run TestLiveCursorNoDuplicates -v ./internal/live

# 本地库条数应与远端总数一致
sqlite3 -readonly "$HOME/Library/Application Support/AI Usage/usage.db" "select count(*), sum(total_cents)/100 from cursor_events;"
```

自动化测试：`TestEventKeyIgnoresTokens`、`TestMigrateV2ToV3DedupesSnapshots`、`TestSyncCursorEventsReconciles`、
`TestInProgressEventNotDuplicated`、`TestSyncRemovesRemoteMissing`、`TestImportMergesLegacySnapshots`。
