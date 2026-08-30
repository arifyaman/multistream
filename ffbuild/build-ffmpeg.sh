#!/usr/bin/env bash
# Build a minimal "remux-only" ffmpeg: no encoders, no decoders, no filters,
# no assembly - just enough for RTMP-in -> -c copy -> RTMP-out, which is all
# the multistream daemon does. The result is ~2 MB instead of ~120 MB.
#
# The smoke test (ffbuild/smoke-test.sh) is the gate that a configure flag
# did not drop something the remux needs.
#
# Env inputs:
#   FFMPEG_VERSION  default 9.0
#   SRC_SHA256      sha256 of ffmpeg-<version>.tar.xz (required)
#   SRCDIR          default /src
#   OUTDIR          default /out
#   OUTNAME         default ffmpeg (add .exe for windows)
#   TARGET_OS       "" | windows | macosx
#   CC              optional compiler override (e.g. "clang -arch x86_64")
#   CROSS_PREFIX    optional, for cross builds (e.g. x86_64-w64-mingw32-)
#   FFMPEG_ARCH     optional, ffmpeg arch name for cross builds
#   MAX_KB          size guard, default 20000 (20 MB)
set -euo pipefail
# Trace the last commands to stderr so a failure log shows where it died.
set -x

VERSION="${FFMPEG_VERSION:-9.0}"
: "${SRC_SHA256:?SRC_SHA256 of ffmpeg-${VERSION}.tar.xz is required}"
SRCDIR="${SRCDIR:-/src}"
OUTDIR="${OUTDIR:-/out}"
OUTNAME="${OUTNAME:-ffmpeg}"
MAX_KB="${MAX_KB:-20000}"
SRC="${SRCDIR}/ffmpeg-${VERSION}"

# sha256_of works on Linux (sha256sum, coreutils or busybox) and macOS (shasum).
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

mkdir -p "$SRCDIR"
cd "$SRCDIR"
if [ ! -f "ffmpeg-${VERSION}.tar.xz" ]; then
  # curl is absent on alpine (busybox provides wget); wget is absent on the
  # macOS runner - use whichever exists.
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "ffmpeg-${VERSION}.tar.xz" \
      "https://ffmpeg.org/releases/ffmpeg-${VERSION}.tar.xz"
  else
    wget -q -O "ffmpeg-${VERSION}.tar.xz" \
      "https://ffmpeg.org/releases/ffmpeg-${VERSION}.tar.xz"
  fi
fi
actual="$(sha256_of "ffmpeg-${VERSION}.tar.xz")"
if [ "$actual" != "$SRC_SHA256" ]; then
  echo "ffmpeg source sha256 mismatch: got ${actual}, want ${SRC_SHA256}" >&2
  exit 1
fi
if [ ! -d "$SRC" ]; then
  tar -xf "ffmpeg-${VERSION}.tar.xz"
fi

cd "$SRC"

flags=(
  --prefix=/usr/local
  --disable-everything
  --disable-asm
  --disable-autodetect
  --disable-doc
  --disable-avdevice
  --disable-swscale
  --disable-swresample
  --enable-protocol=file
  --enable-protocol=tcp
  --enable-protocol=rtmp
  --enable-demuxer=flv
  --enable-muxer=flv
  --enable-parser=h264
  --enable-parser=aac
)

case "${TARGET_OS:-}" in
  "")
    # native linux (musl in Docker): static by nature
    flags+=(--extra-libs="-lpthread -lm")
    ;;
  windows)
    [ -n "${CROSS_PREFIX:-}" ] || { echo "windows build needs CROSS_PREFIX" >&2; exit 1; }
    [ -n "${FFMPEG_ARCH:-}" ] || { echo "windows build needs FFMPEG_ARCH" >&2; exit 1; }
    flags+=(
      --target-os=windows
      --enable-cross-compile
      --cross-prefix="${CROSS_PREFIX}"
      --arch="${FFMPEG_ARCH}"
      --extra-libs="-lws2_32 -lwinmm"
    )
    ;;
  macosx)
    flags+=(--target-os=macosx)
    ;;
  *)
    echo "unknown TARGET_OS: ${TARGET_OS}" >&2
    exit 1
    ;;
esac

# Parallelism: nproc (Linux), sysctl hw.ncpu (macOS), else a sane default.
JOBS="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2)"

./configure "${flags[@]}"
make -j"$JOBS"

# The built binary is always named "ffmpeg"; OUTNAME may add .exe.
mkdir -p "$OUTDIR"
cp ffmpeg "$OUTDIR/$OUTNAME"
chmod +x "$OUTDIR/$OUTNAME"

size_kb=$(( $(stat -c%s "$OUTDIR/$OUTNAME" 2>/dev/null || stat -f%z "$OUTDIR/$OUTNAME") / 1024 ))
echo "built $OUTDIR/$OUTNAME (${size_kb} KB)"
if [ "$size_kb" -gt "$MAX_KB" ]; then
  echo "ffmpeg binary is ${size_kb} KB, above the ${MAX_KB} KB guard - something extra got enabled" >&2
  exit 1
fi
