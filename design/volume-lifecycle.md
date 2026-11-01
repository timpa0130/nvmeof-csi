# Design: volume lifecycle

## Purpose

How the code flows when a volume is created, attached to a pod, and removed:
which RPC runs where, what it touches on the target host and on the node, and
what makes each step safe to retry. Components and naming are in
`architecture.md`.

## Overview

```mermaid
flowchart LR
    PVC["PVC created"] --> CREATE["Create volume"]
    CREATE --> GRANT["Grant node"]
    GRANT --> STAGE["Stage on node"]
    STAGE --> PUBLISH["Publish to pod"]
    PUBLISH --> UNPUB["Unpublish"]
    UNPUB --> UNSTAGE["Unstage"]
    UNSTAGE --> REVOKE["Revoke node"]
    REVOKE --> DELETE["Delete volume"]
```

| Box | RPC | Runs on | Touches |
| --- | --- | --- | --- |
| Create volume | `CreateVolume` | controller | `zfs create`, nvmet subsystem / namespace / port link |
| Grant node | `ControllerPublishVolume` | controller | nvmet `allowed_hosts` link for the node's host NQN |
| Stage on node | `NodeStageVolume` | node | `nvme connect`, sysfs I/O cap, `mkfs` + mount (filesystem mode) |
| Publish to pod | `NodePublishVolume` | node | bind mount into the pod's target path |
| Unpublish | `NodeUnpublishVolume` | node | unmount target |
| Unstage | `NodeUnstageVolume` | node | unmount staging (filesystem mode), `nvme disconnect` |
| Revoke node | `ControllerUnpublishVolume` | controller | drop connections, remove the `allowed_hosts` link |
| Delete volume | `DeleteVolume` | controller | nvmet teardown, `zfs destroy` |

## 1. Create

The external-provisioner calls `CreateVolume` with the PVC's namespace/name in
the parameters. The controller derives every name and builds the export
bottom-up: zvol, then subsystem, then namespace, then port link.

```mermaid
sequenceDiagram
    participant EP as Provisioner
    participant C as Controller
    participant Z as ZFS
    participant T as nvmet
    EP->>C: CreateVolume
    C->>C: validate, derive names
    C->>Z: zvol exists?
    alt exists, same size
        C-->>EP: existing volume
    else new
        C->>Z: create zvol
        C->>T: subsystem, namespace, port link
        C-->>EP: volume ID
    end
```

| Step | Detail |
| --- | --- |
| validate | `validateCapabilities`; namespace/name from `csi.storage.k8s.io/pvc/namespace` / `pvc/name`, else `req.GetName()`; `ValidNamespace`/`ValidName`; `MaxNQN`, `MaxDataset` → `InvalidArgument` |
| derive names | volume ID, NQN, dataset (`pkg/ids`) |
| zvol exists? | same `volsize` → return it (idempotent); different size → `AlreadyExists` |
| create zvol | parents with `mountpoint=none`, then `zfs create -V <bytes> -o volblocksize=16K <dataset>`; waits for `/dev/zvol/<dataset>` (udev) |
| subsystem, namespace, port link | `CreateSubsystem(nqn)` with `attr_allow_any_host=0`, so no host can connect yet; `CreateNamespace(nqn, 1, /dev/zvol/...)`, `LinkSubsystemToPort(nqn)` on port `1` |
| on any failure | `TeardownVolume(nqn)` + `DestroyVolume(dataset)`, then `Internal` — no half-built volume is left |

## 2. Grant, stage and publish

A pod scheduled to a node creates a `VolumeAttachment`, and kubelet waits for it
before staging. The grant is what lets the node connect. kubelet stages once
per node, then publishes per pod. The node never constructs device names: it
connects by NQN and finds the namespace's block devices in sysfs.

```mermaid
sequenceDiagram
    participant A as Attacher
    participant C as Controller
    participant K as kubelet
    participant N as Node
    participant T as nvmet
    A->>C: ControllerPublishVolume
    C->>T: grant host
    C-->>A: attached
    K->>N: NodeStageVolume
    N->>T: nvme connect
    N->>N: find namespace, cap I/O
    opt filesystem mode
        N->>N: mkfs if blank, mount
    end
    N-->>K: staged
    K->>N: NodePublishVolume
    N->>N: bind mount
    N-->>K: published
```

| Step | Detail |
| --- | --- |
| grant host | `GrantHost(nqn, HostNQN(nodeID))`: `attr_allow_any_host=0`, `hosts/<host>`, `allowed_hosts/<host>` link; same node → success; other node → `FailedPrecondition`; no zvol → `NotFound` |
| nvme connect | `nvme connect -t tcp -n <nqn> -a <target-addr> -s <port> --hostnqn <host> --hostid <id>`; no-op when the namespace already exists; waits up to 10s for it; fails if the node has no grant |
| find namespace | `LookupNamespace(nqn)`: every `/sys/block/*` whose `device/subsysnqn` equals the NQN. Head = the entry without a `c` segment (`/dev/nvme1n1`); paths = `nvme<s>c<c>n1` |
| cap I/O | `Namespace.CapIO()` lowers `queue/max_sectors_kb` to `1024` on head **and** paths; runs on every stage (and block publish) before any early return |
| mkfs if blank, mount | `blkid` probe on the head; `mkfs.<fsType>` (default `ext4`) only when no filesystem; mount head → staging path |
| bind mount (block) | target must be a file (created if absent); bind `/dev/nvmeXnY` → target |
| bind mount (filesystem) | target dir created if absent; bind staging → target |
| already mounted | skipped — stage and publish are retry-safe |

Why the I/O cap: the target advertises `mdts: 0` and its kernel rejects
commands > 4 MiB (Data SGL Length Invalid, DNR). Path queues re-merge the
head's bios up to their own limit, so every device of the namespace is capped.
Full analysis: `incident-nvmet-tcp-4mib-write-rejection.md`.

## 3. Teardown

Reverse order of the above. Every step treats "already gone" as success, so
kubelet and the sidecars can retry freely.

```mermaid
sequenceDiagram
    participant K as kubelet
    participant N as Node
    participant A as Attacher
    participant EP as Provisioner
    participant C as Controller
    K->>N: NodeUnpublishVolume
    N->>N: unmount target
    K->>N: NodeUnstageVolume
    N->>N: unmount staging, disconnect
    A->>C: ControllerUnpublishVolume
    C->>C: drop connections, revoke host
    EP->>C: DeleteVolume
    C->>C: nvmet teardown, destroy zvol
```

| Step | Detail |
| --- | --- |
| unmount target | tolerates `EINVAL` / `ENOENT` (not mounted) |
| unmount staging, disconnect | unmount only when mounted (filesystem mode); `nvme disconnect -n <nqn>`; namespace gone → success |
| drop connections, revoke host | `RevokeHost(nqn, host)`: only if `host` holds it, unlink the port (deletes its controllers) and remove the `allowed_hosts` link; then always restore the port link. Other subsystems are untouched. No subsystem → success |
| nvmet teardown | `TeardownVolume(nqn)`: unlink port, remove `allowed_hosts` links, remove namespace, remove subsystem |
| destroy zvol | `zfs destroy <dataset>`; parent datasets left in place; missing volume → success |

## Moving between nodes

Only one node can hold a volume. The new node's attach is refused until the old
one is detached, so its pod waits in `ContainerCreating` with a multi-attach
event instead of mounting the volume twice.

```mermaid
sequenceDiagram
    participant O as Old node
    participant K as Kubernetes
    participant C as Controller
    participant N as New node
    K->>O: delete pod
    O->>O: unpublish, unstage
    O-->>K: volume not in use
    K->>C: revoke old node
    K->>C: grant new node
    K->>N: stage, publish
```

| Case | When the old node is revoked |
| --- | --- |
| Drain, either workload type | once the old node reports the volume no longer in use |
| Dead node | after the pod is gone (force delete, or out-of-service taint) and Kubernetes force-detaches; default wait 6 minutes |
| Running but partitioned node | as for a dead node; the revoke drops its connection, so its I/O stops reaching the target |

The revoke is the fence: a node cut off from the API server cannot be asked to
unmount, but the target stops serving it regardless. Its initiator retries and
is refused. Unflushed writes on a dead or fenced node are lost; the new node
replays the ext4 journal.

## Recovery: reconcile on startup

A controller crash between steps can leave a subsystem without its zvol.
On every controller start, `Reconcile()` lists nvmet subsystems with our NQN
root and tears down any whose dataset no longer exists. Live exports are
untouched, so initiators keep their I/O across a controller restart.

## Other controller RPCs

| RPC | Behavior |
| --- | --- |
| `ValidateVolumeCapabilities` | zvol must exist (`NotFound` otherwise), then capability check |
| `ListVolumes` | `zfs list -t volume`, with `volsize` per dataset |
| `GetCapacity` | `zpool list -o free <pool>` |

## Manual end-to-end check

`test/manual/e2e.sh` walks this whole lifecycle without Kubernetes, as root,
using `csc` against the driver socket: probe → create → stage refused → grant →
second node refused → stage → publish → `dd` write/verify on the device → revoke
drops the connection → re-grant reconnects → unpublish → unstage → revoke →
delete → orphan check.
