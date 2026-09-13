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

## Profiles

A config file holds one or more **profiles**: a profile is one user's whole
streaming chain - the relay endpoint to pull from, that user's keys, and the
platforms to re-broadcast to. Multiple profiles share the same relay service
(a single mediamtx): each profile pulls its own `ingest_path` from it.

```json
{
  "default_profile": "me",
  "profiles": {
    "me":     { "ingest_path": "live/<mine>",   "keys_dir": ".../keys/me",     "platforms": [ ... ] },
    "friend": { "ingest_path": "live/<theirs>", "keys_dir": ".../keys/friend", "platforms": [ ... ] }
  }
}
```

- `default_profile` - which profile commands use when `-profile` is not
  given. Optional; when absent, a profile named `default` is used, or the
  command fails with a list of the available profiles.
- Profile names must match `[a-z][a-z0-9_-]{0,31}` (they become directory
  names and part of the Windows pipe name).
- A file **without** a `profiles` map is a legacy single-profile file: its
  top-level fields are one implicit profile named `default`, exactly as in
  older versions. A file with a `profiles` map must not repeat profile
  fields at the top level (the loader refuses the mix).
- Across profiles, `(ingest_port, ingest_path)` must be unique: two profiles
  must never pull the same relay stream. Platform `name`s may repeat across
  profiles (they are per-profile, and each profile's keys live under its own
  `keys_dir`).
- A profile is self-contained: every field below is a profile field, so a
  profile can be copied or moved on its own.

Selecting a profile, highest priority first:

1. the `-profile <name>` flag (any command)
2. `$MULTISTREAM_PROFILE`
3. the `active` file next to the config file (written by `multistream switch`)
4. `default_profile`
5. a profile named `default`

Each profile runs its own daemon (`multistream -profile <name> daemon`) with
its own state, IPC endpoint and single-instance guard; in practice only one
profile's daemon runs at a time.

The enabled profile - the one a bare `multistream daemon` (or your service
unit) runs - is stored in the `active` file next to the config file. Set it
with `multistream switch <name>` and print it with bare `multistream switch`;
the switch never touches a running daemon, so restart the service to apply
it.

## Fields

These are the profile fields (inside each `profiles.<name>` entry, or at the
top level of a legacy file).

- `mediamtx_api` (required) - base URL of the mediamtx HTTP API.
  `http://127.0.0.1:9997` when the CLI runs on the relay machine. All
  profiles of one deployment normally point at the same relay service.
- `ingest_path` (required) - the path OBS pushes to (must match mediamtx's
  config), e.g. `live/<long-random-name>`. Unique per profile.
- `ingest_port` - the RTMP port the relay listens on. Default `1935`.
- `refresh_sec` - default `--watch` refresh interval in seconds. Default `2`.
- `ffmpeg_path` - the ffmpeg binary the daemon spawns. When unset, the
  daemon resolves it at start: PATH first, then the bundled runtime dir (the
  ffmpeg the npm package installed, exposed via `$MULTISTREAM_RUNTIME_DIR`).
  Set it to pin a specific binary; a set-but-missing path is an error rather
  than a silent fallback.
- `manage_mediamtx` - when `true`, the daemon spawns and supervises the
  mediamtx relay itself instead of expecting an externally managed one: it
  generates a minimal mediamtx config (RTMP + loopback API only, plus
  `alwaysAvailable` when `away_file` is set) in the state dir, starts it
  before the re-broadcasters, and restarts it on exit with the same rate
  limit as the platforms. If a mediamtx API is already reachable at
  `mediamtx_api` when the daemon starts, that external relay is tracked
  instead of spawning a second one. `mediamtx_api` must be a loopback
  address. Default `false`. In a multi-profile deployment the relay is
  usually the shared, externally managed one; if profiles manage their own
  relays, keep at most one managed daemon running at a time (or give each
  its own `ingest_port`/API port).
- `mediamtx_path` - the mediamtx binary used when `manage_mediamtx` is
  true. Resolved like `ffmpeg_path` (PATH, then the bundled runtime dir).
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
  it. The file is only read by mediamtx, not by `multistream`. With a shared
  relay, the mediamtx config carries one `alwaysAvailable` entry per ingest
  path.
- `keys_dir` - where the 0600 `<name>.env` key files live. Give each
  profile its own directory. The daemon reads the files when it starts to
  expand the `${NAME}` templates in each push URL; the read-only commands
  only check that they exist and never print key values.
- `platforms[]` - one entry per platform:
  - `name` (required) - unique within the profile; also the key-file stem
    (`<keys_dir>/<name>.env`).
  - `push_url` (required) - the RTMP(S) push URL. May contain `${NAME}`
    templates, expanded by the daemon from the platform's key file.

## Related

- The daemon's state (pid files, supervisor state, IPC endpoint, generated
  relay config) lives in the per-user state dir, in a **subdirectory per
  profile** (`<state>/<profile>/`). Override the root of that layout with
  `$MULTISTREAM_STATE`.
- **Upgrading from a single-profile (flat) setup:** stop the old daemon
  cleanly first (`systemctl --user stop multistream`, or let it exit on
  SIGTERM/SIGINT - it kills its ffmpeg processes and removes its pid files
  on the way out), install the new binary, and start `multistream daemon`.
  Existing state left flat in the state root is ignored; move it into
  `<state>/default/` or delete it, as you see fit. If the old daemon was
  killed ungracefully, kill its orphaned ffmpeg processes by hand (they are
  the ffmpeg whose command line contains the old ingest input URL) before
  starting the new daemon. The new daemon refuses to start while the old
  one's pid file in the state root points at a still-running multistream
  process, so the two can never supervise the same platforms at once.
- `$MULTISTREAM_RUNTIME_DIR` is set by the npm CLI shim to the bundled
  runtime dir (ffmpeg, mediamtx); it is the last place binary resolution
  looks. When the daemon manages the relay (`manage_mediamtx`), the
  generated mediamtx config lives in the profile's state dir as
  `mediamtx.generated.yml`.
- `multistream config` prints the effective configuration of the selected
  profile without ever reading or printing key values.
