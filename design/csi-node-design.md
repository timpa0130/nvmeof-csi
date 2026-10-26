# Design: CSI node service

## Purpose

Implements staging & publishing for the NVMe-oF/TCP driver, in block or
filesystem mode.

## RPCs

- `NodeGetInfo` — returns configured `NodeID`, sourced from the `--node-id`
  flag (K8s node name) with an `os.Hostname()` fallback.
- `NodeGetCapabilities` — advertises `STAGE_UNSTAGE_VOLUME`; publish/unpublish
  are implemented but not advertised.
- `NodeStageVolume` — validates capability, connects initiator via NVMe-oF/TCP,
  looks up the namespace and caps its I/O size (every stage, before the
  already-mounted early return, so a re-stage re-asserts the cap).
  - Block mode: connection is the staging (no filesystem work).
  - Filesystem mode: uses the namespace head device, creates the requested filesystem
    (`fsType`, default `ext4`) only when the device has none (blkid probe),
    then mounts the device at the staging path.
- `NodeUnstageVolume` — unmounts the staging path when mounted (filesystem
  mode), then disconnects.
- `NodePublishVolume` —
  - Block mode: bind-mounts `/dev/nvmeXnY` onto target path (file target, not
    directory).
  - Filesystem mode: bind-mounts the staged filesystem directory onto the
    target directory.
- `NodeUnpublishVolume` — unmounts target.

## Ownership

The node service never applies uid/gid to staged/published volumes — the
request cannot know the consuming workload's identity. Ownership comes from
the pod (`securityContext.fsGroup`) applied by kubelet under the CSIDriver's
`fsGroupPolicy: File`. Driver-side chown would be redundant (and would fight
kubelet) under this policy.

## Stage flow

```mermaid
sequenceDiagram
    participant EP as kubelet
    participant N as node service
    EP->>N: NodeStageVolume
    N->>N: nvme connect (initiator)
    N->>N: look up namespace (head + paths)
    N->>N: cap I/O at 1 MB on every namespace device
    alt block mode
        N-->>EP: staged (connection only)
    else filesystem mode
        N->>N: blkid probe head; mkfs if no fs
        N->>N: mount dev -> staging path
        N-->>EP: staged
    end
    EP->>N: NodePublishVolume
    alt block mode
        N->>N: bind mount dev -> target file
    else filesystem mode
        N->>N: bind mount staging -> target dir
    end
    N-->>EP: published
```

## Publish semantics

- Target path required.
- Block mode: target must be a file; created if absent; skipped when already
  mounted.
- Filesystem mode: target is a directory (created if absent); staging path
  required; skipped when already mounted.
- Bind mount via `unix.Mount(src, target, "", unix.MS_BIND, "")`.

## Unpublish semantics

- Unmount; tolerate `EINVAL` / `ENOENT` (not mounted).

## Capability note

Publish/unpublish implemented but not advertised in `NodeGetCapabilities` —
capability advertisement should be kept in sync with implementation.
