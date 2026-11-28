# 发布 AudioSmith（Windows：打包、Azure Trusted Signing、自动更新与官网接入）

macOS 发版指南：[release.zh-CN.md](release.zh-CN.md) · Windows 本地构建与调试：[windows-build.md](windows-build.md)

Windows 发版使用**独立工作流** [`.github/workflows/release-windows.yml`](../.github/workflows/release-windows.yml)，与 macOS 的 [`release.yml`](../.github/workflows/release.yml) 解耦：

- 推送 `v<版本号>` 标签时，macOS 和 Windows 两条工作流并行运行；
- **安全隔离**：如果尚未配置 Azure Trusted Signing 的 GitHub Secrets，Windows 工作流会自动降级为**未签名构建**并输出明确提示，绝不会阻塞 macOS 正式发版；
- 支持随时通过 **Actions → Release (Windows) → Run workflow** 手动触发干跑（dry run），验证打包产物、校验和与签名流程。

---

## 1. 产物清单与命名规范

一次完整的 Windows 发版会在 `build/windows-release/` 生成以下文件：

| 文件名 | 说明 |
| --- | --- |
| `AudioSmith-<版本>-windows-amd64-setup.exe` | Windows x64 NSIS 用户级安装包（安装到 `%LOCALAPPDATA%\Programs\AudioSmith`，无需管理员 UAC，注册 `.asvoice`/`.ovsvoice` 与 `audiosmith://`） |
| `AudioSmith-<版本>-windows-amd64-portable.zip` | Windows x64 绿色便携包（解压即用） |
| `AudioSmith-<版本>-windows-arm64-portable.zip` | Windows 11 ARM64 绿色便携包（由 `windows-latest` 交叉编译生成） |
| `SHA256SUMS-windows.txt` | 上述 3 个发布包的统一 SHA-256 校验和（LF 换行，兼容 `sha256sum -c` 与 PowerShell） |
| `update-windows-amd64.json` | MyGo 自动更新清单（仅在仓库已配置 `MYGO_UPDATER_PUBLIC_KEY` 与 `MYGO_UPDATER_PRIVATE_KEY` 时生成） |
| `audiosmith-<版本>-windows-amd64.tar.gz` | MyGo 自动更新归档包（含 Ed25519 签名，配合 `update-windows-amd64.json` 使用） |

便携包（`*-portable.zip`）内部目录结构干净自包含，直接解压后双击 `AudioSmith.exe` 即可运行：

```text
AudioSmith.exe
Sqlitecore.dll
engine/audiosmith-engine.exe
LICENSE
NOTICE
THIRD-PARTY-NOTICES.txt
```

> **引擎定位**：`scripts/build-win.ps1` 与 `scripts/build-win.sh` 先把 `audiosmith-engine.exe` 编译到 `resources/windows-<arch>/engine/audiosmith-engine.exe`，`go tool mygo build` 打包后放入 `build/windows-<arch>/engine/audiosmith-engine.exe`。运行时 `internal/engine/find.go` 会自动从可执行文件同级目录的 `engine\audiosmith-engine.exe` 启动引擎。

---

## 2. 一次性准备：配置 Azure Trusted Signing

Azure Trusted Signing（Azure Portal 中现也称 **Artifact Signing**）提供云端短生命周期代码签名证书，新签名的可执行文件与安装包可快速建立 Microsoft SmartScreen 信任，且无需购买和维护硬件 USB EV 加密狗。

### 第 1 步：在 Azure Portal 创建签名账号与证书配置

1. 登录 [Azure Portal](https://portal.azure.com/)，搜索并进入 **Artifact Signing**（或 **Trusted Signing Accounts**），点击 **Create**：
   - **Region**：选择支持的区域（例如 `East US`、`West US 2`、`North Europe`），记下创建后的 **Account URI / Endpoint**（例如 `https://eus.codesigning.azure.net/`）；
   - **Account Name**：例如 `actnow-signing`；
   - **Pricing tier**：Basic（基础版即可满足个人/小团队发版需求）。
2. 在新建的账号中打开 **Identity validations** → **New identity**（选择 Individual 或 Organization），提交身份核验材料并等待审批通过（通常 1–3 个工作日）。
3. 身份核验通过后，打开 **Certificate profiles** → **Create** → 选择 **Public Trust**：
   - 填写 **Certificate profile name**（例如 `audiosmith-public-trust`），关联已通过的 Identity validation。

### 第 2 步：创建 Entra ID 服务主体并授权

1. 在 Azure Portal 进入 **Microsoft Entra ID** → **App registrations** → **New registration**，创建应用（例如 `audiosmith-github-signing`）。
2. 在应用概览页记下：
   - **Directory (tenant) ID** → `AZURE_TENANT_ID`
   - **Application (client) ID** → `AZURE_CLIENT_ID`
3. 进入 **Certificates & secrets** → **Client secrets** → **New client secret**，生成密钥并记下 **Value** → `AZURE_CLIENT_SECRET`（注意设置到期提醒）。
4. 回到第 1 步创建的 Trusted Signing Account → **Access control (IAM)** → **Add role assignment**：
   - **Role**：选择 **Trusted Signing Certificate Profile Signer**（如使用新角色名则为 **Artifact Signing Certificate Profile Signer**）；
   - **Members**：选择刚创建的 `audiosmith-github-signing` 服务主体并保存。

### 第 3 步：配置 GitHub Secrets

在仓库根目录执行（需先 `gh auth login`）：

```sh
gh secret set AZURE_TENANT_ID          # Entra ID Directory (tenant) ID
gh secret set AZURE_CLIENT_ID          # App registration Application (client) ID
gh secret set AZURE_CLIENT_SECRET      # Client secret Value
gh secret set AZURE_SIGNING_ENDPOINT   # 例如 https://eus.codesigning.azure.net/
gh secret set AZURE_SIGNING_ACCOUNT    # Trusted Signing Account 名称
gh secret set AZURE_SIGNING_PROFILE    # Public Trust Certificate Profile 名称
```

同时确保自动更新密钥已配置（与 macOS 共用同一对 Ed25519 密钥，若已按 [release.zh-CN.md](release.zh-CN.md) 第 4 步配置则无需重复操作）：

- Secret：`MYGO_UPDATER_PRIVATE_KEY`（`mygo-update.key` 内容）
- Variable：`MYGO_UPDATER_PUBLIC_KEY`（`mygo-update.pub` 内容）

### 为什么 CI 使用 `signtool` + `Azure.CodeSigning.Dlib.dll`（而非仅在构建后跑 `azure/trusted-signing-action@v0`）

NSIS 在编译安装包时会动态生成内置的 `Uninstall.exe`。MyGo 通过 `windows.signCommand` 挂钩 NSIS 的 `!uninstfinalize`，在打包阶段一次性签完：

1. `AudioSmith.exe`
2. `engine\audiosmith-engine.exe`
3. `Sqlitecore.dll`
4. NSIS 内置卸载器 `Uninstall.exe`
5. 最终安装包 `AudioSmith-<版本>-windows-amd64-setup.exe`

工作流在检测到上述 6 个 Secrets 全部就绪后，通过 `nuget install Microsoft.Trusted.Signing.Client` 获取 `Azure.CodeSigning.Dlib.dll`，`DefaultAzureCredential` 自动读取 `AZURE_TENANT_ID` / `AZURE_CLIENT_ID` / `AZURE_CLIENT_SECRET` 鉴权，并由 `scripts/build-win.ps1` 临时注入 `windows.signCommand`（退出时自动还原 `mygo.json`，绝不污染仓库）。

> 如果仅需对独立文件补签，也可以在自定义步骤中直接调用 `azure/trusted-signing-action@v0`；但在标准发版链路中，走 `windows.signCommand` 才能保证 `Uninstall.exe` 同步签名。

---

## 3. 接入 `actnow.dev` 官网所需的 3 处改动

当前 AudioSmith 仓库中 vendored 的 [`scripts/actnow-publish.sh`](../scripts/actnow-publish.sh)（受 `TestActnowPublishVendored` 的 SHA-256 锁定）与官网 `actnow-www` 仅接收 macOS 产物（`*.dmg` + `darwin-*`）。因此现阶段 Windows 产物先走 GitHub Release 分发。

后续在 `actnow-www` 仓库开启 Windows `.exe` 与 `update-windows-amd64.json` 接收时，只需做以下 **3 处明确改动**：

### 改动 1：`actnow-www/packages/common/src/releases.ts`（平台与产物校验规则）

1. **`ARTIFACT_KINDS` 与 `RELEASE_PLATFORMS`**：
   - 在 `RELEASE_PLATFORMS` 中新增 `"windows-amd64"`、`"windows-arm64"`；
   - 在 `ARTIFACT_KINDS` 中新增 `"exe"`（或 `"installer"`，用于区分 NSIS `.exe` 安装包与 macOS `.dmg`）。
2. **文件识别与命名检查**：
   - `inferArtifactKind(filename)`：增加 `/\.exe$/i.test(filename)` → 返回 `"exe"`（portable `.zip` 可归入 `"other"` 或独立 `"portable"`，避免被误判为更新归档 `archive`）；
   - `inferArtifactPlatform(filename)`：增加 `.exe` 平台推断（`*-windows-amd64-setup.exe` / `*-amd64.exe` → `"windows-amd64"`，`*-windows-arm64-setup.exe` / `*-arm64.exe` → `"windows-arm64"`）；
   - `checkArtifactName(...)`：增加 `case "exe"` 校验（包含版本号、以 `.exe` 结尾、无空格）。
3. **App 配置与数据库**：
   - 在 `DEFAULT_RELEASE_SETTINGS.audiosmith.platforms` 中加入 `"windows-amd64"`（并在 D1 `app_release_settings` 表的 `audiosmith` 行或后台 Panel 中更新 `platforms` 字段）。

### 改动 2：`actnow-www/scripts/actnow-publish.sh`（发布脚本 + 重新 vendor）

1. 将文件扫描 `find` 规则从 `*.dmg | *.tar.gz | *.delta | update-*.json` 扩展为同时包含 `*.exe`（按需包含 `*-portable.zip` 与 `SHA256SUMS-windows.txt`）；
2. 在平台推断 `case` 中增加 `*.exe` 分支（匹配 `*-amd64*.exe` → `windows-amd64`、`*-arm64*.exe` → `windows-arm64`）；
3. 官网上线后，在 AudioSmith 仓库执行：
   ```sh
   scripts/vendor-actnow-publish.sh
   ```
   核对 diff，更新 `scripts/release_publish_test.go` 中 `TestActnowPublishVendored` 的 SHA-256 常量，并在 `release-windows.yml` 中增加调用 `actnow-publish.sh` 的步骤。

### 改动 3：`actnow-www/packages/api` 与 `apps/web`（下载页与更新源路由）

1. **更新源 `/updates/audiosmith/(beta/)update-windows-amd64.json`**：现有 `/updates/...` 路由已按 `update-<platform>.json` 动态提供清单与 `.tar.gz`/`.delta` 归档，只需在拦截规则中与 `.dmg` 一样禁止从 `/updates/` 直接下载 `.exe`（返回 404）；
2. **下载页与下载接口 `/download/audiosmith/file`**：扩展支持 `?platform=windows-amd64`（返回带 `Content-Disposition: attachment; filename="AudioSmith-<版本>-windows-amd64-setup.exe"` 的 R2 签名下载链接），在下载页同时展示 macOS 与 Windows 下载按钮，并按需提供 `windows-arm64-portable.zip`。

---

## 4. 干跑（Dry Run）、发版与验证流程

### 4.1 在 GitHub Actions 触发干跑

无需打标签、也不会改动任何 GitHub Release 或官网：

1. 打开 GitHub 仓库 → **Actions** → **Release (Windows)** → **Run workflow**；
2. 参数设置：
   - `tag`：**留空**（纯干跑，仅上传 Workflow Artifacts，保留 7 天）；
   - `sign`：未配 Azure Secrets 时选 `false`（即使误选 `true`，缺少 Secrets 也会自动降级为未签名并给出 notice）；配好 Secrets 后选 `true` 验证真实签名。
3. 运行完成后，在 Run 页面下载 `AudioSmith-<版本>-windows-<signed|unsigned>` 压缩包进行实机测试。

### 4.2 正式发版与补挂到草稿 Release

- **推送标签发版**：推送 `v<版本>` 标签后，macOS `release.yml` 与 Windows `release-windows.yml` 会同时启动。
- **时序说明**：Windows 构建通常只需 5–10 分钟，早于 macOS（Swift 编译 + Apple 公证约 30–60 分钟）。如果 Windows 结束时 macOS 尚未创建草稿 Release，`release-windows.yml` 会把所有 Windows 产物安全保存在当次 Run 的 Artifacts（保留 30 天）中并输出提示。
- 等 macOS 创建好草稿 Release 后，二选一补挂 Windows 产物：
  1. 在 GitHub Actions 手动运行 **Release (Windows)**，填入 `tag: v<版本>`、`sign: true`，工作流会自动检测已存在的草稿 Release 并上传全部 Windows 文件（`update-windows-amd64.json` 最后上传）；
  2. 或本地下载 Run Artifact 后执行：
     ```sh
     gh release upload v<版本> --clobber \
       AudioSmith-<版本>-windows-amd64-setup.exe \
       AudioSmith-<版本>-windows-amd64-portable.zip \
       AudioSmith-<版本>-windows-arm64-portable.zip \
       SHA256SUMS-windows.txt
     # 如已生成更新清单，最后上传：
     gh release upload v<版本> --clobber update-windows-amd64.json
     ```

### 4.3 本地打包命令速查

在 Windows（PowerShell）上：

```powershell
# 同时构建 x64 NSIS 安装包 + 便携包 + SHA256SUMS-windows.txt（未签名）
.\scripts\build-win.ps1 -Arch amd64 -Installer -Portable -NoSign

# 仅构建 ARM64 便携包 + SHA256SUMS-windows.txt
.\scripts\build-win.ps1 -Arch arm64 -Portable -NoSign
```

在 Linux / macOS 上交叉编译便携包（无需 `makensis`）：

```bash
scripts/build-win.sh --arch amd64 --portable --no-sign
scripts/build-win.sh --arch arm64 --portable --no-sign
```

### 4.4 验证校验和、数字签名、SmartScreen 与自动更新

1. **校验 SHA-256**：
   ```powershell
   # PowerShell
   Get-Content SHA256SUMS-windows.txt | ForEach-Object {
     $expected, $file = $_ -split '\s+', 2
     $actual = (Get-FileHash -Algorithm SHA256 $file).Hash.ToLowerInvariant()
     if ($actual -ne $expected) { throw "MISMATCH: $file" }
     Write-Host "OK: $file"
   }
   ```
   （Linux / macOS / Git Bash 可直接运行 `sha256sum -c SHA256SUMS-windows.txt`。）

2. **验证 Authenticode 签名**（已签名构建）：
   ```powershell
   signtool verify /pa /v AudioSmith-<版本>-windows-amd64-setup.exe
   signtool verify /pa /v "$env:LOCALAPPDATA\Programs\AudioSmith\AudioSmith.exe"
   signtool verify /pa /v "$env:LOCALAPPDATA\Programs\AudioSmith\engine\audiosmith-engine.exe"
   signtool verify /pa /v "$env:LOCALAPPDATA\Programs\AudioSmith\Uninstall.exe"
   ```
   确认全部返回 `Successfully verified`，且时间戳有效。

3. **测试 Windows Defender SmartScreen**：
   - 在一台干净的 Windows 10/11 测试机（普通核显 / CPU 笔记本或 ARM64 虚拟机均可）通过浏览器下载安装包（保留 Mark-of-the-Web `Zone.Identifier` 标记）；
   - 双击运行 `AudioSmith-<版本>-windows-amd64-setup.exe`：
     - 使用 Azure Trusted Signing 的 Public Trust 证书时，应显示发布者名称且不出现红色阻断；
     - 未签名干跑包会弹出「Windows 已保护你的电脑」，点击「更多信息 → 仍要运行」用于内部测试。

4. **测试自动更新（`update-windows-amd64.json`）**：
   - 确认构建日志中 `MYGO_UPDATER_PUBLIC_KEY` 已生效，且 `update-windows-amd64.json` 结构如下：
     ```json
     {
       "version": "0.5.0-beta.6",
       "notes": "...",
       "url": "https://actnow.dev/updates/audiosmith/beta/audiosmith-0.5.0-beta.6-windows-amd64.tar.gz",
       "size": 12345678,
       "signature": "<base64-ed25519-signature>"
     }
     ```
   - 在已安装旧版本的 Windows 机器上，打开 AudioSmith → **设置 → 检查更新**（Beta 版本需开启「接收 Beta 版更新」）；
   - 确认应用能拉取清单、校验 Ed25519 签名、下载归档并完成原地替换重启；重启后检查 `engine\audiosmith-engine.exe` 与主程序版本号一致且无残留孤儿进程。
