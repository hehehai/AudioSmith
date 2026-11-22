#!/bin/bash
# curate-lockrun.sh: one stage of TestCuratedVoicesE2E on the Mac (run by
# make-curated-voices.sh from MAC_DIR; MODE and CURATE_* in the
# environment). The shared Mac's rules: waits for other apps' GPU
# engines to be idle and for ≥ 50 % free memory, holds /tmp/as-gpu.lock,
# kills the stage when free memory stays below 30 % (two readings, 3 s
# apart) or the disk falls below
# 6 GB (exit 3; stages resume where they stopped), one model loaded at a
# time, an isolated HOME, nice.
set -u
free() { memory_pressure | awk '/free percentage/ {print $NF+0}'; }
disk() { df -g ~ | tail -1 | awk '{print $4}'; }
# busy: another app's GPU engine using a CPU core or more.
busy() { ps -Ao pcpu=,command= | awk '/-engine --role gpu/ && !/awk/ && $1 > 50 {b=1} END {exit !b}'; }
for i in $(seq 1 120); do busy || break; [ "$i" = 1 ] && echo "waiting: another GPU engine is busy"; sleep 15; done
for i in $(seq 1 120); do
  f=$(free); [ "$f" -ge 50 ] && break
  [ $((i % 8)) = 1 ] && echo "waiting: memory only $f % free"; sleep 15
done
[ "$(free)" -ge 50 ] || { echo "memory below 50 % free for 30 min: stop"; exit 3; }
[ "$(disk)" -ge 6 ] || { echo "disk below 6 GB: stop"; exit 3; }
until mkdir /tmp/as-gpu.lock 2>/dev/null; do echo "lock held by: $(cat /tmp/as-gpu.lock/owner 2>/dev/null)"; sleep 20; done
echo "c94 curate $MODE $$ $(date +%T)" > /tmp/as-gpu.lock/owner
trap 'rm -rf /tmp/as-gpu.lock' EXIT
echo "start $MODE $(date +%T) free $(free) % disk $(disk) GB"
D=$PWD
# MyGo's SQLite library, from the user's cache (the isolated HOME has none).
mkdir -p "$D/home/Library/Caches/mygo"
[ -d "$D/home/Library/Caches/mygo/natives" ] || cp -R ~/Library/Caches/mygo/natives "$D/home/Library/Caches/mygo/" 2>/dev/null
# The models: links to the shared ActNow folder's (claims stay in the
# isolated HOME).
M="$D/home/Library/Application Support/ActNow/models/hub"
mkdir -p "$M"
for m in ~/"Library/Application Support/ActNow/models/hub"/*; do
  [ -e "$M/$(basename "$m")" ] || ln -s "$m" "$M/$(basename "$m")"
done
set -m
https_proxy=${https_proxy:-http://127.0.0.1:7897} HOME=$D/home TMPDIR=$D/tmp HF_HUB_CACHE=/Users/$USER/.cache/huggingface/hub AUDIOSMITH_MAX_MODELS=1 AUDIOSMITH_E2E=1 \
  AUDIOSMITH_ENGINE=$D/engine/audiosmith-engine CURATE_OUT=$D/out CURATE_MODE=$MODE \
  nice -n 10 ./curate.test -test.run 'TestCuratedVoicesE2E$' -test.v -test.timeout 8h > "$D/out/$MODE.log" 2>&1 &
pid=$!
low=100
under=0
while kill -0 $pid 2>/dev/null; do
  f=$(free); [ "$f" -lt "$low" ] && low=$f
  # what we use: the test and our engine's resident memory (MB)
  echo "$(date +%T) free $f % test $(ps -o rss= -p $pid 2>/dev/null | awk '{print int($1/1024)}') engine $(pgrep -f "$D/engine/audiosmith-engine" | xargs -n1 ps -o rss= -p 2>/dev/null | awk '{s+=$1} END {print int(s/1024)}')" >> "$D/out/$MODE.mem.log"
  # Two readings in a row under 30 % (a single dip of a few seconds at
  # a model load is not the stage's doing).
  if [ "$f" -lt 30 ]; then under=$((under + 1)); else under=0; fi
  if [ "$under" -ge 2 ] || [ "$(disk)" -lt 6 ]; then
    echo "watchdog: free $f %, disk $(disk) GB: stopping $MODE"; kill -TERM -$pid; sleep 3; kill -KILL -$pid 2>/dev/null
    pkill -f "$D/engine/audiosmith-engine" 2>/dev/null
    wait $pid; exit 3
  fi
  sleep 3
done
wait $pid; rc=$?
pkill -f "$D/engine/audiosmith-engine" 2>/dev/null
grep -E "\] (refs|pick|clone|asr|final) |failed|FAIL|PASS|^ok|panic" "$D/out/$MODE.log" | tail -60
echo "end $MODE $(date +%T) rc $rc free $(free) % (lowest $low %) disk $(disk) GB"
exit $rc
