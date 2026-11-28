#!/usr/bin/env bash
# Builds AudioSmith for Windows (amd64 or arm64):
#   1. Cross-compiles or natively builds audiosmith-engine.exe (CGO_ENABLED=0)
#      and stages it into resources/windows-<arch>/engine/audiosmith-engine.exe
#      (matching internal/engine/find.go and MyGo's platform resource layout).
#   2. Runs `go tool mygo build -platform windows/<arch>` to produce the
#      app directory (build/windows-<arch>/) and optional NSIS installer.
#   3. With --installer, requires and normalizes the NSIS installer to
#      AudioSmith-<version>-windows-<arch>-setup.exe.
#   4. With --portable, packages a clean self-contained portable archive
#      AudioSmith-<version>-windows-<arch>-portable.zip (AudioSmith.exe,
#      Sqlitecore.dll, engine/audiosmith-engine.exe, LICENSE, NOTICE,
#      THIRD-PARTY-NOTICES.txt).
#   5. Generates build/windows-<arch>/SHA256SUMS-windows.txt for packaged
#      release artifacts (automatic with --installer or --portable, or pass
#      --checksum).
#   6. Supports optional Azure Trusted Signing (signtool + Trusted Signing
#      dlib on Windows) or a custom WINDOWS_SIGN_COMMAND; when unset (or
#      --no-sign), produces a clean unsigned build without touching mygo.json.
#
# Usage:
#   scripts/build-win.sh [--arch amd64|arm64] [--installer] [--portable]
#                        [--skip-installer] [--checksum] [--engine-only]
#                        [--no-sign] [--version <ver>]
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
INSTALLER=false
PORTABLE=false
SKIP_INSTALLER=false
CHECKSUM=false
ENGINE_ONLY=false
NO_SIGN=false
VERSION="${AUDIOSMITH_ENGINE_VERSION:-}"

usage() {
  sed -n '2,35p' "$0" | sed 's/^# \{0,1\}//'
}

fail() { echo "build-win: $*" >&2; exit 1; }

sha256_of() {
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$1" | awk '{print tolower($1)}'
  elif command -v shasum > /dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print tolower($1)}'
  else
    fail "needs sha256sum or shasum to generate checksums"
  fi
}

make_zip() {
  local src_dir="$1"
  local zip_out="$2"
  rm -f "$zip_out"
  if command -v zip > /dev/null 2>&1; then
    (cd "$src_dir" && zip -r -q "$zip_out" .)
  elif command -v python3 > /dev/null 2>&1; then
    python3 - "$src_dir" "$zip_out" <<'PY'
import os, sys, zipfile
src, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    for root, dirs, files in os.walk(src):
        dirs.sort()
        for name in sorted(files):
            full = os.path.join(root, name)
            rel = os.path.relpath(full, src).replace(os.sep, "/")
            zf.write(full, rel)
PY
  else
    fail "needs zip or python3 to build portable .zip"
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch)
      [[ $# -ge 2 ]] || fail "--arch requires amd64 or arm64"
      ARCH="$2"; shift 2 ;;
    --installer) INSTALLER=true; shift ;;
    --portable) PORTABLE=true; shift ;;
    --skip-installer) SKIP_INSTALLER=true; shift ;;
    --checksum) CHECKSUM=true; shift ;;
    --engine-only) ENGINE_ONLY=true; shift ;;
    --no-sign) NO_SIGN=true; shift ;;
    --version)
      [[ $# -ge 2 ]] || fail "--version requires a value"
      VERSION="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option: $1 (use --help)" ;;
  esac
done

if $INSTALLER && $SKIP_INSTALLER; then
  fail "--installer and --skip-installer cannot be used together"
fi
if $PORTABLE && ! $INSTALLER; then
  SKIP_INSTALLER=true
fi
if $INSTALLER || $PORTABLE; then
  CHECKSUM=true
fi

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
  echo "build-win: unsigned build"
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
[[ -d "$OUT_DIR" ]] || fail "expected build output directory ${OUT_DIR#"$ROOT"/}"

# Normalize MyGo's NSIS installer name (audiosmith-setup-<ver>-<arch>.exe)
# to the release convention (AudioSmith-<ver>-windows-<arch>-setup.exe).
MYGO_INSTALLER="$OUT_DIR/audiosmith-setup-${VERSION}-${ARCH}.exe"
NORM_INSTALLER="$OUT_DIR/AudioSmith-${VERSION}-windows-${ARCH}-setup.exe"
if [[ -f "$MYGO_INSTALLER" ]]; then
  mv -f "$MYGO_INSTALLER" "$NORM_INSTALLER"
  echo "build-win: installer ${NORM_INSTALLER#"$ROOT"/}"
elif $INSTALLER && [[ ! -f "$NORM_INSTALLER" ]]; then
  fail "NSIS installer was not produced in ${OUT_DIR#"$ROOT"/} (install makensis or check mygo build output)"
fi

# Package clean self-contained portable .zip when requested.
PORTABLE_ZIP="$OUT_DIR/AudioSmith-${VERSION}-windows-${ARCH}-portable.zip"
if $PORTABLE; then
  [[ -f "$OUT_DIR/AudioSmith.exe" ]] || fail "missing ${OUT_DIR#"$ROOT"/}/AudioSmith.exe"
  [[ -f "$OUT_DIR/Sqlitecore.dll" ]] || fail "missing ${OUT_DIR#"$ROOT"/}/Sqlitecore.dll"

  TEMP_DIR="${TEMP_DIR:-$(mktemp -d)}"
  PORTABLE_STAGE="$TEMP_DIR/portable"
  mkdir -p "$PORTABLE_STAGE/engine"

  cp "$OUT_DIR/AudioSmith.exe" "$PORTABLE_STAGE/AudioSmith.exe"
  cp "$OUT_DIR/Sqlitecore.dll" "$PORTABLE_STAGE/Sqlitecore.dll"
  if [[ -f "$OUT_DIR/engine/audiosmith-engine.exe" ]]; then
    cp "$OUT_DIR/engine/audiosmith-engine.exe" "$PORTABLE_STAGE/engine/audiosmith-engine.exe"
  elif [[ -f "$ENGINE_OUT" ]]; then
    cp "$ENGINE_OUT" "$PORTABLE_STAGE/engine/audiosmith-engine.exe"
  else
    fail "missing audiosmith-engine.exe for portable archive"
  fi
  for doc in LICENSE NOTICE THIRD-PARTY-NOTICES.txt; do
    if [[ -f "$OUT_DIR/$doc" ]]; then
      cp "$OUT_DIR/$doc" "$PORTABLE_STAGE/$doc"
    elif [[ -f "$ROOT/$doc" ]]; then
      cp "$ROOT/$doc" "$PORTABLE_STAGE/$doc"
    fi
  done

  make_zip "$PORTABLE_STAGE" "$PORTABLE_ZIP"
  echo "build-win: portable ${PORTABLE_ZIP#"$ROOT"/}"
fi

# Generate SHA256SUMS-windows.txt for packaged release artifacts.
if $CHECKSUM; then
  SUMS_FILE="$OUT_DIR/SHA256SUMS-windows.txt"
  checksum_targets=()
  for candidate in "$NORM_INSTALLER" "$PORTABLE_ZIP"; do
    [[ -f "$candidate" ]] && checksum_targets+=("$candidate")
  done
  if [[ ${#checksum_targets[@]} -gt 0 ]]; then
    : > "$SUMS_FILE"
    for f in "${checksum_targets[@]}"; do
      printf '%s  %s\n' "$(sha256_of "$f")" "$(basename "$f")" >> "$SUMS_FILE"
    done
    echo "build-win: checksums ${SUMS_FILE#"$ROOT"/}"
    cat "$SUMS_FILE"
  fi
fi

echo "build-win: outputs in ${OUT_DIR#"$ROOT"/}:"
ls -la "$OUT_DIR"
