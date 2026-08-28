# multistream

A small Go CLI that monitors a multi-platform RTMP re-broadcast chain:

```
OBS  ->  VPS (mediamtx ingest)  ->  Twitch, Kick, ...
```

It queries the mediamtx HTTP API and systemd, and prints a compact status
table in your terminal. Single static binary, zero runtime dependencies,
no daemon.

```
$ multistream status
multistream status  (config: /etc/multistream/config.json)

ingest    UP    7.08 Mbps, 1920x1080 h264, mpeg-4 audio, readers 2/2, up 1h12m
twitch    UP    connected, restarts 0
kick      UP    connected, restarts 0
```

Exit code is `0` when everything is healthy, `1` when anything is down -
suitable for cron/health checks.

## Build

Go >= 1.22, no external modules.

```
make build    # -> bin/multistream (version stamped from git describe)
make test     # go test ./...
make race     # go test -race ./...
make lint     # golangci-lint run ./...
```

Manual build with version stamping:

```
go build -trimpath \
  -ldflags "-s -w \
    -X github.com/xlip/multistream/internal/version.Version=$(git describe --tags --always) \
    -X github.com/xlip/multistream/internal/version.Commit=$(git rev-parse --short HEAD)" \
  -o multistream ./cmd/multistream
```

## Install

**npm** (downloaded prebuilt binary, SHA-256 verified at install time):

```
npm install -g @arifyaman/multistream
```

**Binary** (any OS/arch, or from the [GitHub releases](https://github.com/xlip/multiStream/releases)):

```
curl -LO https://github.com/xlip/multiStream/releases/download/v2027.1.0-alpha.1/multistream_2027.1.0-alpha.1_linux_amd64
install -m755 multistream_2027.1.0-alpha.1_linux_amd64 /usr/local/bin/multistream
```

## Configuration

JSON file, searched in this order:

1. `-config /path/to/config.json` flag (any command)
2. `$MULTISTREAM_CONFIG`
3. `/etc/multistream/config.json`
4. `./config.json`

```json
{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/REPLACE_WITH_YOUR_LONG_RANDOM_STREAM_NAME",
  "ingest_port": 1935,
  "refresh_sec": 2,
  "keys_dir": "/etc/multistream/keys",
  "platforms": [
    { "name": "twitch", "unit": "multistream-twitch",
      "push_url": "rtmp://live.twitch.tv/app/${TWITCH_KEY}" },
    { "name": "kick", "unit": "multistream-kick",
      "push_url": "rtmps://kick-cdn.example.com//${KICK_KEY}" }
  ]
}
```

- `mediamtx_api` - mediamtx HTTP API base URL (must be reachable from where
  the CLI runs; on the VPS itself this is localhost).
- `ingest_path` - the path OBS pushes to (mediamtx `publishers`).
- `platforms[].unit` - systemd unit of the re-broadcaster (no `.service`
  suffix).
- `platforms[].push_url` - may contain `${VAR}` placeholders, replaced from
  `<keys_dir>/<name>.env` files (`KEY=VALUE`, mode 0600).
- Gotcha: Kick's rtmps URL needs a **double slash** before the key
  (`...live-video.net//KEY`) - ffmpeg treats a single-segment path as
  `app=KEY` with an empty stream name, which Kick rejects.

## Commands

```
multistream [status] [--watch] [--interval N] [--json] [--no-color]
multistream check
multistream restart <platform>
multistream config
```

- `status` - one-shot table (default command). `--watch` keeps refreshing
  and prints an event line when anything changes (ingest dropped, platform
  down/up, resolution or codec change). `--json` for machines.
- `check` - deployment probe without streaming: mediamtx API version,
  unit existence, push endpoint TCP reachability, key file presence.
- `restart <platform>` - `systemctl restart` one re-broadcaster.
- `config` - print the effective configuration (secrets redacted).

Global: `-config <file>`, `-version`, `-h`.

## VPS deployment

The re-broadcast chain itself runs on the VPS: `mediamtx` ingests, one
`ffmpeg` systemd unit per platform re-pushes. Full deployment guide, RAM
budget and gotchas: [initial-plan.md](initial-plan.md).

Typical units:

```
/etc/systemd/system/mediamtx.service
/etc/systemd/system/multistream-twitch.service
/etc/systemd/system/multistream-kick.service
```

The CLI runs on the VPS (localhost mediamtx API) and/or anywhere that can
reach it.

## Development

```
cmd/multistream/       thin entrypoint
internal/cli/          flags, dispatch, command runners
internal/config/       config loading, ${VAR} key expansion
internal/mediamtx/     mediamtx HTTP API client
internal/netmon/       /proc-based PID->connection reader (no root needed)
internal/report/       status collection, table/JSON rendering
internal/check/        deployment probe
internal/systemd/      systemctl wrappers
internal/version/      build metadata (-ldflags)
npm/                   npm wrapper (postinstall binary download)
```

Conventions: stdlib only, no external Go modules; `golangci-lint`
(errcheck, staticcheck, goimports with local prefix); tests must pass
`-race`. CI runs on push/PR; tags `v*` produce GitHub releases
(raw binaries + tarballs + SHA256SUMS) and, when the `NPM_TOKEN` secret is
set, publish `@arifyaman/multistream` to npm.

## License

MIT - see [LICENSE](LICENSE).
