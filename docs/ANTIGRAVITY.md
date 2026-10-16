# Antigravity（反重力）额度查询

> 作者：ixxxxoooo

查询 Google Antigravity IDE 里各模型的剩余额度与重置时间，展示在浮窗、概览页与「Antigravity 额度」页。

## 原理

Antigravity 运行时会拉起一个本机语言服务进程（`.../Contents/Resources/bin/language_server`），它只监听 `127.0.0.1`，
并用启动参数 `--csrf_token` 校验请求。额度可以从它的 `GetUserStatus` 接口读到：

1. `ps -axo pid=,command=` 找到命令行同时含 `language_server` 与 `antigravity`、并带 `--csrf_token` 的进程。
2. `lsof -nP -a -p <pid> -iTCP -sTCP:LISTEN -Fn` 取它监听的端口（通常有一个 https 与一个 http）。
3. 向每个端口 `POST /exa.language_server_pb.LanguageServerService/GetUserStatus`（先 https，再 http；https 证书是自签的，
   因为只访问回环地址所以跳过校验），带 `X-Codeium-Csrf-Token` 与 `Connect-Protocol-Version: 1`。
4. 解析 `userStatus.cascadeModelConfigData.clientModelConfigs[]` 里每个模型的 `quotaInfo.remainingFraction`（剩余比例）与 `resetTime`。

**不需要任何账号凭据，不访问外网，不写入 Antigravity 的任何数据。** 因此 Antigravity 没有运行时取不到数据，
界面标记「未运行」并保留上次的额度。

## 额度池

同一组的模型共用一份额度，它们的（剩余比例, 重置时间）完全一致。应用按这两个值把模型归并成「额度池」，
组名取各模型厂商名（展示名第一个单词）去重后连接，例如：

| 额度池 | 模型 |
| --- | --- |
| `Claude / GPT-OSS` | Claude Opus 4.6 (Thinking)、Claude Sonnet 4.6 (Thinking)、GPT-OSS 120B (Medium) |
| `Gemini` | Gemini 3.8 Flash (High / Medium / Low) |

规则与细节：

- 剩余 100% 时服务端给的重置时间只是「现在 + 滚动窗口」的占位值，界面显示「额度充足」而不是倒计时。
- 有重置时间但没有剩余比例，视为额度已耗尽（服务端省略了 0）。
- `disabled` 的模型（当前套餐不可用）只在额度页的「全部模型」里以「不可用」列出，不参与额度池。
- 级别颜色：已用 ≥ 80% 黄色，≥ 95% 红色。

## 设置

设置 → Antigravity（反重力）：

- **查询额度**开关（默认开）。关闭后不再发任何请求，浮窗高度恢复为两张卡片的大小，侧边栏与概览页不再显示。
  设置字段是 `antigravityOff`（用「关闭」而不是「开启」，让旧设置与旧备份默认保持开启）。
- **测试连接**：立即读一次，显示套餐与有额度信息的模型数；没运行时提示「未检测到正在运行的 Antigravity，请先打开它」。

## 排查

```bash
# 需要 Antigravity 正在运行（只读）
go test -tags live -run TestLiveAntigravity -v ./internal/live
```

没有数据时：确认 Antigravity 应用已打开并登录；`ps -axo command= | grep language_server | grep -i antigravity` 能看到进程；
`lsof -nP -a -p <pid> -iTCP -sTCP:LISTEN` 有监听端口。
