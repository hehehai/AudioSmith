#!/usr/bin/env bash
# Gives the disk image `go tool mygo build` made in build/darwin-arm64 the
# name people download it by, AudioSmith-<version>-arm64.dmg, as before
# MyGo 0.7, which names it by the app's slug: audiosmith-<version>-arm64.dmg
# (the update archives and deltas keep MyGo's names, which the update
# feed uses). The release workflow and scripts/release-local.sh run it
# right after the build; verify-release.sh and release-publish.sh look for
# the new name.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/build/darwin-arm64"
version="$(jq -r .version "$ROOT/mygo.json")"
built="$OUT/audiosmith-$version-arm64.dmg"
want="$OUT/AudioSmith-$version-arm64.dmg"
if [[ -f "$built" ]]; then
  mv -f "$built" "$want"
fi
[[ -f "$want" ]] || { echo "dmg-name: no disk image $built" >&2; exit 1; }
echo "disk image: ${want#$ROOT/}"
