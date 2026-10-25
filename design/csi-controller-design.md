# Design: CSI controller service

## Purpose

Implements provisioning (`CreateVolume` / `DeleteVolume`) and capacity/listing
RPCs for the NVMe-oF/TCP block driver.

## CreateVolume

Flow:

1. `validateCapabilities(req.GetVolumeCapabilities())`
2. Read `csi.storage.k8s.io/pvc/namespace` / `csi.storage.k8s.io/pvc/name`
   from parameters; fall back to `req.GetName()`.
3. Validate namespace/name (no `/`, namespace no `.`).
4. Derive `volumeID`, `nqn`, `dataset`; enforce `MaxNQN` / `MaxDataset`.
5. Idempotency: existing zvol with matching size → return it.
6. Provision: create zvol → create subsystem → create namespace → link to port.
7. On failure: teardown half-provisioned state.

## DeleteVolume

Tear down subsystem + destroy zvol; missing volumes reported as success.

## Other RPCs

- `ControllerPublishVolume` / `ControllerUnpublishVolume` — **no-ops** in this
  driver; real publish happens in the node server.
- `ValidateVolumeCapabilities` — checks zvol exists + capabilities.
- `ListVolumes` / `GetCapacity` / `ControllerGetCapabilities` (only
  `CREATE_DELETE_VOLUME` advertised).

## Design decisions

- Single-namespace model: every subsystem gets exactly one namespace (nsid 1).
- Everything derived deterministically from `namespace/name`.
