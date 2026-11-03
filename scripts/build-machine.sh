#!/usr/bin/env bash
# build-machine.sh — cross-compiles the machine helper `devcules`
# serves at /install, so a fresh computer adds itself without having
# Go or the repo: one binary per
# target, a SHA256SUMS the installer checks, and a VERSION stamp.
#
#   scripts/build-machine.sh            # every target below
#   DEVCULES_MACHINE_TARGETS="linux/amd64" scripts/build-machine.sh
#
# Output: machine-dist/<os>-<arch>/devcules[.exe], SHA256SUMS, VERSION.
set -euo pipefail
cd "$(dirname "$0")/.."

TARGETS=${DEVCULES_MACHINE_TARGETS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"}
OUT=${DEVCULES_MACHINE_DIST:-machine-dist}
VERSION=$(git describe --tags --always --dirty 2>/dev/null || date +%Y.%m.%d)

TMP="$OUT.tmp.$$"
trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP"; mkdir -p "$TMP"
for t in $TARGETS; do
  os=${t%/*}; arch=${t#*/}
  exe=devcules; [ "$os" = windows ] && exe=devcules.exe
  mkdir -p "$TMP/$os-$arch"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "$TMP/$os-$arch/$exe" ./cmd/devcules
  echo "  built $os-$arch"
done
( cd "$TMP" && if command -v sha256sum >/dev/null 2>&1; then sha256sum */devcules*; else shasum -a 256 */devcules*; fi ) > "$TMP/SHA256SUMS"
echo "$VERSION" > "$TMP/VERSION"
# Swap in atomically-ish: a download in flight keeps reading the old files.
mkdir -p "$(dirname "$OUT")"
rm -rf "$OUT.old"; [ -d "$OUT" ] && mv "$OUT" "$OUT.old"
mv "$TMP" "$OUT"; trap - EXIT
rm -rf "$OUT.old"
echo "devcules $VERSION → $OUT"
