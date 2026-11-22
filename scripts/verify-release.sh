#!/usr/bin/env bash
# Checks what `go tool mygo build` made in build/darwin-arm64: the app and
# audiosmith-engine are signed (and, for a Developer ID build, with the
# hardened runtime, their entitlements and a timestamp), the disk image is
# there, and with --release that Gatekeeper accepts the app, the
# notarization tickets are stapled and the update files were written.
# The release workflow and scripts/release-local.sh run it after the build.
#
#   scripts/verify-release.sh            # any build (ad hoc too)
#   scripts/verify-release.sh --release  # a signed, notarized build with updates
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/build/darwin-arm64"
APP="$OUT/AudioSmith.app"
ENGINE="$APP/Contents/Resources/engine/audiosmith-engine"
release=false
[[ "${1:-}" == "--release" ]] && release=true
fail() { echo "verify-release: $*" >&2; exit 1; }

version="$(jq -r .version "$ROOT/mygo.json")"
[[ -d "$APP" ]] || fail "no $APP"
[[ -x "$ENGINE" ]] || fail "no audiosmith-engine in the app (run scripts/build-engine.sh first)"
dmg="$OUT/AudioSmith-$version-arm64.dmg" # scripts/dmg-name.sh named it
[[ -f "$dmg" ]] || fail "no disk image ${dmg#$ROOT/}"

codesign --verify --deep --strict --verbose=2 "$APP" 2>&1 | tail -2
for code in "$APP" "$ENGINE"; do
  info="$(codesign -dvv "$code" 2>&1)"
  name="${code#$OUT/}"
  if grep -q "^Signature=adhoc" <<< "$info"; then
    $release && fail "$name is signed ad hoc"
    echo "$name: ad hoc"
    continue
  fi
  grep -q "Authority=Developer ID Application" <<< "$info" || fail "$name is not signed with a Developer ID"
  grep -Eq "flags=.*runtime" <<< "$info" || fail "$name lacks the hardened runtime"
  grep -q "^Timestamp=" <<< "$info" || fail "$name has no secure timestamp"
  ents="$(codesign -d --entitlements - --xml "$code" 2> /dev/null || true)"
  grep -q "com.apple.security.device.audio-input" <<< "$ents" || fail "$name lacks com.apple.security.device.audio-input"
  echo "$name: $(grep -m1 "^Authority=" <<< "$info" | cut -d= -f2), hardened runtime, microphone entitlement"
done

if $release; then
  spctl --assess --type execute --verbose=2 "$APP" 2>&1 | tail -2
  spctl --assess --type execute "$APP" || fail "Gatekeeper rejects the app"
  xcrun stapler validate "$dmg" > /dev/null || fail "the disk image has no stapled ticket"
  xcrun stapler validate "$APP" > /dev/null || fail "the app has no stapled ticket"
  [[ -f "$OUT/update-darwin-arm64.json" ]] || fail "no update manifest (MYGO_UPDATER_PRIVATE_KEY and the updates section)"
  [[ -f "$OUT/audiosmith-$version-darwin-arm64.tar.gz" ]] || fail "no update archive"
  jq -e --arg v "$version" '.version == $v' "$OUT/update-darwin-arm64.json" > /dev/null || fail "the manifest is not of $version"
  echo "notarized and stapled; update manifest of $version:"
  jq '{version, date, url, size, deltas: [.deltas[]?.from]}' "$OUT/update-darwin-arm64.json"
fi
ls -la "$OUT" | grep -v "^total"
echo "verify-release: ok"
