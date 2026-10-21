#!/usr/bin/env bash
# darwin の arm64 / amd64 向けアーカイブと checksums.txt を dist/（DIST_DIR で上書き可）に出す。公開はしない。
# usage: scripts/release.sh <version>
# exit code: 0 成功 / 1 版が無い・build や tar の失敗
set -euo pipefail

version="${1:?usage: scripts/release.sh <version>}"
dist="${DIST_DIR:-dist}"
mkdir -p "$dist"
archives=(
  "kindle-highlight-sync_${version}_darwin_amd64.tar.gz"
  "kindle-highlight-sync_${version}_darwin_arm64.tar.gz"
)
(cd "$dist" && rm -f checksums.txt "${archives[@]}")

tmp="$(cd "$(mktemp -d)" && pwd -P)"
trap 'rm -rf "$tmp"' EXIT

for arch in arm64 amd64; do
  work="$tmp/$arch"
  mkdir -p "$work"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X main.version=${version}" \
    -o "$work/kindle-highlight-sync" ./cmd/kindle-highlight-sync
  COPYFILE_DISABLE=1 tar -C "$work" -czf "$dist/kindle-highlight-sync_${version}_darwin_${arch}.tar.gz" kindle-highlight-sync
done

cd "$dist"
if command -v sha256sum >/dev/null; then sha256sum "${archives[@]}"; else shasum -a 256 "${archives[@]}"; fi \
  > checksums.txt
