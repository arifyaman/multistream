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

It is a single static Go binary with zero dependencies. One of its commands
(`multistream daemon`) is a small supervisor that owns the ffmpeg
re-broadcasters - it spawns them, restarts them if they die, and keeps their
state. The other commands (`status`, `check`, ...) are read-only and work with
or without the daemon running. `status` exits `0` when everything is healthy
and `1` when anything is down, so it doubles as a health check you can put in
cron or an alert script.

It runs as **your normal user** - no dedicated service account, no groups.
Config, keys and state all live in your home directory.

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
  Kick stream. The `multistream` daemon runs one ffmpeg per platform and
  restarts it automatically (with a rate limit), so transient platform
  outages recover on their own while you keep streaming.
- **Your keys stay in 0600 files in your home config dir**, not in OBS.

`multistream` is how you *see* all of this while you stream: is OBS still
pushing? at what bitrate? is each platform actually connected - not just
"the process is running", but "it holds an open connection to the relay"?

## How it works

```
OBS ──RTMP (one upload)──▶ mediamtx :1935 ──┬──▶ ffmpeg ──▶ Twitch
                        (the relay)          ├──▶ ffmpeg ──▶ Kick
                                             └──▶ ffmpeg ──▶ YouTube
        the multistream daemon spawns + watches the ffmpeg processes

multistream status asks the daemon (and reads the mediamtx API), then
prints the table.
```

Every line is measured, not guessed:

- **ingest** - from the mediamtx HTTP API: is a publisher connected,
  inbound bitrate (delta between polls), resolution and codecs, and
  `readers N/M` = how many of your M re-broadcasters are pulling right now.
- **each platform** - from the daemon (alive? restarting? failed after too
  many restarts? how many restarts? last error?) plus a check of the ffmpeg
  process's actual network connections: it only counts as connected when it
  has an established TCP connection to the relay. `status` also works when
  the daemon is not running - it then falls back to the mediamtx API and the
  processes it can see, and tells you the daemon is down.

## Requirements

- `ffmpeg` for the re-broadcasting - or nothing: the **npm install and the
  release tarballs bundle a minimal remux-only ffmpeg** (plus mediamtx),
  checksum-verified at install time.
- `mediamtx` (the relay) - or nothing again: with `manage_mediamtx: true`
  the daemon runs and supervises the relay itself (the npm package and the
  release tarballs ship a mediamtx binary for this). Without that, install
  mediamtx separately - see [step 1](#1-install-the-relay-mediamtx) - or use
  [the relay on a VPS](#putting-the-relay-on-a-vps-optional).
- The `multistream` daemon runs on **Linux, macOS and Windows** with no
  systemd dependency - it spawns and supervises the ffmpeg processes itself.
  See [OS notes](#os-notes) for the per-OS differences.
- The read-only `status`/`check`/`config` commands run anywhere that can
  reach the mediamtx API and the daemon.

## OS notes

Everything works on all three OSes; what differs is how much the read-only
commands can verify, because the precise per-process checks are built on
`/proc` (Linux only):

- **Linux:** full fidelity. `status` verifies each ffmpeg holds an
  established TCP connection to the relay. If the daemon is not running,
  `status` can still see ffmpeg processes a previous daemon left behind - it
  reads the pid files that daemon wrote to the state dir and checks them
  against the process table (`/proc`). That is read-only diagnostics only:
  nothing is managed without the daemon. On start, the daemon itself clears
  such orphaned ffmpeg so a platform is never pushed twice.
- **macOS / Windows:** the daemon is the source of truth. A platform counts
  as connected when the daemon reports it `running` - sound, because an
  ffmpeg whose input is the relay would exit (and be restarted) if it were
  not pulling. Without the daemon there is no per-platform view (the ingest
  line still works - it comes from the mediamtx API). Orphan detection needs
  a graceful daemon stop; if you hard-kill the daemon and start it again,
  check for stray ffmpeg processes first.

Where the daemon's state lives (overridable with `$MULTISTREAM_STATE`):

- Linux: `~/.local/state/multistream`
- macOS: `~/Library/Application Support/multistream`
- Windows: `%LOCALAPPDATA%\multistream`

Keeping the daemon alive:

- **Linux:** a systemd unit - see [step 3](#3-start-the-daemon) (a user unit,
  or a system unit with `User=<you>`).
- **macOS:** a launchd agent. Save the following as
  `~/Library/LaunchAgents/com.arifyaman.multistream.plist`, with
  `ProgramArguments` set to your binary's path (find it with
  `command -v multistream`):

  ```xml
  <?xml version="1.0" encoding="UTF-8"?>
  <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
  <plist version="1.0">
  <dict>
      <key>Label</key><string>com.arifyaman.multistream</string>
      <key>ProgramArguments</key>
      <array>
          <string>/path/to/multistream</string>
          <string>daemon</string>
      </array>
      <key>RunAtLoad</key><true/>
      <key>KeepAlive</key><true/>
  </dict>
  </plist>
  ```

  Then load it:
  `launchctl load ~/Library/LaunchAgents/com.arifyaman.multistream.plist`
- **Windows:** a Task Scheduler task that runs `multistream.exe daemon` at
  logon (with the "restart if it fails" option), or simply a terminal you
  keep open. If you installed the bare multistream binary alone (not the npm
  package or the full tarball), set `ffmpeg_path` in your config - ffmpeg
  is usually not on PATH on Windows. npm installs and the full tarball
  bundle ffmpeg, so nothing to set.

## Install

**npm** (prebuilt binary for your platform, SHA-256 verified at install
time):

```
npm install -g @arifyaman/multistream
```

**Binary tarball** (one file with everything: the multistream binary plus
the bundled ffmpeg and mediamtx - no other installs needed): download
`multistream_<version>_<os>_<arch>.tar.gz` from the
[latest release](https://github.com/arifyaman/multiStream/releases/latest),
verify it against `SHA256SUMS.txt`, extract it, and put the folder on your
PATH:

```
tar -xzf multistream_*.tar.gz
sudo install -d /opt/multistream
sudo cp -r multistream_*/* /opt/multistream/
sudo ln -s /opt/multistream/multistream /usr/local/bin/
```

(On Windows: extract the tarball and add the folder to your PATH. The
`ffmpeg`/`mediamtx` binaries sit next to `multistream` in the same folder,
and the daemon finds them there. Windows ARM64 has no bundled runtime -
install ffmpeg and mediamtx separately there.)

Or build from source (Go >= 1.22, no external deps): `make build`.

## Quickstart on your own machine

Assumptions: a Linux box (your gaming/streaming PC or a home server) that
runs OBS and has internet access, plus a Twitch and/or Kick account.
Everything below lives on that one machine, and the `multistream` part runs as
**your normal user** - no dedicated account, no groups, and (for the daemon)
no root. Only the optional relay service in step 1 traditionally runs as a
system service.

### 1. Install the relay (mediamtx)

**Shortcut - let the daemon run the relay.** If you installed via npm (or
have a `mediamtx` binary the daemon can find), skip this whole step: set
`"manage_mediamtx": true` in your config and the daemon starts mediamtx
itself from a generated config (RTMP + loopback API, only the path you need),
supervises it like the other processes, and picks up `away_file`
automatically. Then go to [step 2](#2-configure-multistream).

Otherwise, install mediamtx by hand: grab the latest
[mediamtx release](https://github.com/bluenviron/mediamtx/releases)
for your platform (a `mediamtx_<version>_<os>_<arch>.tar.gz` tarball) and
install the binary from it:

```
tar -xzf mediamtx_*.tar.gz
sudo install -m755 mediamtx /usr/local/bin/mediamtx
```

Create `/etc/multistream/mediamtx.yml`:

```yaml
api: yes
apiAddress: 127.0.0.1:9997
paths:
  live/MY_LONG_RANDOM_NAME:
    source: publisher
```

- Pick `MY_LONG_RANDOM_NAME` as a long random string (32+ hex chars). It is
  your only "password": nobody who does not know it can push to your stream.
- mediamtx v1 requires every path to be **pre-configured** like this, or the
  publish is rejected.
- `apiAddress` stays on 127.0.0.1 - it is only used by `multistream` on the
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

### 2. Configure multistream

Everything `multistream` needs lives in your home config dir - nothing in
`/etc`, no special permissions. Keep each key in its own 0600 file, e.g.
`~/.config/multistream/keys/twitch.env`:

```
TWITCH_KEY=live_xxxxxxxxxxxxxxxxxxxx
```

Create `~/.config/multistream/config.json`:

```json
{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/MY_LONG_RANDOM_NAME",
  "ingest_port": 1935,
  "refresh_sec": 2,
  "keys_dir": "/home/YOUR_USER/.config/multistream/keys",
  "platforms": [
    { "name": "twitch",
      "push_url": "rtmp://live.twitch.tv/app/${TWITCH_KEY}" },
    { "name": "kick",
      "push_url": "rtmps://YOUR_KICK_CDN_HOST/${KICK_KEY}" }
  ]
}
```

`keys_dir` is used verbatim, so write an **absolute path** (the `~` in the
example above is only for readability). The daemon reads those files to expand
the `${TWITCH_KEY}` / `${KICK_KEY}` templates in the push URLs; the read-only
commands only check that the files exist and never print the keys.

### 3. Start the daemon

Run it in a terminal to try it:

```
multistream daemon
```

The daemon spawns one ffmpeg per platform and they start waiting: as soon as
OBS publishes, each one locks on and re-pushes automatically. If an ffmpeg
exits, the daemon restarts it (with a rate limit) - all inside the app, so it
works on any OS with no systemd dependency.

To keep it running (and restart it if it dies) **without root**, install a
systemd *user* unit, `~/.config/systemd/user/multistream.service`. Set
`ExecStart` to your binary's path (find it with `command -v multistream`):

```ini
[Unit]
Description=multistream re-broadcast supervisor
After=network-online.target

[Service]
ExecStart=/home/YOUR_USER/.local/bin/multistream daemon
Restart=always

[Install]
WantedBy=default.target
```

```
systemctl --user enable --now multistream
```

On a headless machine (a VPS with no desktop session) enable *linger* so the
user service keeps running after you log out:

```
sudo loginctl enable-linger $USER
```

(If you'd rather use a system service, a unit in `/etc/systemd/system/` with
`User=<your-user>` works the same way.)

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
twitch    DOWN  failed, restarts 3, connection refused
kick      UP  connected, restarts 1
```

In `--watch` mode a transition also prints an event line, e.g.
`14:32:05 twitch DOWN (failed)` or `14:35:10 ingest DROPPED`, so you catch
it even if the table is not on screen.

## Commands

```
multistream [status] [--watch] [--interval N] [--json] [--no-color]
multistream check
multistream restart <platform|relay>
multistream daemon
multistream config
```

| command | what it does |
|---|---|
| `status` | one-shot table (default command). `--watch` keeps refreshing and prints an event line when anything changes (ingest dropped, away file playing, platform down/up). `--json` for machines. Works with or without the daemon running. |
| `check` | probe the setup without streaming: mediamtx API reachable + version, the daemon is running, each push endpoint is TCP-reachable, each key file exists, away file present. Run this after setup. |
| `restart <platform\|relay>` | ask the daemon to restart one re-broadcaster, or the managed relay when `manage_mediamtx` is set (resets the target's restart limit). Refuses if the daemon is not running, since a restart without a supervisor would be unsupervised. |
| `daemon` | run the supervisor in the foreground: spawn one ffmpeg per platform and keep them alive, plus the mediamtx relay when `manage_mediamtx` is set. Keep it running with a service manager (see step 3). |
| `config` | print the effective configuration (key values are never read or printed). |

Global flags: `-config <file>`, `-version`, `-h`.

## Configuration

One JSON file - see [CONFIG.md](CONFIG.md) for the full field reference and
[config.example.json](config.example.json) for a template. It is searched for
in this order: `-config <file>`, `$MULTISTREAM_CONFIG`, the per-user config
dir (`~/.config/multistream/config.json` on Linux), `/etc/multistream/config.json`,
`./config.json`.

What you always set: `mediamtx_api`, `ingest_path`, `keys_dir`, and one
`platforms[]` entry per platform (`name` + `push_url` with a `${KEY}`
template). Everything else has a sensible default.

## Keys

Each platform's stream key lives in its own file under `keys_dir`, named
after the platform's `name`: `<keys_dir>/<name>.env`. The file is a plain env
file - one `NAME=VALUE` per line, with blank lines and `#` comments ignored
and surrounding quotes stripped:

```
# ~/.config/multistream/keys/twitch.env
TWITCH_KEY=live_xxxxxxxxxxxxxxxxxxxx
```

Only the variables used in the platform's `push_url` (`${NAME}`) matter;
extra variables are harmless. If a template has no matching key, the daemon
refuses to start that platform and says exactly which variable is missing.

Where to find the key:

- **Twitch:** creator dashboard → Settings → Stream → copy the stream key.
- **Kick:** creator dashboard → streaming settings → RTMP key.
- **YouTube:** the per-stream name from your YouTube live dashboard. It is
  not a long-lived secret, but keep it unguessable and unshared.

Security:

- Keep key files private: `0600` on Linux/macOS (create with `umask 077` or
  `chmod 600` afterwards); on Windows make sure only your own account can
  read them.
- Never put key *values* in `config.json` - the config only holds the
  `${NAME}` template, so it is safe to show, share or version-control.
- The daemon reads a key file when it starts, to build each ffmpeg command.
  The read-only commands never read or print key values: `check` only verifies
  the file exists, `config` prints the path, and any key that leaks into
  ffmpeg's output is replaced with `[redacted]` in the daemon's state and
  error lines.
- To rotate a key, edit its file and **restart the daemon** (for example
  `systemctl --user restart multistream`) - a platform's key is resolved once
  when the daemon starts, so `multistream restart <platform>` alone will not
  pick up the new value.

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

- **Kick:** `rtmps://<host>/<key>` - the host and key come from your Kick
  creator dashboard.
- **Twitch:** `rtmp://live.twitch.tv/app/<key>`. CBR ceiling and no B-frames
  as above; partner status raises the bitrate cap.
- **YouTube:** `rtmp://a.rtmp.youtube.com/live2/<stream_name>` - the "key"
  is a per-stream name from your YouTube dashboard.

## Off-air: the away file

When OBS is not publishing, the platforms would otherwise go dark. To keep
viewers on something instead, the relay can play an **away file** - a short
MP4 loop - on the ingest path whenever no publisher is connected. This is
mediamtx's built-in "always available" mode (mediamtx >= 1.16.3), so no
extra process is involved: mediamtx serves the file itself and swaps to the
live stream the moment OBS reconnects, without re-encoding and without
viewers reconnecting. The re-broadcast ffmpeg processes keep pulling through
the whole switch, which is why the platforms never drop.

### 1. Prepare the away file

Point mediamtx at any MP4 clip you like - a "be right back" card, a
highlight reel, an ad, whatever. Put it somewhere mediamtx can read, e.g.
`/etc/multistream/away.mp4`. Requirements: MP4, H.264 video + AAC audio, a
keyframe at the start (every MP4 has one), and a total duration of a few
minutes or less (it is played on repeat).

One constraint to know: while the away segment is playing, mediamtx
requires the incoming live stream's **audio** to match the away file's
(same codec, sample rate and channel count), or it rejects the publish
("audio configuration does not match"). Video may differ freely - the
resolution and level switch without issue. So keep the away file's audio at
AAC 48 kHz stereo, which matches OBS's default, or match your OBS audio
settings exactly.

### 2. Enable it in mediamtx

If the daemon manages the relay (`manage_mediamtx`), there is nothing to do -
the generated config already carries `alwaysAvailable` + your away file
whenever `away_file` is set; just (re)start the daemon.

Otherwise, in `/etc/multistream/mediamtx.yml`, extend the ingest path:

```yaml
paths:
  live/MY_LONG_RANDOM_NAME:
    source: publisher
    alwaysAvailable: true
    alwaysAvailableFile: /etc/multistream/away.mp4
```

Then `sudo systemctl restart mediamtx`. Without `alwaysAvailableFile`,
mediamtx serves a built-in black video + silence instead (then set
`alwaysAvailableTracks` to the codecs of your stream).

### 3. Tell multistream about it

Add the file to the multistream config so `check` can verify it:

```json
"away_file": "/etc/multistream/away.mp4"
```

### What you see

```
$ multistream status
ingest    AWAY  1.20 Mbps  1920x1080 h264  mpeg-4 audio  readers 2/2  away 1h05m
twitch    UP  connected, restarts 0
kick      UP  connected, restarts 0
```

`AWAY` means the away file is playing, the re-broadcasters are pushing it
to every platform, and the chain is waiting for the real stream. It is a
healthy state: the exit code stays `0`, so cron alerts only fire when
something is actually broken. When OBS starts, the line flips back to `UP`;
in `--watch` mode you get an event line on both transitions
(`ingest AWAY (offline segment, waiting for publisher)` and
`ingest PUBLISHING`).

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
internal/supervisor/   spawns + watches the ffmpeg processes (and the managed relay)
internal/daemonipc/    daemon <-> client request/response protocol
internal/state/        state dir, pid files, supervisor state document
internal/procscan/     /proc PID liveness + cmdline guard
internal/mediamtx/     mediamtx HTTP API client
internal/netmon/       /proc-based PID->connection reader (no root needed)
internal/report/       status collection, table/JSON rendering
internal/check/        deployment probe
internal/version/      build metadata (-ldflags)
ffbuild/               build farm for the bundled runtime (minimal ffmpeg + mediamtx)
npm/                   npm wrapper (postinstall binary + runtime download)
```

Conventions: stdlib only, no external Go modules; `golangci-lint`
(errcheck, staticcheck, goimports with local prefix); tests must pass
`-race`. CI runs on push/PR; tags `v*` produce a GitHub release
(raw binaries + tarballs + SHA256SUMS) and publish
`@arifyaman/multistream` to npm via trusted publishing.

## License

MIT - see [LICENSE](LICENSE).
