<#
.SYNOPSIS
    Builds AudioSmith for Windows (amd64 or arm64).

.DESCRIPTION
    1. Builds audiosmith-engine.exe (CGO_ENABLED=0) and stages it into
       resources\windows-<arch>\engine\audiosmith-engine.exe (matching
       internal/engine/find.go and MyGo's platform resource layout).
    2. Runs `go tool mygo build -platform windows/<arch>` to produce the
       portable build directory (build\windows-<arch>\) and NSIS installer
       (unless -SkipInstaller is specified).
    3. Supports optional Azure Trusted Signing (signtool.exe +
       Azure.CodeSigning.Dlib.dll) when AZURE_SIGNING_* environment variables
       are set, or a custom WINDOWS_SIGN_COMMAND. When unset (or -NoSign),
       produces a clean unsigned dev build without modifying mygo.json.

.PARAMETER Arch
    Target architecture: amd64 (default, or $env:GOARCH) or arm64.

.PARAMETER SkipInstaller
    Skip NSIS installer generation (portable directory only).

.PARAMETER EngineOnly
    Build and stage audiosmith-engine.exe only.

.PARAMETER NoSign
    Force an unsigned dev build even if signing environment variables are set.

.PARAMETER Version
    Version string stamped into audiosmith-engine.exe (default: mygo.json version).

.EXAMPLE
    .\scripts\build-win.ps1
    .\scripts\build-win.ps1 -Arch arm64 -SkipInstaller
    .\scripts\build-win.ps1 -EngineOnly
#>
[CmdletBinding()]
param(
    [ValidateSet("amd64", "arm64")]
    [string]$Arch = "",
    [switch]$SkipInstaller,
    [switch]$EngineOnly,
    [switch]$NoSign,
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"

$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$MygoConfigPath = Join-Path $Root "mygo.json"

if (-not $Arch) {
    if ($env:GOARCH -in @("amd64", "arm64")) {
        $Arch = $env:GOARCH
    } else {
        $Arch = "amd64"
    }
}

if (-not $Version) {
    if ($env:AUDIOSMITH_ENGINE_VERSION) {
        $Version = $env:AUDIOSMITH_ENGINE_VERSION
    } elseif (Test-Path $MygoConfigPath) {
        $Version = (Get-Content $MygoConfigPath -Raw | ConvertFrom-Json).version
    }
    if (-not $Version) {
        $Version = "0.1.0-win"
    }
}

function Find-SignTool {
    $cmd = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $kitsRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
    if (Test-Path $kitsRoot) {
        $sdkArch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "x64" }
        $candidates = Get-ChildItem -Path $kitsRoot -Directory -Filter "10.*" |
            Sort-Object Name -Descending |
            ForEach-Object { Join-Path $_.FullName "$sdkArch\signtool.exe" } |
            Where-Object { Test-Path $_ }
        if ($candidates) { return $candidates[0] }
    }
    return $null
}

function Find-TrustedSigningDlib {
    if ($env:AZURE_SIGNING_DLIB -and (Test-Path $env:AZURE_SIGNING_DLIB)) {
        return $env:AZURE_SIGNING_DLIB
    }
    $sdkArch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "x64" }
    $roots = @(
        (Join-Path $env:LOCALAPPDATA "Microsoft\MicrosoftArtifactSigningClientTools"),
        (Join-Path $env:LOCALAPPDATA "Microsoft\MicrosoftTrustedSigningClientTools"),
        (Join-Path $env:USERPROFILE ".nuget\packages\microsoft.trusted.signing.client")
    )
    foreach ($r in $roots) {
        if (-not (Test-Path $r)) { continue }
        $found = Get-ChildItem -Path $r -Recurse -Filter "Azure.CodeSigning.Dlib.dll" -ErrorAction SilentlyContinue |
            Where-Object { $_.FullName -match "\\$sdkArch\\" } |
            Select-Object -First 1
        if ($found) { return $found.FullName }
    }
    return $null
}

# 1. Build and stage audiosmith-engine.exe.
$StageDir = Join-Path $Root "resources\windows-$Arch\engine"
$EngineOut = Join-Path $StageDir "audiosmith-engine.exe"
New-Item -ItemType Directory -Force -Path $StageDir | Out-Null

Write-Host "build-win: building audiosmith-engine.exe (windows/$Arch, CGO_ENABLED=0, version=$Version)"
Push-Location $Root
try {
    $prevGOOS = $env:GOOS
    $prevGOARCH = $env:GOARCH
    $prevCGO = $env:CGO_ENABLED
    try {
        $env:GOOS = "windows"
        $env:GOARCH = $Arch
        $env:CGO_ENABLED = "0"
        & go build -trimpath -ldflags "-s -w -X main.Version=$Version" -o $EngineOut ./engine-win/cmd/audiosmith-engine
        if ($LASTEXITCODE -ne 0) {
            throw "go build audiosmith-engine failed with exit code $LASTEXITCODE"
        }
    } finally {
        $env:GOOS = $prevGOOS
        $env:GOARCH = $prevGOARCH
        $env:CGO_ENABLED = $prevCGO
    }
} finally {
    Pop-Location
}

$EngineInfo = Get-Item $EngineOut
Write-Host ("build-win: staged {0} ({1:N2} MB)" -f $EngineOut.Substring($Root.Length + 1), ($EngineInfo.Length / 1MB))

if ($EngineOnly) {
    exit 0
}

# 2. Optional Azure Trusted Signing / custom signCommand (restores mygo.json in finally).
$TempDir = $null
$MygoBackup = $null
$SignCmd = $null

try {
    if (-not $NoSign) {
        if ($env:WINDOWS_SIGN_COMMAND) {
            $SignCmd = $env:WINDOWS_SIGN_COMMAND
        } elseif ($env:AZURE_SIGNING_ENDPOINT -or $env:AZURE_SIGNING_ACCOUNT -or $env:AZURE_SIGNING_PROFILE) {
            if (-not $env:AZURE_SIGNING_ENDPOINT) { throw "AZURE_SIGNING_ENDPOINT is required for Azure Trusted Signing" }
            if (-not $env:AZURE_SIGNING_ACCOUNT)  { throw "AZURE_SIGNING_ACCOUNT is required for Azure Trusted Signing" }
            if (-not $env:AZURE_SIGNING_PROFILE)  { throw "AZURE_SIGNING_PROFILE is required for Azure Trusted Signing" }

            $SignTool = Find-SignTool
            if (-not $SignTool) {
                throw "signtool.exe not found (install Windows 10/11 SDK)"
            }
            $Dlib = Find-TrustedSigningDlib
            if (-not $Dlib) {
                throw "Azure.CodeSigning.Dlib.dll not found (set AZURE_SIGNING_DLIB or install Microsoft.Trusted.Signing.Client)"
            }

            $TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("audiosmith-sign-" + [System.Guid]::NewGuid().ToString("N"))
            New-Item -ItemType Directory -Force -Path $TempDir | Out-Null
            $MetaFile = Join-Path $TempDir "trusted-signing-metadata.json"
            $Meta = [ordered]@{
                Endpoint               = $env:AZURE_SIGNING_ENDPOINT
                CodeSigningAccountName = $env:AZURE_SIGNING_ACCOUNT
                CertificateProfileName = $env:AZURE_SIGNING_PROFILE
            }
            $Meta | ConvertTo-Json -Depth 3 | Set-Content -Path $MetaFile -Encoding utf8

            $TsUrl = if ($env:AZURE_SIGNING_TIMESTAMP_URL) { $env:AZURE_SIGNING_TIMESTAMP_URL } else { "http://timestamp.acs.microsoft.com" }
            $SignCmd = "`"$SignTool`" sign /v /fd SHA256 /tr $TsUrl /td SHA256 /dlib `"$Dlib`" /dmdf `"$MetaFile`" %1"
        }
    }

    if ($SignCmd) {
        if (-not $TempDir) {
            $TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("audiosmith-sign-" + [System.Guid]::NewGuid().ToString("N"))
            New-Item -ItemType Directory -Force -Path $TempDir | Out-Null
        }
        $MygoBackup = Join-Path $TempDir "mygo.json.bak"
        Copy-Item $MygoConfigPath $MygoBackup -Force

        $cfg = Get-Content $MygoBackup -Raw | ConvertFrom-Json
        if (-not $cfg.windows) {
            $cfg | Add-Member -NotePropertyName windows -NotePropertyValue ([PSCustomObject]@{})
        }
        if ($cfg.windows.PSObject.Properties.Name -contains "signCommand") {
            $cfg.windows.signCommand = $SignCmd
        } else {
            $cfg.windows | Add-Member -NotePropertyName signCommand -NotePropertyValue $SignCmd
        }
        $cfg | ConvertTo-Json -Depth 10 | Set-Content -Path $MygoConfigPath -Encoding utf8
        Write-Host "build-win: signing enabled via windows.signCommand (mygo.json will be restored on exit)"
    } else {
        Write-Host "build-win: unsigned dev build"
    }

    # 3. Build AudioSmith app bundle (+ NSIS installer unless -SkipInstaller).
    $BuildArgs = @("tool", "mygo", "build", "-platform", "windows/$Arch")
    if ($SkipInstaller) {
        $BuildArgs += "-skip-installer"
    }
    Write-Host ("build-win: running go {0}" -f ($BuildArgs -join " "))
    Push-Location $Root
    try {
        $prevCGO = $env:CGO_ENABLED
        try {
            $env:CGO_ENABLED = "0"
            & go @BuildArgs
            if ($LASTEXITCODE -ne 0) {
                throw "go tool mygo build failed with exit code $LASTEXITCODE"
            }
        } finally {
            $env:CGO_ENABLED = $prevCGO
        }
    } finally {
        Pop-Location
    }
} finally {
    if ($MygoBackup -and (Test-Path $MygoBackup)) {
        Copy-Item $MygoBackup $MygoConfigPath -Force
    }
    if ($TempDir -and (Test-Path $TempDir)) {
        Remove-Item $TempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$OutDir = Join-Path $Root "build\windows-$Arch"
if (Test-Path $OutDir) {
    Write-Host ("build-win: outputs in {0}:" -f $OutDir.Substring($Root.Length + 1))
    Get-ChildItem $OutDir | Format-Table Name, Length, LastWriteTime -AutoSize
}
