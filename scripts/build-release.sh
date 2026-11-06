#!/bin/sh
# Build the release files into ./dist: one archive per platform plus checksums.txt.
# Usage: sh scripts/build-release.sh <version>      (e.g. 0.1.0; a leading "v" is removed)
# Needs: go, tar, zip (or python3), sha256sum (or shasum). Used by .github/workflows/release-binaries.yml;
# run it yourself to see exactly what a release would contain.
set -eu

version=${1:?usage: build-release.sh <version>}
version=${version#v}
root=$(cd "$(dirname "$0")/.." && pwd)
dist=$root/dist
rm -rf "$dist"
mkdir -p "$dist"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os=${target%/*}
  arch=${target#*/}
  exe=shrink-your-shit
  [ "$os" = windows ] && exe=shrink-your-shit.exe
  name=shrink-your-shit_${os}_${arch}
  mkdir -p "$stage/$name"
  (cd "$root" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$stage/$name/$exe" .)
  cp "$root/LICENSE" "$root/README.md" "$stage/$name/"
  if [ "$os" = windows ]; then
    if command -v zip >/dev/null 2>&1; then
      (cd "$stage" && zip -qr "$dist/$name.zip" "$name")
    else
      (cd "$stage" && python3 -m zipfile -c "$dist/$name.zip" "$name")
    fi
  else
    tar -C "$stage" -czf "$dist/$name.tar.gz" "$name"
  fi
done

cd "$dist"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -- * > checksums.txt
else
  shasum -a 256 -- * > checksums.txt
fi
cat checksums.txt
