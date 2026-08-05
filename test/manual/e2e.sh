#!/usr/bin/env bash
#
# End-to-end test for the NVMe-oF/TCP CSI driver (block mode).
#
# Flow: identity -> create-volume -> node stage -> node publish -> dd ->
# node unpublish -> node unstage -> delete-volume -> orphan check.
#
# Run as root (or with a sudo-capable user). All CSI RPCs go through csc.
set -euo pipefail

DRIVER_BIN="${DRIVER_BIN:-$(dirname "$0")/../../bin/nvmeof-csi}"
if [ -z "${CSC:-}" ]; then
  for c in "$(command -v csc 2>/dev/null)" "$(go env GOPATH 2>/dev/null)/bin/csc" "/home/timlindholm/go/bin/csc"; do
    if [ -n "$c" ] && [ -x "$c" ]; then CSC="$c"; break; fi
  done
  CSC="${CSC:-csc}"
fi
ENDPOINT="unix://${CSI_SOCKET:-/run/csi/nvmeof-csi.sock}"
SOCKET="${CSI_SOCKET:-/run/csi/nvmeof-csi.sock}"
TARGET_ADDR="${TARGET_ADDR:-192.168.1.86}"
TRSVCID="${TRSVCID:-4420}"
POOL="${POOL:-nvme}"

NS="default"
VOL="e2e-$(date +%s)"
VOLUME_ID="${NS}/${VOL}"
NQN="nqn.2026-08.io.example.nvmeof:${NS}.${VOL}"
DATASET="${POOL}/${VOLUME_ID}"
STAGING="/tmp/csi-e2e-staging"
TARGET="/tmp/csi-e2e-device"
WORK=/tmp/csi-e2e-work

STARTED_DAEMON=0

log() { echo "--- $*"; }
die() { echo "FAIL: $*" >&2; exit 1; }

# Build the driver if the binary is not present.
if [ ! -x "$DRIVER_BIN" ]; then
  log "building driver"
  (cd "$(dirname "$0")/../.." && go build -o "$DRIVER_BIN" ./cmd/nvmeof-csi)
fi

if ! pgrep -x nvmeof-csi >/dev/null 2>&1; then
  log "starting driver"
  STARTED_DAEMON=1
  setsid "$DRIVER_BIN" --target-addr "$TARGET_ADDR" --port "$TRSVCID" --pool "$POOL" \
    >/tmp/csi-e2e-driver.log 2>&1 </dev/null &
  for _ in $(seq 1 30); do [ -S "$SOCKET" ] && break; sleep 1; done
  [ -S "$SOCKET" ] || die "driver socket $SOCKET not created"
fi

CSCV=("$CSC" "--endpoint=$ENDPOINT")
CAP="SINGLE_NODE_WRITER,block"
PARAMS="csi.storage.k8s.io/pvc/namespace=${NS},csi.storage.k8s.io/pvc/name=${VOL}"

cleanup() {
  if [ "$STARTED_DAEMON" = 1 ]; then
    log "stopping driver"
    pkill -x nvmeof-csi || true
  fi
}
trap cleanup EXIT

rm -rf "$WORK" "$STAGING" "$TARGET"; mkdir -p "$WORK"

log "identity: probe"
"${CSCV[@]}" identity probe >/dev/null || die "probe failed"

log "controller: create-volume $VOLUME_ID"
"${CSCV[@]}" controller create-volume --cap "$CAP" --req-bytes 67108864 --params "$PARAMS" "$VOL" >/dev/null

log "node: stage"
"${CSCV[@]}" node stage --cap "$CAP" --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null

log "node: publish"
"${CSCV[@]}" node publish --cap "$CAP" --target-path "$TARGET" --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null

log "verify block device at $TARGET"
[ -b "$TARGET" ] || die "target is not a block device: $(ls -l "$TARGET")"

log "dd write + read + cmp (64MiB)"
dd if=/dev/urandom of="$WORK/orig" bs=1M count=64 status=none
dd if="$WORK/orig" of="$TARGET" bs=1M count=64 status=none
dd if="$TARGET" of="$WORK/copy" bs=1M count=64 status=none
cmp "$WORK/orig" "$WORK/copy" || die "data mismatch"

log "node: unpublish"
"${CSCV[@]}" node unpublish --target-path "$TARGET" "$VOLUME_ID" >/dev/null
[ -e "$TARGET" ] && ! mountpoint -q "$TARGET" || die "target still mounted"

log "node: unstage"
"${CSCV[@]}" node unstage --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
nvme list-subsys -o json | grep -q "$NQN" && die "controller for $NQN still connected"

log "controller: delete-volume"
"${CSCV[@]}" controller delete-volume "$VOLUME_ID" >/dev/null

log "orphan check"
if find /sys/kernel/config/nvmet/subsystems -mindepth 1 -maxdepth 1 -name "*${VOL}*" | grep -q .; then
  die "configfs subsystem for $VOL still present"
fi
if ls /sys/kernel/config/nvmet/ports/*/subsystems/ 2>/dev/null | grep -q "$NQN"; then
  die "port link for $NQN still present"
fi
if zfs list -H -o name 2>/dev/null | grep -q "^${DATASET}$"; then
  die "zvol ${DATASET} still present"
fi

log "PASS"
log "configfs subsystems now: $(find /sys/kernel/config/nvmet/subsystems -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)"
log "connected controllers:"
nvme list -o json
