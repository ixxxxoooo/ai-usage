# Cursor 统计修复与 Antigravity 额度（v1.2.0）

> 作者：ixxxxoooo ｜ 日期：2026-10-17

## 需求

1. Cursor 明细看起来重复，并且与官网远端不一致，查明是算法问题还是别的原因。
2. 增加 Antigravity（反重力）额度查询，并充分测试。

## 结论与改动

详见 `docs/CURSOR_DUPLICATES.md` 与 `docs/ANTIGRAVITY.md`。

- 根因：去重键包含 Token，而 Cursor 进行中的事件时间戳不变、Token 持续增长，每分钟刷新都会多存一份快照。
  真实数据核对：远端 8596 条，本机 9847 条，API 估值 $6850.72 vs $10036.78。
- 修复：键不含 Token + 原地更新 + 与远端对齐清理 + schema v3 迁移合并 + 导入合并。
- Antigravity：读本机 `language_server` 的 `GetUserStatus`，额度池归并，浮窗/概览/额度页/设置开关与测试连接。

## 测试

- 单元：model（键）、store（Sync、v2→v3 迁移）、transfer（旧快照合并）、service（原地更新、对齐清理、Antigravity 刷新/未运行/关闭/互不影响）、antigravity（解析、进程/端口解析、https/http、错误归类）、gui（浮窗/概览/额度页/设置）。
- 真实联调：`go test -tags live -run "TestLiveCursorNoDuplicates|TestLiveAntigravity" -v ./internal/live`。
