#!/usr/bin/env bash
# Makes and measures the curated voices of Discover voices (thread #94,
# internal/gallery/data/curated.json) on the Mac with the real engine:
# TestCuratedVoicesE2E's stages, one model at a time, each under the
# shared GPU lock and a memory watchdog, with an isolated HOME (the user's
# AudioSmith is not touched) and nothing played.
#
#   MAC_DIR=code/AudioSmith-c94 scripts/make-curated-voices.sh setup     # mirror, engine, test binary
#   MAC_DIR=code/AudioSmith-c94 scripts/make-curated-voices.sh refs      # then pick, clone, asr
#   CURATE_ONLY=villain,grandpa scripts/make-curated-voices.sh refs      # some voices
#   scripts/make-curated-voices.sh pull                                  # results to $CURATE_TMP
#   scripts/make-curated-voices.sh encode picks.json                     # refs to Ogg (here), pushed back
#   scripts/make-curated-voices.sh final picks.json                      # previews + listening lines
#   CURATE_PREVIEWS=4 scripts/make-curated-voices.sh final picks.json    # and 3 more preview takes to choose from
#   scripts/make-curated-voices.sh install                               # Ogg clips to assets/gallery-voices
#   scripts/make-curated-voices.sh smoke                                 # the app with the bundle: curated and old voices in the Studio
#   scripts/make-curated-voices.sh clean                                 # delete the Mac scratch
#
# picks.json maps a voice id (without "c-") to the seed index chosen.
# Needs on the Mac: Go, an engine build (ENGINE_FROM, default the main
# checkout's resources/darwin-arm64/engine), Qwen3-TTS 1.7B VoiceDesign and
# Base, Kokoro, CustomVoice and Qwen3-ASR in the shared Hugging Face
# cache; here: ffmpeg with libvorbis.
set -euo pipefail

HOST="${MAC_HOST:-mac-air}"
export MAC_DIR="${MAC_DIR:-code/AudioSmith-c94}"
ENGINE_FROM="${ENGINE_FROM:-code/AudioSmith-v/resources/darwin-arm64/engine}"
TMP="${CURATE_TMP:-/tmp/as-curate-$USER}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REMOTE_OUT="$MAC_DIR/out"
stage="${1:?stage: setup, refs, pick, clone, asr, pull, encode, final, install, clean}"

remote() { ssh "$HOST" "$@"; }

# run_stage MODE [env...]: one test stage on the Mac, under the lock,
# detached (an SSH drop does not stop it); polled until it ends. Exit 3:
# stopped for memory or disk (the stages resume where they stopped).
run_stage() {
  local mode=$1; shift
  # shellcheck disable=SC2029
  remote "cd '$MAC_DIR' && rm -f out/$mode.rc && (MODE=$mode $* CURATE_ONLY='${CURATE_ONLY:-}' CURATE_SEEDS='${CURATE_SEEDS:-}' CURATE_PREVIEWS='${CURATE_PREVIEWS:-}' \
    nohup bash -c 'bash scripts/curate-lockrun.sh; echo \$? > out/$mode.rc' > out/$mode.stage.log 2>&1 < /dev/null &)"
  local rc=""
  while [ -z "$rc" ]; do
    sleep 60
    # shellcheck disable=SC2029
    rc=$(remote "cat '$MAC_DIR/out/$mode.rc' 2>/dev/null" 2>/dev/null || true)
  done
  remote "cat '$MAC_DIR/out/$mode.stage.log'" || true
  return "$rc"
}

case "$stage" in
setup)
  "$ROOT/scripts/mac.sh"
  # shellcheck disable=SC2029
  remote "set -e; cd '$MAC_DIR'; mkdir -p engine out home tmp
    rsync -a --delete ~/'$ENGINE_FROM'/ engine/
    export PATH=/opt/homebrew/bin:\$PATH GOTOOLCHAIN=auto GOPROXY=\${MAC_GOPROXY:-https://goproxy.cn,direct}
    nice -n 10 go test -c -o curate.test ./app && ls -la curate.test engine/audiosmith-engine"
  ;;
refs | pick | clone | asr | smoke)
  run_stage "$stage"
  ;;
final)
  picks="${2:?picks.json}"
  rsync -a "$picks" "$HOST:$REMOTE_OUT/picks.json"
  run_stage final CURATE_PICKS="\$PWD/out/picks.json"
  ;;
pull)
  mkdir -p "$TMP"
  rsync -a --exclude 'lib/' --exclude 'logs/' --exclude 'cand*.wav' "$HOST:$REMOTE_OUT/" "$TMP/"
  echo "results in $TMP"
  ;;
encode)
  # The picked references, as the app ships them: Ogg Vorbis, mono 24 kHz.
  picks="${2:?picks.json}"
  mkdir -p "$TMP/final"
  python3 -I - "$picks" "$TMP" <<'EOF'
import json, subprocess, sys
picks, tmp = json.load(open(sys.argv[1])), sys.argv[2]
for vid, k in picks.items():
    src = f"{tmp}/c-{vid}/s{k}/ref.wav"
    subprocess.run(["nice", "-n", "19", "ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-threads", "2", "-i", src, "-ac", "1", "-ar", "24000",
                    "-c:a", "libvorbis", "-q:a", "3", "-map_metadata", "-1", "-fflags", "+bitexact", "-flags:a", "+bitexact",
                    f"{tmp}/final/{vid}.ref.ogg"], check=True)
EOF
  remote "mkdir -p '$REMOTE_OUT/final'"
  rsync -a "$TMP/final/"*.ref.ogg "$HOST:$REMOTE_OUT/final/"
  ;;
install)
  out="$ROOT/assets/gallery-voices"
  shopt -s nullglob
  for f in "$TMP"/final/*.ref.ogg; do cp "$f" "$out/"; done
  for f in "$TMP"/final/c-*/preview.wav; do
    id="$(basename "$(dirname "$f")")"; id="${id#c-}"
    nice -n 19 ffmpeg -nostdin -loglevel error -y -threads 2 -i "$f" -ac 1 -ar 24000 \
      -c:a libvorbis -q:a 3 -map_metadata -1 -fflags +bitexact -flags:a +bitexact "$out/$id.preview.ogg"
  done
  du -ch "$out"/*.ogg | tail -1
  ;;
clean)
  remote "rm -rf '$MAC_DIR'; rmdir /tmp/as-gpu.lock 2>/dev/null; true"
  ;;
*)
  echo "unknown stage $stage" >&2; exit 2
  ;;
esac
