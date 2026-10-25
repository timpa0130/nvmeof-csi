# Design: manual end-to-end flow

## Purpose

`test/manual/e2e.sh` — exercises the full NVMe-oF/TCP block flow without
Kubernetes.

## Flow

1. Start driver (`nvmeof-csi --target-addr ... --port ... --pool ...`).
2. `identity probe` → ready.
3. `controller create-volume` → ZFS zvol + NVMe-oF subsystem.
4. `node stage` → connect initiator.
5. `node publish` → bind mount block device.
6. Verify block device at target; `dd` round-trip write/verify.
7. `node unpublish` → `node unstage` → `controller delete-volume`.
8. Orphan check.

## Notes

- Run as root; all RPCs via `csc` CLI against the driver socket.
- Driver auto-reconciles orphan subsystems on startup.
