# Design: IDs and naming (pkg/ids)

## Purpose

Deterministic naming between Kubernetes volume IDs and backend resources.

## Concepts

- `VolumeID` = `<namespace>/<name>` (e.g. `default/vol-a`).
- `NQN` = NVMe Qualified Name, e.g. `nqn.2026-08.io.example.nvmeof:<ns>.<name>`.
- `Dataset` = ZFS dataset, e.g. `<pool>/<ns>/<name>`.

## Operations

- `ParseNQN(nqn)` / `SplitVolumeID(volumeID)` — reverse lookup.
- `ValidNamespace(namespace)` / `ValidName(name)` — format validation.
- `MaxNQN` / `MaxDataset` — length limits.

## Design notes

- Naming is deterministic → idempotent provisioning and stable teardown.
