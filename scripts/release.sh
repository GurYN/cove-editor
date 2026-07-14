#!/usr/bin/env bash
# Local release build: darwin/arm64 + darwin/amd64 (CGo cross-compiles via
# clang -arch on macOS). Linux artifacts come from the CI release workflow —
# tree-sitter's CGo makes cross-compiling them from a Mac not worth the pain.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?usage: release.sh vX.Y.Z}"
LDFLAGS="-s -w -X main.version=${VERSION}"
DIST=dist
rm -rf "$DIST" && mkdir -p "$DIST"

build() {
  local goarch="$1" cc="$2"
  local out="cove_${VERSION}_darwin_${goarch}"
  echo "building $out"
  CGO_ENABLED=1 GOOS=darwin GOARCH="$goarch" CC="$cc" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/cove" ./cmd/cove
  tar -C "$DIST" -czf "$DIST/$out.tar.gz" cove
  rm "$DIST/cove"
}

build arm64 "clang -arch arm64"
build amd64 "clang -arch x86_64"

(cd "$DIST" && shasum -a 256 ./*.tar.gz > checksums.txt)
echo "---"
cat "$DIST/checksums.txt"
