#!/usr/bin/env bash
#
# End-to-end test for the NVMe-oF/TCP CSI driver (block mode).
#
# Flow: identity -> create-volume -> refused stage -> controller publish ->
# node stage -> node publish -> dd -> fence, re-stage, verify -> target reboot
# with a fresh stage, verify -> target reboot with a live node, verify -> node unpublish ->
# node unstage -> controller unpublish -> delete-volume -> orphan check.
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
# Must equal the driver's --node-id, which defaults to the hostname.
NODE_ID="${NODE_ID:-$(hostname)}"

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

# Prints the state of the initiator controller connected to $NQN, if any.
ctrl_state() {
  local d
  for d in /sys/class/nvme/nvme*; do
    if [ "$(cat "$d/subsysnqn" 2>/dev/null)" = "$NQN" ]; then
      cat "$d/state"
    fi
  done
}

# Build the driver if the binary is not present.
if [ ! -x "$DRIVER_BIN" ]; then
  log "building driver"
  (cd "$(dirname "$0")/../.." && go build -o "$DRIVER_BIN" ./cmd/nvmeof-csi)
fi

start_driver() {
  log "starting driver"
  setsid "$DRIVER_BIN" --target-addr "$TARGET_ADDR" --port "$TRSVCID" --pool "$POOL" \
    >>/tmp/csi-e2e-driver.log 2>&1 </dev/null &
  for _ in $(seq 1 30); do [ -S "$SOCKET" ] && break; sleep 1; done
  [ -S "$SOCKET" ] || die "driver socket $SOCKET not created"
}

if ! pgrep -x nvmeof-csi >/dev/null 2>&1; then
  STARTED_DAEMON=1
  : >/tmp/csi-e2e-driver.log
  start_driver
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

log "node: stage is refused before controller publish"
if "${CSCV[@]}" node stage --cap "$CAP" --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null 2>&1; then
  die "stage succeeded without controller publish"
fi

log "controller: publish to $NODE_ID"
"${CSCV[@]}" controller publish --cap "$CAP" --node-id "$NODE_ID" "$VOLUME_ID" >/dev/null

log "controller: publish to another node is refused"
if "${CSCV[@]}" controller publish --cap "$CAP" --node-id "other-$NODE_ID" "$VOLUME_ID" >/dev/null 2>&1; then
  die "volume published to two nodes"
fi

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

log "controller: unpublish fences the connected node"
"${CSCV[@]}" controller unpublish --node-id "$NODE_ID" "$VOLUME_ID" >/dev/null
for _ in $(seq 1 15); do [ "$(ctrl_state)" != "live" ] && break; sleep 1; done
[ "$(ctrl_state)" != "live" ] || die "controller still live after unpublish"
# A refused reconnect carries do-not-retry: newer kernels remove the controller,
# older ones keep retrying until ctrl_loss_tmo. Either way it must not be live.
for _ in $(seq 1 20); do [ -z "$(ctrl_state)" ] && break; sleep 1; done
[ "$(ctrl_state)" != "live" ] || die "fenced node reconnected"
log "controller after fence: $(ctrl_state | grep . || echo removed)"

log "node: a fenced node returns by staging again"
"${CSCV[@]}" node unpublish --target-path "$TARGET" "$VOLUME_ID" >/dev/null
"${CSCV[@]}" node unstage --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
"${CSCV[@]}" controller publish --cap "$CAP" --node-id "$NODE_ID" "$VOLUME_ID" >/dev/null
"${CSCV[@]}" node stage --cap "$CAP" --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
"${CSCV[@]}" node publish --cap "$CAP" --target-path "$TARGET" --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
dd if="$TARGET" of="$WORK/copy" bs=1M count=64 status=none
cmp "$WORK/orig" "$WORK/copy" || die "data mismatch after fence"

# A reboot empties configfs; do that to this volume with the driver stopped. It
# needs this script's own driver, and a port with no other volumes to close.
PORT_SUBS=/sys/kernel/config/nvmet/ports/1/subsystems
if [ "$STARTED_DAEMON" = 1 ] && [ "$(ls "$PORT_SUBS" | wc -l)" = 1 ]; then
  stop_and_wipe() {
    pkill -x nvmeof-csi
    for _ in $(seq 1 10); do pgrep -x nvmeof-csi >/dev/null || break; sleep 1; done
    rm -f "$SOCKET"
    SUB="/sys/kernel/config/nvmet/subsystems/$NQN"
    rm -f "$PORT_SUBS/$NQN" "$SUB/allowed_hosts/"*
    echo 0 >"$SUB/namespaces/1/enable"
    rmdir "$SUB/namespaces/1" "$SUB"
  }

  log "target reboot, same node: worker restarts too and stages from scratch"
  "${CSCV[@]}" node unpublish --target-path "$TARGET" "$VOLUME_ID" >/dev/null
  "${CSCV[@]}" node unstage --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
  stop_and_wipe
  start_driver
  # No controller publish: the attachment outlives the reboot, so the grant must too.
  "${CSCV[@]}" node stage --cap "$CAP" --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
  "${CSCV[@]}" node publish --cap "$CAP" --target-path "$TARGET" --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
  dd if="$TARGET" of="$WORK/copy" bs=1M count=64 status=none
  cmp "$WORK/orig" "$WORK/copy" || die "data mismatch after target reboot"

  log "target reboot, separate NAS: running worker reconnects"
  stop_and_wipe
  sleep 12 # longer than the initiator's 10s reconnect delay
  [ -n "$(ctrl_state)" ] || die "controller removed during target outage"
  start_driver
  for _ in $(seq 1 30); do [ "$(ctrl_state)" = "live" ] && break; sleep 1; done
  [ "$(ctrl_state)" = "live" ] || die "controller not live after target reboot"
  dd if="$TARGET" of="$WORK/copy" bs=1M count=64 status=none
  cmp "$WORK/orig" "$WORK/copy" || die "data mismatch after target reboot"
else
  log "target reboot: skipped, needs its own driver and an otherwise empty port"
fi

log "node: unpublish"
"${CSCV[@]}" node unpublish --target-path "$TARGET" "$VOLUME_ID" >/dev/null
[ -e "$TARGET" ] && ! mountpoint -q "$TARGET" || die "target still mounted"

log "node: unstage"
"${CSCV[@]}" node unstage --staging-target-path "$STAGING" "$VOLUME_ID" >/dev/null
nvme list-subsys -o json | grep -q "$NQN" && die "controller for $NQN still connected"

log "controller: unpublish"
"${CSCV[@]}" controller unpublish --node-id "$NODE_ID" "$VOLUME_ID" >/dev/null

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
