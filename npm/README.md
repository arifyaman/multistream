# @arifyaman/multistream

npm wrapper for the [multistream](https://github.com/xlip/multiStream) CLI:
a terminal status monitor for the OBS -> mediamtx -> platforms RTMP
re-broadcast chain.

## Install

    npm install -g @arifyaman/multistream

The postinstall step downloads the prebuilt binary for your platform from
the GitHub release and verifies its SHA-256 checksum before installing it.

## Usage

    multistream status --watch     # live table, events on change
    multistream check              # probe API, units, endpoints, key files
    multistream restart kick       # restart one re-broadcaster
    multistream config             # show effective config

The binary reads its config from `$MULTISTREAM_CONFIG`,
`/etc/multistream/config.json` or `./config.json`. See the
[project README](https://github.com/xlip/multiStream) for the config format
and the VPS deployment guide.
