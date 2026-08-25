#!/bin/bash
# Build v0.1.1 .so on CT101 (with kickPoller fix), copy back + package + release
set -e
SRC="/d/Python/projects/CPA Plugin/cpa-plugin-vast-cluster-bench"
TARGET_HOST=10.21.1.1
KEY="$HOME/.codex/secrets/ssh/10.21.1.1_ed25519"
VERSION=0.1.8

echo "=== 1. stream source into CT101 ==="
ssh -o BatchMode=yes -i "$KEY" root@$TARGET_HOST "pct exec 101 -- rm -rf /tmp/vcb-src; pct exec 101 -- mkdir -p /tmp/vcb-src/src"
for f in go.mod go.sum; do
  [ -f "$SRC/$f" ] && cat "$SRC/$f" | ssh -o BatchMode=yes -i "$KEY" root@$TARGET_HOST "pct exec 101 -- sh -c 'cat > /tmp/vcb-src/$f'"
done
for f in "$SRC"/src/*.go; do
  cat "$f" | ssh -o BatchMode=yes -i "$KEY" root@$TARGET_HOST "pct exec 101 -- sh -c 'cat > /tmp/vcb-src/src/$(basename "$f")'"
done

echo "=== 2. build v$VERSION ==="
ssh -o BatchMode=yes -i "$KEY" root@$TARGET_HOST "
pct exec 101 -- docker run --rm -v /tmp/vcb-src:/build -w /build golang:1.26-bookworm bash -c '
  export GOFLAGS=-mod=mod
  CGO_ENABLED=1 go build -buildvcs=false -trimpath -buildmode=c-shared -ldflags \"-s -w -X main.pluginVersion=$VERSION\" -o /build/vast-cluster-bench-v$VERSION.so ./src
  ls -la /build/vast-cluster-bench-v$VERSION.so
'"

echo "=== 3. copy .so back ==="
mkdir -p "$SRC/dist"
ssh -o BatchMode=yes -i "$KEY" root@$TARGET_HOST "pct exec 101 -- cat /tmp/vcb-src/vast-cluster-bench-v$VERSION.so" > "$SRC/dist/vast-cluster-bench-v$VERSION.so"
ls -la "$SRC/dist/"

echo "=== 4. package zip ==="
cd "$SRC" && python .github/scripts/package_local.py && ls -la dist/