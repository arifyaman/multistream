# @arifyaman/multistream

npm wrapper for the [multistream](https://github.com/xlip/multiStream) CLI:
a terminal dashboard for the OBS -> mediamtx -> platforms RTMP re-broadcast
chain. It shows the live status of every link and - via its daemon - runs and
supervises the per-platform ffmpeg re-broadcasters.

## Install

    npm install -g @arifyaman/multistream

The postinstall step downloads the prebuilt binary for your platform from
the GitHub release and verifies its SHA-256 checksum before installing it.

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
