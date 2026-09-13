# AGENTS.md

## Project overview

`multistream` is a small terminal dashboard for multi-platform streaming
(Twitch, Kick, YouTube at the same time). OBS pushes once to a
[mediamtx](https://github.com/bluenviron/mediamtx) relay; the `multistream`
daemon then spawns and supervises one ffmpeg per platform that re-pushes the
stream with `-c copy` (rewrap only, no re-encoding). `multistream status`
shows one compact table covering ingest (from the mediamtx HTTP API) and each
platform (from the daemon plus per-process connection checks on Linux).

- Single static Go binary, zero external Go modules (stdlib only).
- `multistream daemon` owns the ffmpeg (and optionally the mediamtx)
  processes; the other commands (`status`, `check`, `config`, ...) are
  read-only and work with or without the daemon.
- `status` exits 0 when everything is healthy, 1 when anything is down, so it
  doubles as a health check for cron or alerting.
- Runs as the normal user; config, keys and state live in the home directory.
  See `README.md` and `CONFIG.md` for the user-facing documentation.

## Repository layout

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
test/                  test fixtures
```

## Go module and build

- Module: `github.com/xlip/multistream`, Go 1.22.
- Build: `make build` (outputs `bin/multistream` with version ldflags).
- Run: `make run ARGS="status --watch"`.
- Config: see `config.example.json`; documented in `CONFIG.md`.

## Common commands

```
make build   build the binary into bin/ with version info
make run     run from source (ARGS="status --watch" etc.)
make test    go test ./...
make race    go test -race ./...
make vet     go vet ./...
make fmt     gofmt -w .
make lint    golangci-lint run ./...
make clean   remove build artifacts
```

## Conventions

- Stdlib only: do not add external Go dependencies.
- Lint config is `.golangci.yml` (golangci-lint v2 format); goimports uses the
  local prefix `github.com/xlip/multistream`.
- Tests must pass with the race detector (`make race`).
- CI (`.github/workflows/ci.yml`) runs gofmt, vet, `go test -race ./...` and
  golangci-lint on push/PR.
- Tags `v*` produce a GitHub release (raw binaries + tarballs + SHA256SUMS)
  and publish `@arifyaman/multistream` to npm via trusted publishing.

## Push rules

- `golangci-lint` is a **requirement before pushing to remote**: always run
  `make lint` (i.e. `golangci-lint run ./...`) and make sure it passes
  cleanly before `git push`. Fix or justify any lint findings; do not push
  with lint failures.
