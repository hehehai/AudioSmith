#!/usr/bin/env bash
# Mirrors this project to a Mac and runs a command there, for developing on
# Linux. The Mac needs Xcode and Go (GOTOOLCHAIN=auto fetches the Go the
# module asks for).
#
#   scripts/mac.sh scripts/build-engine.sh
#   scripts/mac.sh go tool mygo build
#   scripts/mac.sh --pull resources/darwin-arm64   # copy a path back
#
# MAC_HOST (default mac-mini; mac-air is the spare) and MAC_DIR (default code/AudioSmith) pick the
# target; MAC_GOPROXY its Go module proxy (proxy.golang.org times out there).
set -euo pipefail

HOST="${MAC_HOST:-mac-mini}"
GOPROXY_MAC="${MAC_GOPROXY:-https://goproxy.cn,direct}"
DIR="${MAC_DIR:-code/AudioSmith}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [[ "${1:-}" == "--pull" ]]; then
  shift
  for p in "$@"; do
    mkdir -p "$ROOT/$(dirname "$p")"
    rsync -a --delete "$HOST:$DIR/$p/" "$ROOT/$p/"
  done
  exit 0
fi

ssh "$HOST" "mkdir -p '$DIR'"
rsync -a --delete \
  --exclude .git --exclude /build --exclude /.mygo \
  --exclude /engine/.build --exclude /resources/darwin-arm64 \
  --exclude /AudioSmith \
  "$ROOT/" "$HOST:$DIR/"

[[ $# -eq 0 ]] && exit 0
# shellcheck disable=SC2029
ssh "$HOST" "cd '$DIR' && export PATH=/opt/homebrew/bin:\$PATH GOTOOLCHAIN=auto GOPROXY='$GOPROXY_MAC' && $*"
