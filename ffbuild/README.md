# ffbuild - runtime bundle build farm

Builds the two binaries multistream bundles for npm users:

- **ffmpeg** - built from source with a *minimal* configure: no encoders, no
  decoders, no filters, no assembly. All the daemon does is RTMP-in,
  `-c copy`, RTMP-out, so the binary is ~2 MB instead of ~120 MB.
- **mediamtx** - built from a pinned commit (pure Go, CGO disabled). Upstream
  ships every target except windows/arm64, so we build all six ourselves for
  uniformity.

The result is packaged per platform as
`multistream-runtime_<os>_<arch>.tar.gz`:

```
<os>_<arch>/
  bin/ffmpeg          (or ffmpeg.exe)
  bin/mediamtx        (or mediamtx.exe)
  VERSIONS            pinned versions + build date
  LICENSE-ffmpeg.txt  LGPL 2.1 (ffmpeg is a separate spawned program)
  LICENSE-mediamtx.txt  MIT
```

and published to a dedicated GitHub release `runtime-v*` (tag chosen in the
dispatch form, or pushed as a `runtime-v*` git tag for a reproducible
default-versions build). The npm postinstall (`npm/install.js`) downloads
only the current platform's bundle from that release; `release.yml` carries
the `RUNTIME_TAG` it references.

## Files

- `build-ffmpeg.sh` - fetches the pinned ffmpeg source (sha256-checked),
  configures the minimal build, enforces a size guard (20 MB) so an
  accidental `--enable` fails loudly.
- `Dockerfile.linux` - alpine (musl) build, one image per arch via buildx.
- `build-mediamtx.sh` - shallow-clones the pinned mediamtx tag, verifies the
  commit, generates the three go:embed files the same way upstream does
  (pinned VERSION + hash-verified downloaders), cross-compiles six targets.
- `smoke-test.sh` - the gate that a configure flag did not drop something the
  remux needs. Starts mediamtx (away segment on path A), starts the minimal
  ffmpeg re-broadcaster in exactly the daemon's command shape, connects a
  publisher, and verifies the output stream is valid h264+aac.
- `LICENSE-ffmpeg.txt`, `LICENSE-mediamtx.txt` - shipped inside every bundle.
- `../test/data/sample.flv`, `../test/data/away.mp4` - fixtures for the smoke
  test (generated, h264/aac, AAC 48 kHz stereo to match the away-file audio
  constraint).

## Bumping a version

1. Run the `ffbuild` workflow (manual dispatch).
2. For ffmpeg: set `ffmpeg_version` + `src_sha256` (sha256 of the
   `ffmpeg-<version>.tar.xz` from ffmpeg.org/releases).
3. For mediamtx: set `mediamtx_version` (tag) + `mediamtx_commit`
   (`git ls-remote https://github.com/bluenviron/mediamtx <tag>`).
4. Bump `runtime_tag` (e.g. `runtime-v2`).
5. When the run is green, update `RUNTIME_TAG` in `.github/workflows/release.yml`
   and cut the next app release.

## Known constraints

- The minimal ffmpeg has no encoders/decoders by design: it cannot probe,
  transcode, or generate test content. The smoke test therefore uses a full
  system ffmpeg (via apt) only to *verify* the output.
- mediamtx's generated config for the managed relay (see the daemon's
  `manage_mediamtx`) must match the bundled mediamtx version's key names
  (e.g. `apiAddress`, `moq`); mediamtx rejects unknown keys.
- macOS builds run on a `macos-latest` (arm64) runner; the x86_64 binary is
  a cross-compile via `CC="clang -arch x86_64"`.
