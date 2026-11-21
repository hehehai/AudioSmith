# Contributing to AudioSmith

[简体中文](#简体中文) below.

Thank you for helping. Bug reports, ideas, translations and pull requests are all welcome.

- **Bugs:** Settings → Diagnostics and about → Report a bug opens an issue with the diagnostics filled in (no text, audio or keys). Otherwise [open an issue](https://github.com/hehehai/AudioSmith/issues/new) and say what happened, what you expected and how to reproduce it.
- **Larger changes** (a new feature, a new model, a change of the engine protocol): open an issue first, so we can agree on the approach before you spend time on it.
- **Pull requests:** one topic each, against `main`, with tests for what you change. Describe what changes for the user and how you checked it; for a change of the UI, add before/after screenshots.

## Setting up

AudioSmith runs on macOS 15 or later with Apple silicon. Building the app needs Xcode 26 (Swift 6.2) and Go 1.27.3 (`GOTOOLCHAIN=auto` downloads it); see [Build from source](README.md#build-from-source). The Go side, including the whole UI, also builds and tests headless on Linux, with the engine faked. Read [docs/architecture.md](docs/architecture.md) before a larger change: it says where things live and the rules the code follows. The app itself (package main: the UI and the wiring) is in `app/`, the files it embeds in `assets/`, everything else in `internal/`; `go run ./app` runs it from the repository root, `go tool mygo dev` with hot reload.

## Checks

Run these before you push; CI runs the same on Linux:

```sh
CGO_ENABLED=0 go test -p 4 ./...                     # everything, the UI headless
go tool mygo vet .                                    # MyGo's checks of the UI code
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./... # the Mac build compiles
gofmt -l . | grep -v ^engine/                         # prints nothing
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12   # if you change a workflow
scripts/snapshots.sh check                            # renders the UI before and after your change and lists the differences
```

Also:

- **Text the user sees** goes through `i18n.T` in English, with its Chinese translation in `internal/i18n/locales/zh-Hans.json` (`TestKeyParity` checks that every key has one).
- **File sizes:** a Go file stays under 600 lines, a test file under 800 (`internal/archtest`); split a growing file by topic.
- **Dependencies:** `THIRD-PARTY-NOTICES.txt`, which the app ships, has the licence of every third-party component. After you add, remove or update one (a Go module, a Swift package in `engine/Package.resolved`, the mlx-audio-swift pin, MyGo, RapiDoc, the voice previews), run `scripts/third-party-notices.sh` (with `--fetch` after a Swift pin moved) and commit the result; `TestNoticesCurrent` fails until you do, and the generator asks for the licence of a component it does not know yet. Only add dependencies whose licence is compatible with the AGPL-3.0.
- **The engine** (Swift, `engine/`) builds and tests on a Mac only: `scripts/build-engine.sh`, `scripts/test-engine.sh`.
- Don't commit build outputs, downloaded models or secrets.

## Licence of contributions and the CLA

AudioSmith is licensed under the [GNU Affero General Public License, version 3](LICENSE) (AGPL-3.0). Before we can merge your first pull request, you sign the [Contributor License Agreement](CLA.md) (CLA), once:

- You keep the copyright in your work. You license it to the project, which releases it under the AGPL-3.0 and may also release it under other terms, such as a commercial licence or an app-store build. The CLA also promises that versions with your contribution stay available as free software.
- To sign, comment on your pull request with exactly: `I have read the CLA Document and I hereby sign the CLA`. The CLA bot asks you to when you open the pull request, and records your GitHub user name in the project's public signature file. Every author of the pull request's commits signs; link your commits to your GitHub account (commit with an e-mail address registered there).
- If your employer may own what you write, make sure they agree (CLA section 5), or ask for a corporate agreement at riverhohai@gmail.com.

Comment `recheck` if the bot's check doesn't update.

## For maintainers

The CLA workflow (`.github/workflows/cla.yml`) stores signatures in `signatures/version1/cla.json` on the `cla-signatures` branch, which must exist before the first outside pull request:

```sh
git switch --orphan cla-signatures
git commit --allow-empty -m "CLA signatures"
git push origin cla-signatures
git switch main
```

The bot commits to that branch with the workflow's token: don't require pull requests or reviews on it; do block force pushes and deletion (a branch ruleset). A new version of CLA.md that should be signed again goes with a new `path-to-signatures` (`signatures/version2/cla.json`).

---

## 简体中文

感谢参与！欢迎报告问题、提建议、补翻译和提交 PR。

- **报告问题：** 设置 → 诊断与关于 → 报告问题，会打开一个已填好诊断信息（不含文字、音频和密钥）的 issue；也可以直接[新建 issue](https://github.com/hehehai/AudioSmith/issues/new)，写清发生了什么、你期望什么、怎么复现。
- **较大的改动**（新功能、新模型、改引擎协议）请先开 issue 讨论方案。
- **PR：** 一个 PR 只做一件事，基于 `main`，并附上测试；说明对用户有什么变化、你是怎么验证的；改界面请附前后对比截图。

**代码位置：** 应用本身（package main：界面和装配）在 `app/`，它嵌入的文件在 `assets/`，其余在 `internal/`；在仓库根目录用 `go run ./app` 运行，`go tool mygo dev` 带热重载。

**提交前运行检查：** 见上面 [Checks](#checks) 的命令（`go test`、`go tool mygo vet .`、macOS 交叉编译、`gofmt`，改了 workflow 再跑 actionlint，改了界面跑 `scripts/snapshots.sh check`）。界面文字用英文写在 `i18n.T` 里，并在 `internal/i18n/locales/zh-Hans.json` 补上中文。增删或升级依赖（Go 模块、`engine/Package.resolved` 里的 Swift 包、mlx-audio-swift 的版本、MyGo、RapiDoc、试听音频）后，运行 `scripts/third-party-notices.sh`（Swift 依赖版本变了加 `--fetch`）并提交生成的 `THIRD-PARTY-NOTICES.txt`，否则 `TestNoticesCurrent` 会失败；只引入与 AGPL-3.0 兼容的许可。

**许可证与 CLA：** AudioSmith 采用 [GNU AGPL-3.0](LICENSE) 许可。你的第一个 PR 合并前，需要签署一次[贡献者许可协议（CLA）](CLA.md)：

- 你保留自己作品的版权，并授权本项目使用：项目以 AGPL-3.0 发布，也可以用其他条款（例如商业许可、App Store 版本）发布。CLA 同时承诺，包含你贡献的版本始终以自由软件形式提供源代码。
- 签署方法：在你的 PR 里留言，内容与下面这句完全一致：`I have read the CLA Document and I hereby sign the CLA`。CLA 机器人会提示你，并把你的 GitHub 用户名记录在公开的签名文件里。PR 中每个提交的作者都要签署；提交邮箱需要关联到你的 GitHub 账号。
- 如果你的雇主可能拥有你写的代码，请先取得雇主同意（CLA 第 5 条），或发邮件到 riverhohai@gmail.com 申请公司版协议。

CLA 的英文原文是唯一有效文本；本节只是说明。
