#!/usr/bin/env bash
# smoke-test.sh - proves the minimal ffmpeg build can do the job:
# mediamtx relays a file-push to path A; the minimal ffmpeg pulls A and
# re-pushes (exactly the daemon's command shape) to path B; then we
# verify both paths via the mediamtx API and ffprobe the final output.
#
# Usage: smoke-test.sh <ffmpeg-binary> <mediamtx-binary> <sample.flv> <away.mp4>
set -euo pipefail

FFMPEG="${1:?usage: smoke-test.sh <ffmpeg> <mediamtx> <sample.flv> <away.mp4>}"
MEDIAMTX="${2:?}"
SAMPLE="${3:?}"
AWAY="${4:?}"
abs() { (cd "$(dirname "$1")" && printf '%s/%s' "$(pwd)" "$(basename "$1")"); }
FFMPEG="$(abs "$FFMPEG")"
MEDIAMTX="$(abs "$MEDIAMTX")"
SAMPLE="$(abs "$SAMPLE")"
AWAY="$(abs "$AWAY")"
for f in "$FFMPEG" "$MEDIAMTX" "$SAMPLE" "$AWAY"; do
  if [ ! -f "$f" ]; then
    echo "FAIL: required input missing: $f"
    exit 1
  fi
done
# The artifact round-trip can drop the executable bit; fail loud rather than
# die deep in the test with "Permission denied".
for f in "$FFMPEG" "$MEDIAMTX"; do
  if [ ! -x "$f" ]; then
    echo "FAIL: not executable: $f"
    exit 1
  fi
done
WORK="$(mktemp -d)"
# The || true matters: on the success path all jobs are already reaped, so
# kill gets no arguments and errors; under set -e that would override the
# exit code and fail a passing test.
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$WORK"' EXIT
cd "$WORK" # mediamtx may generate cert files in the CWD

cat > "$WORK/mediamtx.yml" <<EOF
api: yes
apiAddress: 127.0.0.1:9997
rtmp: yes
rtmpAddress: 127.0.0.1:1935
hls: no
rtsp: no
srt: no
webrtc: no
moq: no
logLevel: info
paths:
  live/a:
    source: publisher
    alwaysAvailable: true
    alwaysAvailableFile: $AWAY
  live/b:
    source: publisher
EOF

"$MEDIAMTX" "$WORK/mediamtx.yml" > "$WORK/mediamtx.log" 2>&1 &
MTX_PID=$!
sleep 1
if ! kill -0 "$MTX_PID" 2>/dev/null; then
  echo "FAIL: mediamtx exited at startup"
  echo "--- mediamtx.log ---"; cat "$WORK/mediamtx.log"
  exit 1
fi

# Wait for the API before anything connects to it. A cold start on a small
# runner can take a few seconds, and the relay must connect to a live
# mediamtx exactly like the daemon does.
for i in $(seq 1 20); do
  if curl -s http://127.0.0.1:9997/v3/paths/list >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! curl -s http://127.0.0.1:9997/v3/paths/list >/dev/null 2>&1; then
  echo "FAIL: mediamtx API did not come up within 20s"
  echo "--- mediamtx.log ---"; cat "$WORK/mediamtx.log"
  exit 1
fi

# re-broadcaster: exactly the daemon's command shape (A -> B, -c copy).
# It starts while path A serves the away segment, like the daemon does.
"$FFMPEG" -hide_banner -loglevel warning \
  -i rtmp://127.0.0.1:1935/live/a \
  -c copy -f flv rtmp://127.0.0.1:1935/live/b > "$WORK/relay.log" 2>&1 &
RELAY_PID=$!
sleep 2

# publisher: loop the sample file into path A (switches A from away to live)
"$FFMPEG" -hide_banner -loglevel warning -stream_loop -1 -re \
  -i "$SAMPLE" -c copy -f flv rtmp://127.0.0.1:1935/live/a > "$WORK/pub.log" 2>&1 &
PUB_PID=$!

# wait until both paths are online (live publisher, not the away segment)
# The || echo keeps a refused connection from killing the script under
# set -e: an unreachable API reads as "offline" and the loop keeps going.
online() {
  curl -s "http://127.0.0.1:9997/v3/paths/get/$1" 2>/dev/null \
    | python3 -c "import json,sys; print(json.load(sys.stdin)['online'])" 2>/dev/null \
    || echo "offline"
}
for i in $(seq 1 30); do
  sleep 1
  a_on="$(online live/a)"
  b_on="$(online live/b)"
  if [[ "$a_on" == "True" && "$b_on" == "True" ]]; then break; fi
done
if [[ "$a_on" != "True" || "$b_on" != "True" ]]; then
  echo "FAIL: paths not online (a=$a_on b=$b_on)"
  echo "--- mediamtx.log ---"; cat "$WORK/mediamtx.log"
  echo "--- pub.log ---"; cat "$WORK/pub.log"
  echo "--- relay.log ---"; cat "$WORK/relay.log"
  exit 1
fi

# path A must have exactly 1 reader (the re-broadcaster)
readers="$(curl -s http://127.0.0.1:9997/v3/paths/get/live/a 2>/dev/null \
  | python3 -c "import json,sys; print(len(json.load(sys.stdin)['readers']))" 2>/dev/null || echo 0)"
if [[ "$readers" != "1" ]]; then
  echo "FAIL: path live/a readers = $readers, want 1"
  exit 1
fi

# pull path B to a file and verify it is valid h264+aac
"$FFMPEG" -hide_banner -loglevel warning -t 5 -i rtmp://127.0.0.1:1935/live/b -c copy -f flv "$WORK/out.flv"
kill "$PUB_PID" "$RELAY_PID" "$MTX_PID" 2>/dev/null || true
wait 2>/dev/null || true

info="$(ffprobe -hide_banner "$WORK/out.flv" 2>&1)"
if grep -q "Video: h264" <<< "$info" && grep -q "Audio: aac" <<< "$info"; then
  echo "PASS: minimal ffmpeg remuxed live/a -> live/b (h264+aac verified)"
else
  echo "FAIL: output stream missing h264/aac"
  echo "$info"
  exit 1
fi
