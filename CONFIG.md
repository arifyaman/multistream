# multistream configuration

The config is a single JSON file, found in this order (first match wins):

1. `-config /path/to/config.json` (any command)
2. `$MULTISTREAM_CONFIG`
3. the per-user config dir - `~/.config/multistream/config.json` on Linux,
   `~/Library/Application Support/multistream/config.json` on macOS,
   `%APPDATA%\multistream\config.json` on Windows
4. `/etc/multistream/config.json`
5. `./config.json`

Start from [config.example.json](config.example.json). Key values are never
stored in the config - only `${NAME}` templates that the daemon expands from
the key files (see [Keys](README.md#keys) in the README).

## Fields

- `mediamtx_api` (required) - base URL of the mediamtx HTTP API.
  `http://127.0.0.1:9997` when the CLI runs on the relay machine.
- `ingest_path` (required) - the path OBS pushes to (must match mediamtx's
  config), e.g. `live/<long-random-name>`.
- `ingest_port` - the RTMP port the relay listens on. Default `1935`.
- `refresh_sec` - default `--watch` refresh interval in seconds. Default `2`.
- `ffmpeg_path` - the ffmpeg binary the daemon spawns. Default `ffmpeg`
  (looked up on PATH). Set it when ffmpeg is not on PATH (common on Windows).
- `restart_sec` - how long the daemon waits after an ffmpeg exit before
  respawning it. Default `5`.
- `start_limit_interval_sec` / `start_limit_burst` - if a platform restarts
  more than `start_limit_burst` times within `start_limit_interval_sec`, the
  daemon stops respawning it and marks it `failed` (a manual
  `multistream restart <platform>` resets the limit). Defaults `60` and `5`.
- `away_file` - optional. The off-air placeholder MP4 that mediamtx loops
  while no publisher is connected (see
  [Off-air: the away file](README.md#off-air-the-away-file) in the README).
  `check` verifies the file exists and that mediamtx is new enough to play
  it. The file is only read by mediamtx, not by `multistream`.
- `keys_dir` - where the 0600 `<name>.env` key files live. The daemon reads
  them when it starts to expand the `${NAME}` templates in each push URL; the
  read-only commands only check that they exist and never print key values.
- `platforms[]` - one entry per platform:
  - `name` (required) - unique identifier; also the key-file stem
    (`<keys_dir>/<name>.env`).
  - `push_url` (required) - the RTMP(S) push URL. May contain `${NAME}`
    templates, expanded by the daemon from the platform's key file.

## Related

- The daemon's state (pid files, supervisor state, IPC endpoint) lives in the
  per-user state dir; override its location with `$MULTISTREAM_STATE`.
- `multistream config` prints the effective configuration without ever
  reading or printing key values.
