#!/usr/bin/env bash
# Build mediamtx from a pinned commit for all six platform targets.
# Upstream releases cover every target except windows/arm64, so we build
# everything ourselves - mediamtx is pure Go (CGO disabled), which makes
# this a plain cross-compile.
#
# The three go:embed files that upstream's Makefile generates
# (internal/core/VERSION, hls.min.js, the rpicamera blobs) are produced here
# the same way: a pinned VERSION string plus mediamtx's own hash-verified
# downloaders.
#
# Env inputs:
#   MEDIAMTX_VERSION  default v1.20.1
#   MEDIAMTX_COMMIT   commit the tag must resolve to (required)
#   SRCDIR            default ./src
#   OUTDIR            default ./out
set -euo pipefail

VERSION="${MEDIAMTX_VERSION:-v1.20.1}"
: "${MEDIAMTX_COMMIT:?MEDIAMTX_COMMIT is required}"
SRCDIR="${SRCDIR:-$PWD/src}"
OUTDIR="${OUTDIR:-$PWD/out}"
SRC="$SRCDIR/mediamtx"

rm -rf "$SRC"
git clone --quiet --depth 1 --branch "$VERSION" https://github.com/bluenviron/mediamtx "$SRC"
actual="$(git -C "$SRC" rev-parse HEAD)"
if [ "$actual" != "$MEDIAMTX_COMMIT" ]; then
  echo "mediamtx tag ${VERSION} now resolves to ${actual}, want ${MEDIAMTX_COMMIT}" >&2
  exit 1
fi

cd "$SRC"
# internal/core/VERSION is go:embed'ed; normally written by versiongetter
# from a full git history, which a shallow clone lacks.
printf '%s' "$VERSION" > internal/core/VERSION
# hls.min.js (hls.js, pinned by the downloader) and the rpicamera helper
# blobs (pinned + hash-verified by the downloaders) are go:embed'ed.
(cd internal/servers/hls && go run ./hlsjsdownloader)
(cd internal/staticsources/rpicamera && go run ./mtxrpicamdownloader)

mkdir -p "$OUTDIR"
for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64 windows_arm64; do
  goos="${target%_*}"
  goarch="${target#*_}"
  ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  echo "building mediamtx for ${goos}/${goarch}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w" \
    -o "$OUTDIR/mediamtx-${goos}-${goarch}${ext}" .
done
ls -lh "$OUTDIR"
