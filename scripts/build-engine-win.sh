#!/usr/bin/env bash
# Cross-compile AudioSmith's Windows hybrid engine (CGO_ENABLED=0).
# Usage:
#   scripts/build-engine-win.sh [amd64|arm64|all] [output-dir]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCH="${1:-all}"
OUT_DIR="${2:-$ROOT/dist/engine-win}"
VERSION="${AUDIOSMITH_ENGINE_VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo 0.1.0-win)}"

mkdir -p "$OUT_DIR"

build_one() {
	local goarch="$1"
	local out="$OUT_DIR/audiosmith-engine-windows-${goarch}.exe"
	echo "Building audiosmith-engine (windows/${goarch}, CGO_ENABLED=0, version=${VERSION}) -> ${out}"
	(
		cd "$ROOT"
		GOOS=windows GOARCH="$goarch" CGO_ENABLED=0 \
			go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" \
			-o "$out" ./engine-win/cmd/audiosmith-engine
	)
	ls -lh "$out"
}

case "$ARCH" in
	amd64) build_one amd64 ;;
	arm64) build_one arm64 ;;
	all)
		build_one amd64
		build_one arm64
		;;
	*)
		echo "Usage: $0 [amd64|arm64|all] [output-dir]" >&2
		exit 2
		;;
esac
