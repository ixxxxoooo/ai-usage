# 自动更新

从 1.3.0 起，AI Usage 会自己检查并安装新版本，基于 [MyGo 的签名更新](https://github.com/egoist/mygo/blob/main/docs/updates.md)。

## 用户侧

- 每 6 小时在后台检查一次（启动约 10 秒后补一次到期的检查），发现新版本时弹出更新窗口：显示更新内容，可「安装更新」「稍后提醒」「跳过此版本」，勾选「以后自动下载并安装」后静默安装、下次启动生效。
- 手动检查：托盘右键「检查更新…」，或 设置 > 通用 > 软件更新 > 「检查更新」。设置里也可以关闭「自动检查更新」。
- 应用需要放在可写的位置（「应用程序」文件夹，或 `~/Applications`）；直接从 DMG 里运行、开发模式运行时不会检查。
- 1.2.0 及更早版本没有这个功能，需要手动安装一次 1.3.0。

## 工作原理

1. `mygo.json` 的 `updates` 里有**公钥**和仓库 `ixxxxoooo/ai-usage`。构建时公钥与清单地址
   `https://github.com/ixxxxoooo/ai-usage/releases/latest/download/update-darwin-universal.json` 被写进程序。
2. 发布时 CI 用**私钥**（仓库 Secret `MYGO_UPDATER_PRIVATE_KEY`）签名，`mygo build` 额外生成：
   - `ai-usage-<版本>-darwin-universal.tar.gz`：安装好的 .app 归档；
   - `ai-usage-<旧版本>-to-<版本>-darwin-universal.delta`：从前 3 个版本的增量包（有就生成）；
   - `update-darwin-universal.json`：清单（版本、更新说明、下载地址、大小、Ed25519 签名）。
3. 这些文件随 DMG 一起上传到 GitHub Release。正式版（不带 `-` 的 tag）会成为 latest，已安装的应用就能读到它的清单。
4. 应用下载后校验大小与签名，在 .app 旁边解包，再原子地替换原 .app，然后重启到新版本。

更新说明来自 `CHANGELOG.md` 里对应版本的 `## [x.y.z]` 一节，所以每次发版都必须先写好 CHANGELOG（发布流程也会检查）。

## 发版

```sh
# 1. 改版本号：mygo.json 的 version 与 internal/gui/actions.go 的 AppVersion
# 2. CHANGELOG.md 新增 ## [x.y.z] - 日期
git commit -am "chore: 发布 vx.y.z" && git tag vx.y.z && git push origin main vx.y.z
```

推送 tag 后 Release 工作流会测试、构建、签名并发布。

本地也可以构建带更新文件的包：

```sh
MYGO_UPDATER_PRIVATE_KEY="$(cat ~/Library/Application\ Support/mygo/update-keys/mygo-update.key)" make build-universal
```

## 密钥

- 私钥：`~/Library/Application Support/mygo/update-keys/mygo-update.key`（`go tool mygo keygen` 生成），同时存在 GitHub 仓库 Secret `MYGO_UPDATER_PRIVATE_KEY` 里。**不要提交进仓库，并另外备份一份（如密码管理器）**：已安装的应用只接受这把私钥签名的更新，丢了就只能让用户手动重装。
- 公钥：`mygo.json` 的 `updates.publicKey`，可以公开。
- 更换密钥会让旧版本无法自动更新到新版本，非必要不要换。
