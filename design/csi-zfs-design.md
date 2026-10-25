# Design: ZFS backend

## Purpose

`pkg/zfs` — wraps the `zfs` CLI; backs volume provisioning.

## Operations

- `CreateVolume(dataset, sizeBytes)` — create parent datasets
  (`mountpoint=none`), then `zfs create -V <size> -o volblocksize=16K <dataset>`;
  wait for `/dev/zvol/<dataset>` to appear.
- `DestroyVolume(dataset)` — destroy zvol; parents left in place.
- `Exists(dataset)`, `VolSize(dataset)`, `DevicePath(dataset)`.
- `WaitForDevice(dataset, timeout)`.
- `FreeBytes()` — pool free capacity for `GetCapacity`.
- `Volumes()` — list datasets for `ListVolumes`.

## Design notes

- 16K `volblocksize`; size passed via `-V` (bytes).
- Idempotent create; missing volume tolerated on destroy.
