# Design: NVMe-oF/TCP initiator connection (pkg/node)

## Purpose

`pkg/node` — attaches/detaches the initiator to an NVMe-oF/TCP subsystem via
the `nvme` CLI.

## Operations

- `Connect(ctx, nqn, traddr, trsvcid)` — `nvme connect -t tcp -n <nqn> -a <addr>
  [-s <trsvcid>]`; idempotent; waits for the controller device.
- `Disconnect(nqn)` — `nvme disconnect -n <nqn>`; idempotent.
- `FindDeviceByNQN(nqn)` — maps NQN → `/dev/nvmeXnY` via `nvme list-subsys` /
- `CapMaxSectors(dev)` — caps the device's `max_sectors_kb` to 1024 (1 MB =
  one SGL segment, 256 × 4K descriptors). The target advertises `mdts: 0`
  (unlimited), so the initiator would otherwise build multi-MB chained SGLs
  that the target rejects ("SGL Invalid Data Offset", DNR) — breaking mkfs,
  fsync and large buffered writes. Called from `NodeStageVolume` for both
  block and filesystem modes; idempotent; silently skips devices without a
  sysfs entry.
  `nvme list`.
- `WaitForDevice(ctx, nqn)` — polls up to 10s for the device.

## Design notes

- Connection target derived from the subsystem NQN and configured
  target address/port.
