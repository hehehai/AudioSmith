#!/usr/bin/env bash
# Builds AudioSmith for Windows (amd64 or arm64):
#   1. Cross-compiles or natively builds audiosmith-engine.exe (CGO_ENABLED=0)
#      and stages it into resources/windows-<arch>/engine/audiosmith-engine.exe
#      (matching internal/engine/find.go and MyGo's platform resource layout).
#   2. Runs `go tool mygo build -platform windows/<arch>` to produce the
#      portable app directory (build/windows-<arch>/) and NSIS installer
#      (unless --skip-installer is passed or makensis is unavailable).
#   3. Supports optional Azure Trusted Signing (signtool + Trusted Signing
#      dlib on Windows) or a custom WINDOWS_SIGN_COMMAND; when unset (or
#      --no-sign), produces a clean unsigned dev build without touching
#      mygo.json.
#
# Usage:
#   scripts/build-win.sh [--arch amd64|arm64] [--skip-installer] [--engine-only] [--no-sign] [--version <ver>]
#
# Environment:
#   GOARCH                       amd64 (default) or arm64 (--arch overrides)
#   AUDIOSMITH_ENGINE_VERSION    version stamped into audiosmith-engine.exe
#   AZURE_SIGNING_ENDPOINT       Azure Trusted Signing endpoint URL
#   AZURE_SIGNING_ACCOUNT        Azure Trusted Signing account name
#   AZURE_SIGNING_PROFILE        Azure Trusted Signing certificate profile
#   AZURE_SIGNING_DLIB           path to Azure.CodeSigning.Dlib.dll (Windows)
#   AZURE_SIGNING_TIMESTAMP_URL  default http://timestamp.acs.microsoft.com
#   WINDOWS_SIGN_COMMAND         custom sign command with %1 placeholder
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCH="${GOARCH:-amd64}"
SKIP_INSTALLER=false
ENGINE_ONLY=false
NO_SIGN=false
VERSION="${AUDIOSMITH_ENGINE_VERSION:-}"

usage() {
  sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
}

fail() { echo "build-win: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch)
      [[ $# -ge 2 ]] || fail "--arch requires amd64 or arm64"
      ARCH="$2"; shift 2 ;;
    --skip-installer) SKIP_INSTALLER=true; shift ;;
    --engine-only) ENGINE_ONLY=true; shift ;;
    --no-sign) NO_SIGN=true; shift ;;
    --version)
      [[ $# -ge 2 ]] || fail "--version requires a value"
      VERSION="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option: $1 (use --help)" ;;
  esac
done

case "$ARCH" in
  amd64|arm64) ;;
  *) fail "unsupported arch \"$ARCH\" (expected amd64 or arm64)" ;;
esac

if [[ -z "$VERSION" ]]; then
  if command -v jq > /dev/null 2>&1 && [[ -f "$ROOT/mygo.json" ]]; then
    VERSION="$(jq -r '.version // empty' "$ROOT/mygo.json")"
  fi
  if [[ -z "$VERSION" ]]; then
    VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo 0.1.0-win)"
  fi
fi

STAGE="$ROOT/resources/windows-${ARCH}/engine"
ENGINE_OUT="$STAGE/audiosmith-engine.exe"

mkdir -p "$STAGE"
echo "build-win: building audiosmith-engine.exe (windows/${ARCH}, CGO_ENABLED=0, version=${VERSION})"
(
  cd "$ROOT"
  GOOS=windows GOARCH="$ARCH" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" \
    -o "$ENGINE_OUT" ./engine-win/cmd/audiosmith-engine
)
ls -lh "$ENGINE_OUT"
echo "build-win: staged ${ENGINE_OUT#"$ROOT"/}"

if $ENGINE_ONLY; then
  exit 0
fi

# Optional code signing configuration (restores mygo.json on exit).
MYGO_BACKUP=""
TEMP_DIR=""
cleanup() {
  if [[ -n "$MYGO_BACKUP" && -f "$MYGO_BACKUP" ]]; then
    cp "$MYGO_BACKUP" "$ROOT/mygo.json"
  fi
  if [[ -n "$TEMP_DIR" && -d "$TEMP_DIR" ]]; then
    rm -rf "$TEMP_DIR"
  fi
}
trap cleanup EXIT

is_windows_host() {
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) return 0 ;;
    *) return 1 ;;
  esac
}

SIGN_CMD=""
if ! $NO_SIGN; then
  if [[ -n "${WINDOWS_SIGN_COMMAND:-}" ]]; then
    SIGN_CMD="$WINDOWS_SIGN_COMMAND"
  elif [[ -n "${AZURE_SIGNING_ENDPOINT:-}${AZURE_SIGNING_ACCOUNT:-}${AZURE_SIGNING_PROFILE:-}" ]]; then
    [[ -n "${AZURE_SIGNING_ENDPOINT:-}" ]] || fail "AZURE_SIGNING_ENDPOINT is required for Azure Trusted Signing"
    [[ -n "${AZURE_SIGNING_ACCOUNT:-}" ]] || fail "AZURE_SIGNING_ACCOUNT is required for Azure Trusted Signing"
    [[ -n "${AZURE_SIGNING_PROFILE:-}" ]] || fail "AZURE_SIGNING_PROFILE is required for Azure Trusted Signing"
    if ! is_windows_host; then
      fail "Azure Trusted Signing requires Windows (signtool.exe + Azure.CodeSigning.Dlib.dll); pass --no-sign for an unsigned cross-compiled build"
    fi
    [[ -n "${AZURE_SIGNING_DLIB:-}" && -f "$AZURE_SIGNING_DLIB" ]] || fail "AZURE_SIGNING_DLIB must point to Azure.CodeSigning.Dlib.dll"
    command -v jq > /dev/null 2>&1 || fail "jq is required to configure Azure Trusted Signing"

    TEMP_DIR="$(mktemp -d)"
    META_FILE="$TEMP_DIR/trusted-signing-metadata.json"
    jq -n \
      --arg ep "$AZURE_SIGNING_ENDPOINT" \
      --arg acc "$AZURE_SIGNING_ACCOUNT" \
      --arg prof "$AZURE_SIGNING_PROFILE" \
      '{Endpoint: $ep, CodeSigningAccountName: $acc, CertificateProfileName: $prof}' > "$META_FILE"
    TS_URL="${AZURE_SIGNING_TIMESTAMP_URL:-http://timestamp.acs.microsoft.com}"
    SIGN_CMD="signtool sign /v /fd SHA256 /tr ${TS_URL} /td SHA256 /dlib \"${AZURE_SIGNING_DLIB}\" /dmdf \"${META_FILE}\" %1"
  fi
fi

if [[ -n "$SIGN_CMD" ]]; then
  command -v jq > /dev/null 2>&1 || fail "jq is required to inject windows.signCommand"
  TEMP_DIR="${TEMP_DIR:-$(mktemp -d)}"
  MYGO_BACKUP="$TEMP_DIR/mygo.json.bak"
  cp "$ROOT/mygo.json" "$MYGO_BACKUP"
  jq --arg cmd "$SIGN_CMD" '.windows = ((.windows // {}) + {signCommand: $cmd})' \
    "$MYGO_BACKUP" > "$ROOT/mygo.json"
  echo "build-win: signing enabled via windows.signCommand (mygo.json will be restored on exit)"
else
  echo "build-win: unsigned dev build"
fi

BUILD_ARGS=(-platform "windows/${ARCH}")
if $SKIP_INSTALLER; then
  BUILD_ARGS+=(-skip-installer)
fi

echo "build-win: running go tool mygo build ${BUILD_ARGS[*]}"
(
  cd "$ROOT"
  CGO_ENABLED=0 go tool mygo build "${BUILD_ARGS[@]}"
)

OUT_DIR="$ROOT/build/windows-${ARCH}"
if [[ -d "$OUT_DIR" ]]; then
  echo "build-win: outputs in ${OUT_DIR#"$ROOT"/}:"
  ls -la "$OUT_DIR"
fi
