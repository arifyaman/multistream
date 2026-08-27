# Multi-Platform Streaming via VPS (OBS -> VPS -> Twitch/Kick/YouTube)

## Goal
Encode once in OBS, push one RTMP stream to the VPS, and have the VPS
re-broadcast the same H.264 stream to multiple platforms without any
video decoding or encoding on the VPS (remux only, `-c copy`).
A small status CLI runs on the VPS and shows the health of every link
(OBS -> mediamtx -> each platform) in the terminal.

## Decisions (2026-08-27)
- No Docker / no containers (limited RAM): native static binaries + systemd.
- The "app" is a terminal CLI (`multistream`) that prints stream status.

## Architecture
OBS/game box --RTMP, H.264--> VPS: mediamtx :1935  (native binary, systemd)
                                       |  -c copy, no encode
                     +-----------------+------------------+
                     v                v                  v
               Twitch          Kick           YouTube   (+future)
   (one ffmpeg systemd unit per platform, watched by the `multistream` CLI)

- Game box uploads 1x bitrate only (main win over pushing N streams directly).
- VPS CPU cost: ~2-5% per output. RAM: mediamtx ~30MB + ~40MB per ffmpeg
  process + CLI ~10MB => ~150-250MB for 3 platforms (no Docker daemon overhead).
- Latency: standard latency RTMP end to end (~5-30s per platform pipeline).
  No WebRTC/WHIP/low-latency modes; those would force transcode.

## VPS setup (no Docker)
RAM budget (measured on the VPS): 1.9Gi total, ~1Gi available, 0 swap.
Stack adds ~150-250MB (mediamtx ~30-50MB + 3x ffmpeg ~30-50MB each). Fits.
Actions: create a 2GB swap file first (no swap = OOM on any spike), and
identify what already uses ~900Mi (`ps aux --sort=-%mem | head -15`).
1. mediamtx v1.20.1 (native static Go binary, systemd unit) - RTMP ingress
   on 1935, HLS off, WebRTC off (saves RAM/CPU). ~30MB.
   - Install: single binary from GitHub releases -> /usr/local/bin/mediamtx
   - Unit: /etc/systemd/system/mediamtx.service, Restart=always, StartLimit*
   - Control API (9997) bound to 127.0.0.1 only (used by the CLI, not public)
   - v1 gotcha (verified locally): every path must be PRE-CONFIGURED in
     mediamtx.yml or the publish is rejected:
       paths:
         live/<name>:
           source: publisher
   - Long random stream name as the single ingress point.
   - Auth: mediamtx `onPublish` webhook, or at minimum the random stream name.
     Never expose the VPS ingress as a public open publish point.
2. Native ffmpeg (apt). One systemd service per platform (resilience: one
   platform failing kills nothing):
   - Twitch:  `ffmpeg -i rtmp://127.0.0.1:1935/live/<name> -c copy rtmp://live.twitch.tv/app/<key>`
   - Kick:    `ffmpeg -i rtmp://127.0.0.1:1935/live/<name> -c copy rtmp://live.kick.com/<key>`
   - YouTube: `ffmpeg -i rtmp://127.0.0.1:1935/live/<name> -c copy rtmp://a.rtmp.youtube.com/live2/<streamName>`
   - `Restart=always`, `RestartSec=5`, `StartLimitIntervalSec`/`StartLimitBurst`
     to avoid restart storms. Keys in 0600 env files, not in unit files.
   - Units run as a dedicated system user `multistream` (not root): the CLI
     reads /proc/<pid>/fd to verify each ffmpeg is actually connected to
     1935, which requires same-user (or root) access. The CLI is run as the
     same `multistream` user, plus `adm` group for journal reads.
3. Health/alerting: mediamtx `onUnpublish`/`onRead` hooks -> notify (e.g.
   Telegram) on OBS drop, for when you are not in the terminal.

GOTCHA (hit live 2026-08-28, Kick): ffmpeg 5.1 parses a single-segment RTMP
path `rtmps://host/KEY` as app=KEY + EMPTY stream name, and Kick rejects the
empty publish (symptom: TLS "IO error: End of file" right after push starts).
OBS sends app="" + name=KEY, which is why direct OBS tests work. Fix: use a
double slash so ffmpeg parses app="" + name=KEY:
  rtmps://kick-cdn.example.com//${KICK_KEY}
Rule of thumb: if a platform's URL is host/<key> (no app segment), always
write host//<key> in ffmpeg unit files.

## Status CLI (`multistream`)  [BUILT + TESTED locally 2026-08-28]
A small terminal app that shows the whole chain at a glance. Single static Go
binary, ZERO external Go deps (stdlib only), no runtime needed on the VPS.
Read-only: it never holds the RTMP keys.

Verified E2E on the dev box: real mediamtx v1.20.1 + testsrc2 push + ffmpeg
pull; `status` printed `ingest UP 2.05 Mbps 1280x720 h264 readers 1/1`.

Data sources (verified against the v1.20.1 OpenAPI):
- mediamtx Control API (http://127.0.0.1:9997):
  - `GET /v3/paths/get/{name}` -> one object with everything: `online`,
    `onlineTime`, `inboundBytes` (bitrate = delta between polls), `readers`
    (count = how many re-broadcasters pull), `tracks2` (codec + width/height)
  - `GET /v3/info` -> version (for `check`)
  - NOTE: older docs list `/v3/readers/list` + `/v3/connections/list`; those
    endpoints do NOT exist in v1.20.1 (readers are embedded in the path).
- systemd (per-platform ffmpeg units):
  - `systemctl show -p LoadState,ActiveState,SubState,NRestarts,MainPID multistream-<p>`
    -> is each push alive / failed / restart-looping
- /proc (per running platform): the ffmpeg PID's socket inodes
  (/proc/<pid>/fd) intersected with ESTABLISHED conns to 127.0.0.1:1935
  (/proc/net/tcp) -> "alive" vs "actually pulling"
- journalctl: last error-priority line per non-running unit -> surfaces
  push failures ("connection refused", "invalid stream key", ...)

Commands:
- `multistream status`               one-shot table (default; safe for cron)
- `multistream status --watch [sec]` live refresh (htop-style)
- `multistream status --json`        machine-readable (automation/alerts)
- `multistream check`                probe each platform RTMP endpoint (dry run)
- `multistream restart <platform>`   restart one re-broadcaster (systemctl)
- `multistream config`               show config + where secrets live

Example output (real, from the local E2E run):
  ingest    UP  2.05 Mbps  1280x720 h264  mpeg-4 audio  readers 1/1  up 4m34s
  twitch    DOWN  unit not found

Fully healthy it reads:
  ingest    UP  6.02 Mbps  1920x1080 h264  mpeg-4 audio  readers 3/3  up 2h14m
  twitch    UP  connected, restarts 0
  kick      UP  connected, restarts 0
  youtube   DOWN  failed/failed, restarts 3, connection refused

(fps is not exposed by the mediamtx API; resolution is.)

Config: /etc/multistream/config.json (zero-dep choice: JSON, not YAML)
- mediamtx_api, ingest_path, ingest_port (default 1935), refresh_sec
- keys_dir: where the 0600 key env files live (<name>.env per platform)
- platforms: [{name, unit, push_url}] - push_url keeps the ${KEY} template;
  the real keys stay in the 0600 env files, never in this file.
- See config.example.json in the repo for the full shape.

## OBS settings (VPS cannot fix these later)
- Encoder: x264 (hardware encoder ok, must be H.264, NOT HEVC -
  Twitch and Kick do not accept H.265 RTMP ingest)
- b-frames: 0 (Twitch rejects B-frames; most common silent failure)
- Rate control: CBR, 6000 kbps (safe ceiling for Twitch non-partner, Kick, YouTube)
- 1080p, 30 or 60 fps, AAC 160 kbps, High profile (Level 4.1 for 1080p30, 4.2 for 1080p60)

## Bandwidth
- ~1.9 TB per month per direction per 6 Mbps 24/7 stream.
- Budget: 1 in + N out. 4 platforms = ~9.5 TB/month.
- ACTION: check VPS traffic cap and upload headroom (24 Mbps sustained out for 4x6Mbps).

## Constraints / non-goals
- No decode or encode on the VPS, ever. If a requirement ever forces it
  (per-platform quality, H.265 source), revisit with GPU/NVENC.
- No Docker / no containers (limited RAM); native binaries + systemd only.
- No low-latency ingest; standard RTMP only.
- All platforms get identical stream (same bitrate/resolution).

## Open questions
- [ANSWERED] Platform list: kick + twitch (youtube later). Ingest path
  `live/REPLACE_WITH_YOUR_LONG_RANDOM_STREAM_NAME` (deployed 2026-08-28).
  Ingest URL for OBS: rtmp://YOUR_VPS_IP:1935/live/REPLACE_WITH_YOUR_LONG_RANDOM_STREAM_NAME
- Twitch Partner status (affects max bitrate; using 6000 kbps ceiling for now)
- VPS traffic cap (Hetzner-style cloud; region close to game box)
- Target resolution/fps (OBS side; 1080p30/60 assumed)
- [ANSWERED] CLI language: Go (implemented, zero external deps)
- Follow-up: if game box has a static public IP, restrict port 1935 to it
  with an nftables rule. If OBS cannot connect, check the cloud (Hetzner)
  firewall allows inbound TCP 1935.
- Follow-up: rotate both stream keys (they were shared in plaintext).
- Motivation check: is game box upload the bottleneck? If not, an OBS
  MultiRTMP plugin could skip the VPS (costs N x upload from game box)
- Future: ingest from phone/second device (VPS design already supports it)

## Rollout
1. Verify VPS caps (DONE: 1.9Gi RAM + 2G swap created 2026-08-27;
   ~900Mi used by the existing production stack - do not disturb).
2. Deploy CLI + install mediamtx v1.20.1 (native) + ffmpeg + per-platform
   systemd units (user `multistream`).
3. DONE: `multistream` CLI built in this repo (Go, stdlib only), unit-tested
   and E2E-tested locally against real mediamtx v1.20.1.
4. OBS config; 5. Test each platform with a short stream;
   6. Monitoring + alerts (webhook + CLI watch);
   7. Optional: SaaS fallback knowledge (Restream.io) if VPS becomes a burden.
