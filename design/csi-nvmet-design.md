# Design: NVMe-oF/TCP backend (kernel nvmet)

## Purpose

`pkg/nvmet` — talks to the kernel NVMe-oF/TCP target via configfs.

## Components

- `Target{root}` — configfs root (default `/sys/kernel/config/nvmet`).
- `Available()` — whether nvmet configfs tree exists.
- `EnsurePort(traddr, trsvcid)` — create/refresh port `1` (`tcp`, IPv4).
- Subsystem / namespace / port lifecycle:
  - `CreateSubsystem(nqn)`
  - `CreateNamespace(nqn, nsid, devicePath)`
  - `LinkSubsystemToPort(nqn, trsvcid)`
  - `ListSubsystems()`
  - `TeardownVolume(nqn)`

## Design notes

- In-memory configfs: idempotent; refuses to rewrite an active port's attrs.
- Teardown is what backs volume deletion.
