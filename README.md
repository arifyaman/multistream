# multistream

**Stream to Twitch, Kick and YouTube at the same time - and actually know
what is live where.**

`multistream` is a small terminal dashboard for multi-platform streaming.
It watches your whole stream chain - OBS, the relay, and every platform -
and shows it in one compact table:

```
$ multistream status
ingest    UP  7.08 Mbps  1920x1080 h264  mpeg-4 audio  readers 2/2  up 1h12m
twitch    UP  connected, restarts 0
kick      UP  connected, restarts 0
```

It is a single static Go binary with zero dependencies and no daemon.
Exit code is `0` when everything is healthy and `1` when anything is down,
so it doubles as a health check you can put in cron or an alert script.

## Why relay instead of pushing directly from OBS?

The naive way to stream to three platforms is three separate RTMP
connections from OBS. You can, but:

- **You upload three times.** Every frame leaves your machine three times,
  for the whole stream, every stream.
- **One flaky platform can hurt the others.** Encoder load and upload
  spikes are shared, and a stuck connection is hard to spot mid-stream.
- **Your stream keys live in OBS**, sitting in its settings on disk.

The relay approach fixes all three. OBS pushes **once** to a small relay
program ([mediamtx](https://github.com/bluenviron/mediamtx)) on a machine
you control. Then one tiny `ffmpeg` process per platform pulls the stream
from the relay and re-pushes it - `-c copy`, so it only rewraps the
stream, no re-encoding. From there:

- **You upload once.** The relay fans out locally; it costs a couple of
  percent CPU per platform, not your upload.
- **Every platform gets the identical stream** - same bitrate, resolution
  and encoder settings, because it is literally the same stream.
- **Failures are isolated.** Twitch having a bad day does not touch your
  Kick stream. Each ffmpeg is a systemd unit with `Restart=always`, so
  transient platform outages recover on their own while you keep streaming.
- **Your keys stay in 0600 files next to the relay**, not in OBS.

`multistream` is how you *see* all of this while you stream: is OBS still
pushing? at what bitrate? is each platform actually connected - not just
"the process is running", but "it holds an open connection to the relay"?

## How it works

```
OBS ──RTMP (one upload)──▶ mediamtx :1935 ──┬──▶ ffmpeg ──▶ Twitch
                       (the relay)          ├──▶ ffmpeg ──▶ Kick
                                            └──▶ ffmpeg ──▶ YouTube

multistream polls the mediamtx API, systemd and /proc on the
relay machine, then prints the table.
```

Every line is measured, not guessed:

- **ingest** - from the mediamtx HTTP API: is a publisher connected,
  inbound bitrate (delta between polls), resolution and codecs, and
  `readers N/M` = how many of your M re-broadcasters are pulling right now.
- **each platform** - from its systemd unit (alive? failed?
  restart-looping? how many restarts?) plus a check of the ffmpeg process's
  actual network connections: it only counts as connected when it has an
  established TCP connection to the relay.

## Requirements

- **Linux + systemd** on the machine that runs the relay and the ffmpeg
  units - that is where the full per-platform status comes from.
- `mediamtx` (the relay) and `ffmpeg` on that machine.
- The `multistream` binary itself runs anywhere that can reach the mediamtx
  API, but the per-platform lines (unit state, connection check) are
  systemd/Linux-only, so this project targets Linux hosts.

## Install

Prebuilt binaries for linux/darwin/windows (amd64/arm64) are attached to
every [GitHub release](https://github.com/arifyaman/multiStream/releases):

```
curl -LO https://github.com/arifyaman/multiStream/releases/download/v2027.1.0-alpha.1/multistream_2027.1.0-alpha.1_linux_amd64
install -m755 multistream_2027.1.0-alpha.1_linux_amd64 /usr/local/bin/multistream
```

Or build from source (Go >= 1.22, no external deps): `make build`.

## Quickstart on your own machine

Assumptions: a Linux box (your gaming/streaming PC or a home server) that
runs OBS and has internet access, plus a Twitch and/or Kick account.
Everything below lives on that one machine.

### 1. Install the relay (mediamtx)

```
curl -LO https://github.com/bluenviron/mediamtx/releases/download/v1.20.1/mediamtx_v1.20.1_linux_amd64.tar.gz
tar -xzf mediamtx_v1.20.1_linux_amd64.tar.gz
sudo install -m755 mediamtx /usr/local/bin/mediamtx
```

Create `/etc/multistream/mediamtx.yml`:

```yaml
api: yes
apiAddr: 127.0.0.1:9997
paths:
  live/MY_LONG_RANDOM_NAME:
    source: publisher
```

- Pick `MY_LONG_RANDOM_NAME` as a long random string (32+ hex chars). It is
  your only "password": nobody who does not know it can push to your stream.
- mediamtx v1 requires every path to be **pre-configured** like this, or the
  publish is rejected.
- `apiAddr` stays on 127.0.0.1 - it is only used by `multistream` on the
  same machine.

Run it under systemd (`/etc/systemd/system/mediamtx.service`):

```ini
[Unit]
Description=multistream relay (mediamtx)
After=network-online.target

[Service]
ExecStart=/usr/local/bin/mediamtx /etc/multistream/mediamtx.yml
Restart=always

[Install]
WantedBy=multi-user.target
```

### 2. One ffmpeg unit per platform

Keep each key in its own 0600 file, e.g. `/etc/multistream/keys/twitch.env`:

```
TWITCH_KEY=live_xxxxxxxxxxxxxxxxxxxx
```

Then one systemd unit per platform. Twitch
(`/etc/systemd/system/multistream-twitch.service`):

```ini
[Unit]
Description=multistream re-broadcast to Twitch
After=network-online.target mediamtx.service

[Service]
EnvironmentFile=/etc/multistream/keys/twitch.env
ExecStart=/usr/bin/ffmpeg -hide_banner -loglevel warning \
    -i rtmp://127.0.0.1:1935/live/MY_LONG_RANDOM_NAME \
    -c copy -f flv rtmp://live.twitch.tv/app/${TWITCH_KEY}
Restart=always
RestartSec=5
StartLimitIntervalSec=60
StartLimitBurst=5

[Install]
WantedBy=multi-user.target
```

Repeat for Kick (and YouTube) with the platform's push URL - see
[Platform notes](#platform-notes) for the URL quirks. Then:

```
sudo systemctl daemon-reload
sudo systemctl enable --now mediamtx multistream-twitch multistream-kick
```

The ffmpeg units start and wait: as soon as OBS publishes, each one locks
on and re-pushes automatically.

### 3. Configure multistream

`/etc/multistream/config.json`:

```json
{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/MY_LONG_RANDOM_NAME",
  "ingest_port": 1935,
  "refresh_sec": 2,
  "keys_dir": "/etc/multistream/keys",
  "platforms": [
    { "name": "twitch", "unit": "multistream-twitch",
      "push_url": "rtmp://live.twitch.tv/app/${TWITCH_KEY}" },
    { "name": "kick", "unit": "multistream-kick",
      "push_url": "rtmps://YOUR_KICK_CDN_HOST//${KICK_KEY}" }
  ]
}
```

### 4. Point OBS at the relay

In OBS → Settings → Stream:

- **Server:** `rtmp://127.0.0.1:1935/live` (or your machine's LAN/VPS
  address if OBS runs elsewhere)
- **Stream key:** `MY_LONG_RANDOM_NAME`

That's it - OBS now pushes once, and the relay fans out to every platform.

### 5. Watch it

```
multistream status --watch
```

Start streaming in OBS: the `ingest` line flips to UP with your bitrate
and resolution, and `readers` climbs to `M/M` as each platform locks on.

When something breaks, you see exactly which link broke - here Twitch's
ingest is refusing connections (bad key, outage, rate limit) while Kick
keeps streaming fine:

```
$ multistream status
ingest    UP  7.08 Mbps  1920x1080 h264  mpeg-4 audio  readers 1/2  up 1h12m
twitch    DOWN  failed/failed, restarts 3, connection refused
kick      UP  connected, restarts 1
```

In `--watch` mode a transition also prints an event line, e.g.
`14:32:05 twitch DOWN (failed)` or `14:35:10 ingest DROPPED`, so you catch
it even if the table is not on screen.

## Commands

```
multistream [status] [--watch] [--interval N] [--json] [--no-color]
multistream check
multistream restart <platform>
multistream config
```

| command | what it does |
|---|---|
| `status` | one-shot table (default command). `--watch` keeps refreshing and prints an event line when anything changes (ingest dropped, platform down/up, resolution change). `--json` for machines. |
| `check` | probe the setup without streaming: mediamtx API reachable + version, each unit exists, each push endpoint is TCP-reachable, each key file exists. Run this after setup. |
| `restart <platform>` | restart one re-broadcaster (`systemctl restart`). |
| `config` | print the effective configuration (key values are never read or printed). |

Global flags: `-config <file>`, `-version`, `-h`.

## Configuration

JSON file, searched in this order:

1. `-config /path/to/config.json` (any command)
2. `$MULTISTREAM_CONFIG`
3. `/etc/multistream/config.json`
4. `./config.json`

- `mediamtx_api` - base URL of the mediamtx HTTP API. `http://127.0.0.1:9997`
  when the CLI runs on the relay machine.
- `ingest_path` - the path OBS pushes to (must match mediamtx's config).
- `ingest_port` - RTMP port, default 1935.
- `refresh_sec` - default `--watch` interval in seconds.
- `keys_dir` - where the 0600 `<name>.env` key files live. `multistream`
  only checks that they exist; it never reads or prints key values. The
  ffmpeg units load the same files via `EnvironmentFile=`, which is what
  actually expands `${TWITCH_KEY}` in the push URL.
- `platforms[].unit` - the systemd unit name without `.service`.

## OBS settings that matter

The relay only rewraps - it cannot fix an incompatible source stream:

- **Encoder:** x264 / any H.264 hardware encoder. **Not HEVC** - Twitch and
  Kick reject H.265 RTMP ingest.
- **b-frames: 0.** Twitch rejects B-frames; this is the most common silent
  "my stream is up but no viewers" failure.
- **Rate control:** CBR, 6000 kbps is a safe ceiling for Twitch
  non-partner, Kick and YouTube.
- 1080p30/60, AAC audio 160 kbps.

## Platform notes

- **Kick:** its rtmps URL needs a **double slash** before the key -
  `rtmps://host//KEY`, not `rtmps://host/KEY`. ffmpeg parses a
  single-segment path as `app=KEY` with an *empty* stream name, and Kick
  rejects the empty publish (symptom: TLS "End of file" right after the
  push starts). Rule of thumb: if a platform's URL is `host/<key>` with no
  app segment, always write `host//<key>` in the ffmpeg unit.
- **Twitch:** `rtmp://live.twitch.tv/app/<key>`. CBR ceiling and no B-frames
  as above; partner status raises the bitrate cap.
- **YouTube:** `rtmp://a.rtmp.youtube.com/live2/<stream_name>` - the "key"
  is a per-stream name from your YouTube dashboard.

## Putting the relay on a VPS (optional)

The same setup works on a small VPS instead of (or in addition to) your
home machine - OBS pushes to `rtmp://<vps>:1935/live/MY_LONG_RANDOM_NAME`
over the internet. Reasons to do it:

- Your home upload stays at 1x no matter how many platforms you add.
- The relay is close to the platforms' ingest servers (lower egress
  latency, and your home connection's outages stop mattering).
- 1-2 GB of RAM is plenty for 3-4 platforms (mediamtx ~30 MB, each ffmpeg
  ~40 MB, plus the CLI).

The full write-up - RAM budget, swap sizing, unit hardening, what we hit
in the wild - is in [initial-plan.md](initial-plan.md). Whatever host you
pick, keep the ingest path long and random, and restrict port 1935 to your
OBS machine's IP if it has one.

## Automation

- `status` exits `0` all-healthy, `1` anything down, `2` usage/config
  error - ideal for cron and alert hooks:

```
*/5 * * * * /usr/local/bin/multistream status >/dev/null || echo "stream chain degraded" >> /var/log/multistream-alerts
```

- `status --json` prints one JSON document per state for scripting
  (`{"ok":false,"ingest":{"online":true,...},"platforms":[...]}`).

## Development

```
cmd/multistream/       thin entrypoint
internal/cli/          flags, dispatch, command runners
internal/config/       config loading, key file locations
internal/mediamtx/     mediamtx HTTP API client
internal/netmon/       /proc-based PID->connection reader (no root needed)
internal/report/       status collection, table/JSON rendering
internal/check/        deployment probe
internal/systemd/      systemctl wrappers
internal/version/      build metadata (-ldflags)
```

Conventions: stdlib only, no external Go modules; `golangci-lint`
(errcheck, staticcheck, goimports with local prefix); tests must pass
`-race`. CI runs on push/PR; tags `v*` produce a GitHub release with
per-platform raw binaries, tarballs and SHA256SUMS.

## License

MIT - see [LICENSE](LICENSE).
