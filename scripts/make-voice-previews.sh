#!/usr/bin/env bash
# Remakes the bundled voice previews, assets/voice-previews/*.ogg (thread
# #60): renders them on the Mac with the real engine (TestVoicePreviewsE2E:
# one line per built-in speaker, recommended sampling, trimmed, −18 LUFS,
# then Qwen3-ASR listens to each: report.md), copies the WAVs back and
# encodes them here with ffmpeg (libvorbis, mono 24 kHz).
#
#   MAC_DIR=code/AudioSmith-vp scripts/make-voice-previews.sh
#   PREVIEW_ONLY=Vivian,af_heart scripts/make-voice-previews.sh   # some speakers
#   scripts/make-voice-previews.sh --encode-only                  # WAVs already in $PREVIEW_TMP
#
# Needs: the engine built in MAC_DIR (scripts/mac.sh scripts/build-engine.sh,
# or a copy of resources/darwin-arm64), Qwen3-TTS 0.6B CustomVoice, Kokoro
# and Qwen3-ASR in the Mac's Hugging Face cache, ffmpeg with libvorbis here.
# The Mac run uses an isolated HOME (the user's AudioSmith is not touched),
# holds the shared GPU lock /tmp/as-gpu.lock and plays nothing.
set -euo pipefail

HOST="${MAC_HOST:-mac-mini}"
export MAC_DIR="${MAC_DIR:-code/AudioSmith-vp}"
TMP="${PREVIEW_TMP:-/tmp/as-voice-previews-$USER}"
REMOTE=/tmp/as-vp
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/assets/voice-previews"

if [[ "${1:-}" != "--encode-only" ]]; then
  "$ROOT/scripts/mac.sh"   # mirror the sources
  # shellcheck disable=SC2029
  ssh "$HOST" "set -e
    mkdir /tmp/as-gpu.lock 2>/dev/null || { echo 'GPU lock taken:'; cat /tmp/as-gpu.lock/owner 2>/dev/null; exit 1; }
    echo 'voice previews (make-voice-previews.sh)' > /tmp/as-gpu.lock/owner
    trap 'rm -rf /tmp/as-gpu.lock' EXIT
    export PATH=/opt/homebrew/bin:\$PATH GOTOOLCHAIN=auto GOPROXY=\${MAC_GOPROXY:-https://goproxy.cn,direct}
    export GOMODCACHE=\$(go env GOMODCACHE) GOCACHE=\$(go env GOCACHE) HF_HUB_CACHE=\$HOME/.cache/huggingface/hub
    free=\$(memory_pressure | awk '/free percentage/ {print \$NF+0}')
    [ \"\$free\" -ge 40 ] || { echo \"memory: only \$free % free\"; exit 1; }
    rm -rf $REMOTE && mkdir -p $REMOTE/home
    cd '$MAC_DIR'
    HOME=$REMOTE/home AUDIOSMITH_E2E=1 AUDIOSMITH_PREVIEW_DIR=$REMOTE AUDIOSMITH_PREVIEW_ONLY='${PREVIEW_ONLY:-}' AUDIOSMITH_PREVIEW_SEED='${PREVIEW_SEED:-}' \
      go test -run TestVoicePreviewsE2E -count=1 -v -timeout 1h ./app"
  rm -rf "$TMP" && mkdir -p "$TMP"
  rsync -a --include '*.wav' --include report.md --exclude '*' "$HOST:$REMOTE/" "$TMP/"
  ssh "$HOST" "rm -rf $REMOTE"
fi

shopt -s nullglob
wavs=("$TMP"/*.wav)
(( ${#wavs[@]} )) || { echo "no WAVs in $TMP" >&2; exit 1; }
for f in "${wavs[@]}"; do
  name="$(basename "$f" .wav)"
  nice -n 19 ffmpeg -nostdin -loglevel error -y -threads 2 -i "$f" -ac 1 -ar 24000 \
    -c:a libvorbis -q:a 2 -map_metadata -1 -fflags +bitexact -flags:a +bitexact "$OUT/$name.ogg"
done
echo "${#wavs[@]} previews in $OUT ($(du -ch "$OUT"/*.ogg | tail -1 | cut -f1)); report: $TMP/report.md"
