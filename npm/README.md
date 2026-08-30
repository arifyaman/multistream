# @arifyaman/multistream

npm wrapper for the [multistream](https://github.com/xlip/multiStream) CLI:
a terminal dashboard for the OBS -> mediamtx -> platforms RTMP re-broadcast
chain. It shows the live status of every link and - via its daemon - runs and
supervises the per-platform ffmpeg re-broadcasters and, with
`manage_mediamtx`, the mediamtx relay itself.

## Install

    npm install -g @arifyaman/multistream

The postinstall step downloads the prebuilt binary and the bundled runtime
(a minimal remux-only ffmpeg and mediamtx) for your platform from the GitHub
releases, verifies their SHA-256 checksums, and installs them under the
package. You do not need ffmpeg or mediamtx installed on your system. Set
`MULTISTREAM_SKIP_RUNTIME=1` before installing to skip the runtime and use
system-wide binaries from PATH instead.

## Usage

    multistream status --watch     # live table, events on change
    multistream check              # probe relay, daemon, endpoints, key files
    multistream daemon             # run the re-broadcaster supervisor
    multistream restart kick       # ask the daemon to restart one platform
    multistream config             # show effective config

`multistream` runs as your normal user and keeps its config, keys and state in
your home directory (`~/.config/multistream/`, `~/.local/state/multistream/`) -
no dedicated account or groups. The binary finds its config in this order:
`-config <file>`, `$MULTISTREAM_CONFIG`, `~/.config/multistream/config.json`,
`/etc/multistream/config.json`, `./config.json`. See the
[project README](https://github.com/xlip/multiStream) for the full setup and
[CONFIG.md](https://github.com/xlip/multiStream/blob/main/CONFIG.md) for the
config reference.
