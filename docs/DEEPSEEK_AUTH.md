# DeepSeek 登录是怎么获取的

> 作者：ixxxxoooo

账单、今日 Token、每日明细来自 `platform.deepseek.com` 的网页接口，需要网页登录凭据。

## 1. 这个接口到底要哪个「cookie」？

**不需要 Cookie。** 以 `GET /api/v0/usage/by_api_key/amount` 为例，实测（只读请求）：

| 请求带了什么 | 结果 |
| --- | --- |
| 只有 `authorization: Bearer <userToken>` | HTTP 200，返回账单数据 |
| 只有 Cookie（`smidV2`、`HWWAFSESID`、`HWWAFSESTIME` 等），没有 authorization | `{"code":40002,"msg":"Missing Token"}` |

所以鉴权靠的是请求头 `authorization: Bearer …` 里的那串 token。它就是浏览器 `localStorage` 里 `userToken` 的 `value`。
请求里的 Cookie 只是华为云 WAF 与设备指纹（`HWWAF*`、`smidV2`），和登录无关，应用也不会发送它们。

## 2. 手动获取（最稳）

1. 在浏览器登录 <https://platform.deepseek.com/usage>。
2. 按 `F12` → **Network** → 点开任意一条 `api/v0/...` 请求 → **Request Headers** → 复制 `authorization:` 后面 `Bearer ` 之后的那一串（约 64 个字符）。
   - 或者 **Application → Local Storage → `https://platform.deepseek.com` → `userToken`**，复制其中的 `value`。
3. 打开应用 → 设置 → DeepSeek → 平台登录 → 选「手动粘贴 token」→ 粘贴 → 保存 → 点「测试连接」。

粘贴时下面这些写法都会被自动整理成纯 token，不用手工删：纯 token、`Bearer xxx`、`authorization: Bearer xxx`（含 curl 的 `-H '…'` 行，甚至整条 curl）、localStorage 的 JSON（`{"value":"…"}`）。
如果粘的是 Cookie，会直接提示「这不是平台 token」，不会保存。

token 只保存在 macOS 钥匙串里。

## 3. 自动从 Chrome 读取的原理

选「自动读取 Chrome 登录」时，应用只读地做了这些事：

1. 在 `~/Library/Application Support/` 下找 Chromium 系浏览器的数据目录：Chrome、Chrome Beta、Edge、Brave、Chromium、Arc、Vivaldi。
2. 遍历每个 Profile（`Default`、`Profile 1`、`Profile 2` …）的 `Local Storage/leveldb` 目录。
3. 浏览器正在运行时数据库有锁，所以把该目录的文件 **复制到系统临时目录**（跳过 `LOCK`），在副本上以只读方式打开 LevelDB（日志写到一半时会尝试自动恢复），读完立即删除副本。浏览器原数据不会被修改。
4. 在副本里取键 `_https://platform.deepseek.com\x00\x01userToken`，值的第一个字节是编码标记（`0x01` = Latin-1，`0x00` = UTF-16LE），后面是 JSON `{"value":"<token>","__version":"0"}`，取出 `value`。
   - localStorage 是明文的，不像 Cookie 需要用钥匙串里的「Chrome Safe Storage」解密，所以读它不会弹钥匙串授权。
5. 多个 Profile / 浏览器里可能都有登录（其中一些已过期），应用会依次试用，直到有一个能调通接口；成功的来源会显示为 `Chrome / Profile 1` 这样的形式，并缓存在内存里，不落盘。收到 401 时会重新扫描再试。

### 为什么会显示「未登录」：macOS 权限

macOS 不允许普通应用读取其它应用（Chrome）的数据目录。从 Finder / Launchpad 启动本应用时，读取会失败：

```
open ~/Library/Application Support/Google/Chrome: operation not permitted
```

在终端里跑同样的代码却能读到，因为终端（或 Cursor）本身有「完全磁盘访问权限」，这会让人误以为应用也能读。

解决办法二选一：

- **授权**：系统设置 → 隐私与安全性 → 完全磁盘访问权限 → 添加并打开 AI Usage → 重启应用。设置页「自动读取需要授权」里有按钮可直接打开该页面。
  - 本应用是 ad-hoc 签名（没有 Apple 开发者证书），每次升级后系统可能把它当成新应用，需要重新授权。
- **手动粘贴 token**（见上）：不需要任何系统权限，升级也不受影响。选自动模式时，只要自动读取失败，已保存的手动 token 也会作为后备使用。

## 4. 自己排查

```bash
# 真实应用进程里的登录检查（用 open 启动，才能反映 Finder 启动时的权限），报告里只有脱敏信息
open -n "/Applications/AI Usage.app" --args --selftest=deepseek --selftest-out=/tmp/ds.json
sleep 20 && cat /tmp/ds.json

# 在终端里用本机浏览器登录做只读联调
go test -tags live -run 'TestLiveDeepSeek|TestLiveManualTokenFlow' -v ./internal/live
```

报告里的 `dsDiag.scanNotes` 会逐个列出每个浏览器 / Profile 的读取结果，`dsDiag.candidates` 会列出每个候选 token（脱敏）是否可用。
