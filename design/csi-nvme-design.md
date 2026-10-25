# Design: NVMe-oF/TCP initiator connection (pkg/node)

## Purpose

`pkg/node` — attaches/detaches the initiator to an NVMe-oF/TCP subsystem via
the `nvme` CLI.

## Operations

- `Connect(ctx, nqn, traddr, trsvcid)` — `nvme connect -t tcp -n <nqn> -a <addr>
  [-s <trsvcid>]`; idempotent; waits for the controller device.
- `Disconnect(nqn)` — `nvme disconnect -n <nqn>`; idempotent.
- `FindDeviceByNQN(nqn)` — maps NQN → `/dev/nvmeXnY` via `nvme list-subsys` /
  `nvme list`.
- `WaitForDevice(ctx, nqn)` — polls up to 10s for the device.

## Design notes

- Connection target derived from the subsystem NQN and configured
  target address/port.
