# Design: CSI node service

## Purpose

Implements staging & publishing for the NVMe-oF/TCP block driver.

## RPCs

- `NodeGetInfo` — returns configured `NodeID`, sourced from the `--node-id`
  flag (K8s node name) with an `os.Hostname()` fallback.
- `NodeGetCapabilities` — advertises `STAGE_UNSTAGE_VOLUME`; publish/unpublish
  are implemented but not advertised.
- `NodeStageVolume` — validates capability, connects initiator via NVMe-oF/TCP.
- `NodeUnstageVolume` — disconnects.
- `NodePublishVolume` — bind-mounts `/dev/nvmeXnY` onto target path (block:
  file target, not directory).
- `NodeUnpublishVolume` — unmounts target.

## Publish semantics

- Target path required; must be a file for block volumes (directory rejected).
- Creates target file if absent.
- Idempotent: skips mount if already mounted.
- Bind mount via `unix.Mount(dev, target, "", unix.MS_BIND, "")`.

## Unpublish semantics

- Unmount; tolerate `EINVAL` / `ENOENT` (not mounted).

## Capability note

Publish/unpublish implemented but not advertised in `NodeGetCapabilities` —
capability advertisement should be kept in sync with implementation.
