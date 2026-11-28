# Building AudioSmith for Windows

AudioSmith builds for **Windows 10/11 x64 (`amd64`)** and **Windows 11 ARM64 (`arm64`)** with `CGO_ENABLED=0` using MyGo's native Win32/Direct3D 11/DirectWrite/WASAPI backend and the Windows hybrid engine (`engine-win`).

> **Hardware baseline:** AudioSmith on Windows is designed to run cleanly out of the box on everyday laptops, Intel/AMD integrated GPUs (iGPU), CPU-only PCs, and Windows ARM64 machines — it does **not** assume a high-end NVIDIA GPU. Dedicated NVIDIA/AMD/Intel GPUs are detected automatically as optional accelerators when present.

---

## 1. Prerequisites

| Tool | Needed for | Notes |
| --- | --- | --- |
| **Go 1.27+** | All builds and tests | Matches `go.mod` (`CGO_ENABLED=0`) |
| **`jq`** | `scripts/build-win.sh`, `scripts/release-*.sh` | Not required for `scripts/build-win.ps1` |
| **NSIS (`makensis`)** | Building the `.exe` installer | On Windows, `go tool mygo build` downloads NSIS automatically if missing; on Linux/macOS install `nsis` or pass `--skip-installer` |
| **Zig** *(optional)* | Rebuilding `Sqlitecore.dll` from source | **Not required** for normal builds or `go test`: MyGo downloads a prebuilt, SHA-256-verified `Sqlitecore.dll` for `windows-amd64` and `windows-arm64` automatically |
| **Windows 10/11 SDK (`signtool.exe`) + Trusted Signing Client Tools** *(optional)* | Azure Trusted Signing | Only required for signed release builds |

---

## 2. Cross-Compiling from Linux or macOS

You can compile both `audiosmith-engine.exe` and the full portable `AudioSmith.exe` bundle (including `Sqlitecore.dll` and resources) directly from Linux or macOS with `CGO_ENABLED=0`:

```bash
# Portable amd64 ZIP + SHA256SUMS-windows.txt (skips NSIS installer automatically)
scripts/build-win.sh --arch amd64 --portable

# Portable arm64 ZIP + SHA256SUMS-windows.txt
scripts/build-win.sh --arch arm64 --portable

# Portable directory only (no ZIP, no NSIS installer)
scripts/build-win.sh --arch amd64 --skip-installer

# Engine binary only (staged to resources/windows-<arch>/engine/audiosmith-engine.exe)
scripts/build-win.sh --arch amd64 --engine-only
```

If `makensis` is installed on Linux/macOS (`apt install nsis` / `brew install makensis`), pass `--installer` (optionally combined with `--portable`) to also produce `build/windows-<arch>/AudioSmith-<version>-windows-<arch>-setup.exe` and `SHA256SUMS-windows.txt`.

### Cross-platform `go vet` & compile checks

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./app/ ./internal/... ./engine-win/...
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go vet ./app/ ./internal/... ./engine-win/...
```

### Engine staging layout

`scripts/build-win.sh` and `scripts/build-win.ps1` stage the engine binary at:

```text
resources/windows-<arch>/engine/audiosmith-engine.exe
```

MyGo copies `resources/windows-<arch>/` into the root of `build/windows-<arch>/` (and the NSIS installer), so at runtime `internal/engine/find.go` resolves:

1. `$AUDIOSMITH_ENGINE` (explicit override, if set)
2. `<app-dir>\engine\audiosmith-engine.exe` (packaged build and `mygo dev`)
3. `<repo>\resources\windows-<arch>\engine\audiosmith-engine.exe` (`go run ./app` from the repo root)

---

## 3. Native Windows Build (PowerShell)

On Windows 10/11 (PowerShell 5.1+ or PowerShell 7+):

```powershell
# Default: unsigned amd64 build + NSIS installer
.\scripts\build-win.ps1

# Release packaging: NSIS installer + self-contained portable ZIP + SHA256SUMS-windows.txt
.\scripts\build-win.ps1 -Arch amd64 -Installer -Portable

# ARM64 portable ZIP + SHA256SUMS-windows.txt (skips NSIS installer automatically)
.\scripts\build-win.ps1 -Arch arm64 -Portable

# Build and stage audiosmith-engine.exe only
.\scripts\build-win.ps1 -EngineOnly
```

Outputs are written to `build\windows-<arch>\`:
- `AudioSmith.exe` — main desktop application
- `engine\audiosmith-engine.exe` — staged Windows hybrid engine
- `Sqlitecore.dll` — SQLite native plugin library
- `AudioSmith-<version>-windows-<arch>-setup.exe` — per-user NSIS installer (`%LOCALAPPDATA%\Programs\AudioSmith`, registers `.asvoice` / `.ovsvoice` and `audiosmith://` under `HKCU`, no UAC required)
- `AudioSmith-<version>-windows-<arch>-portable.zip` — self-contained portable archive (when `-Portable` is passed)
- `SHA256SUMS-windows.txt` — SHA-256 checksums for the installer and/or portable ZIP (automatic with `-Installer` or `-Portable`, or pass `-Checksum`)

### `Sqlitecore.dll`: Prebuilt Cache vs. Building with Zig

By default, MyGo downloads its published `Sqlitecore.dll` for `windows-amd64` and `windows-arm64` into `%LOCALAPPDATA%\mygo\natives\<sha256>\Sqlitecore.dll` and checks its SHA-256 against `mygo-plugin.json`. This means `go test ./...` and `go tool mygo build` work on Windows **without installing Zig or CGO**.

If you are developing against a modified local fork of MyGo's SQLite plugin and need to rebuild `Sqlitecore.dll` from source:

1. Install **Zig** (`winget install zig.zig` or download from https://ziglang.org/download/) and ensure `zig.exe` is on `PATH`.
2. In your local MyGo checkout, run `mygo native build` inside `plugins/sqlite/internal/sqlitecore` (MyGo invokes `zig build` targeting `x86_64-windows-gnu` / `aarch64-windows-gnu`).

---

## 4. Testing on Everyday / iGPU PCs & Win11 ARM64 VM

### Unit tests on Windows

```powershell
$env:CGO_ENABLED = "0"
go vet ./...
go tool mygo vet .
go test -count=1 ./app ./internal/... ./engine-win/...
```

### Smoke-testing on everyday / iGPU hardware

1. Build or copy `build\windows-amd64\` (or `build\windows-arm64\` for Snapdragon X / ARM64 VM) onto the target machine.
2. Launch `AudioSmith.exe` from PowerShell or Explorer.
3. Verify in the UI / logs (`%APPDATA%\AudioSmith\logs`):
   - Window renders cleanly via D3D11 + DirectWrite (including software/iGPU adapters).
   - `audiosmith-engine.exe` starts automatically from `engine\audiosmith-engine.exe` (or override with `$env:AUDIOSMITH_ENGINE = "C:\path\to\audiosmith-engine.exe"`).
   - Hardware detection reports the actual GPU/iGPU/CPU capabilities without assuming CUDA/NVIDIA.

### Testing in a Windows 11 ARM64 VM (e.g. VMware Fusion on Apple Silicon)

1. Cross-compile on macOS/Linux:
   ```bash
   scripts/build-win.sh --arch arm64 --skip-installer
   ```
2. Copy `build/windows-arm64/` to the VM (e.g. via `scp` or shared folder).
3. Launch `AudioSmith.exe` inside an interactive desktop session (note: SSH sessions run in Session 0 without a desktop; use the VM console or a scheduled task in Session 1 to test GUI rendering, tray, notifications, and global shortcuts).

---

## 5. Code Signing with Azure Trusted Signing

AudioSmith uses MyGo's `windows.signCommand` hook to sign `AudioSmith.exe`, `engine\audiosmith-engine.exe`, `Sqlitecore.dll`, the NSIS uninstaller (`Uninstall.exe`), and the final NSIS installer (`audiosmith-setup-<version>-<arch>.exe`).

### One-time setup

1. Provision an **Azure Trusted Signing** account and certificate profile in Azure Portal.
2. Install the **Windows 10/11 SDK** (for `signtool.exe`) and **Microsoft Trusted Signing Client Tools** (`Azure.CodeSigning.Dlib.dll`).
3. Authenticate using `DefaultAzureCredential`:
   - **Local developer machine:** run `az login`.
   - **GitHub Actions CI:** configure OIDC (`azure/login@v2` with `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`).

### Running a signed build

Set the following environment variables and run `scripts/build-win.ps1`:

```powershell
$env:AZURE_SIGNING_ENDPOINT = "https://eus.codesigning.azure.net/"
$env:AZURE_SIGNING_ACCOUNT  = "<code-signing-account-name>"
$env:AZURE_SIGNING_PROFILE  = "<certificate-profile-name>"
# Optional: auto-discovered from standard Client Tools / NuGet paths if omitted
$env:AZURE_SIGNING_DLIB     = "C:\...\Azure.CodeSigning.Dlib.dll"
# Optional: defaults to http://timestamp.acs.microsoft.com
# $env:AZURE_SIGNING_TIMESTAMP_URL = "http://timestamp.acs.microsoft.com"

.\scripts\build-win.ps1 -Arch amd64
```

Both `scripts/build-win.ps1` and `scripts/build-win.sh` temporarily inject `windows.signCommand` into `mygo.json` and restore `mygo.json` automatically on exit (so signing settings are never committed). Pass `-NoSign` / `--no-sign` to force an unsigned dev build even when these variables are set.

To verify the resulting signatures on Windows:

```powershell
signtool verify /pa /v build\windows-amd64\AudioSmith.exe
signtool verify /pa /v build\windows-amd64\engine\audiosmith-engine.exe
signtool verify /pa /v build\windows-amd64\audiosmith-setup-*-amd64.exe
```

---

## 6. Release & Publishing Notes

For the full Windows release runbook (GitHub Actions `.github/workflows/release-windows.yml`, Azure Trusted Signing setup & secrets, dry-run/SmartScreen testing, and the `actnow.dev` ingest checklist), see [`docs/release-windows.zh-CN.md`](release-windows.zh-CN.md).

- **Windows Release workflow (`.github/workflows/release-windows.yml`)**: runs independently from macOS `.github/workflows/release.yml`, builds `AudioSmith-<version>-windows-amd64-setup.exe`, `AudioSmith-<version>-windows-amd64-portable.zip`, `AudioSmith-<version>-windows-arm64-portable.zip`, `SHA256SUMS-windows.txt`, and `update-windows-amd64.json` (when updater keys are configured). Falls back cleanly to an unsigned build if Azure Trusted Signing secrets are absent.
- **Update feed config (`scripts/release-config.sh`)**: setting `MYGO_UPDATER_PUBLIC_KEY` writes the cross-platform `updates` block (`https://actnow.dev/updates/audiosmith` with `prereleases: true`). Setting `WINDOWS_SIGN_COMMAND` writes `windows.signCommand` into `mygo.json` for CI builds.
- **Staging Windows release artifacts (`scripts/release-publish.sh`)**:
  - `--include-windows` auto-discovers `build/windows-amd64` and `build/windows-arm64` if present.
  - `--windows-dir <dir>` (repeatable) explicitly includes Windows build directories (`.exe`, `-portable.zip`, `SHA256SUMS-windows*.txt`, and `update-windows-*.json`).
  - Combine with `--skip-website` on a Windows runner to attach Windows installers and `update-windows-*.json` manifests to an existing draft GitHub release without needing `build/darwin-arm64`.
- **`actnow.dev` website ingest (`scripts/actnow-publish.sh`)**: `scripts/actnow-publish.sh` is vendored verbatim from `actnow.dev` (SHA-256 pinned by `TestActnowPublishVendored`). Until `actnow.dev` enables Windows `.exe` ingest and `scripts/vendor-actnow-publish.sh` is refreshed, Windows installers and manifests are published via GitHub releases while macOS continues publishing to both `actnow.dev` and GitHub.
