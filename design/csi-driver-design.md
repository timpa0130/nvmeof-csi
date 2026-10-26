# Design: NVMe-oF/TCP CSI Driver and the External Component Flow

## Purpose

Explain how the `nvmeof-csi` driver works, how a full storage volume lifecycle
flows through it, and where the **external** Kubernetes components fit when the
backing disks are *not* directly attached to the Kubernetes node.

## 1. The CSI driver model

A CSI driver is a gRPC server that speaks the Container Storage Interface over a
Unix socket (default: `/run/csi/<plugin>.sock`).

Three service areas:

- **Identity** – plugin metadata, capabilities, probe.
- **Controller** – provisioning (`CreateVolume` / `DeleteVolume`), and
  attach (`ControllerPublishVolume` / `ControllerUnpublishVolume`).
- **Node** – staging & publishing (`NodeStageVolume`, `NodePublishVolume`,
  `NodeUnstageVolume`, `NodeUnpublishVolume`).

This repo's driver (`cmd/nvmeof-csi`) implements all three behind a single
binary, selectable via `--mode all|controller|node`.

## 2. What `nvmeof-csi` does

- Backend storage: **ZFS** zvol (`zfs create -V <bytes> -o volblocksize=16K`).
- NVMe-oF/TCP transport: **kernel nvmet** (configfs) + `nvme` CLI
  (`nvme connect -t tcp -n <nqn> -a <addr> -s <trsvcid>`).
- Semantics: `SINGLE_NODE_WRITER`; **block mode** (raw device) and
  **filesystem mode** (`fsType` created on the block device at stage time).
  No snapshot/clone/expand layer.

## 3. External counterparts

In Kubernetes the driver is consumed by *external* components:

- **external-provisioner** – watches PVCs, calls CSI controller RPCs to
  provision/delete volumes.
- **external-attacher / CSI node driver** – calls node RPCs to attach &
  publish.
- **external-snapshotter / external-resizer** – snapshots & expansion.

When the physical disks are **not directly on the Kubernetes node**, the flow
changes character: the *controller* provisions the remote backend (ZFS zvol +
NVMe-oF subsystem), and the *node* performs the initiator connection over
NVMe-oF/TCP.

## 4. Design diagrams

### 4.1 Driver internals

```mermaid
flowchart LR
    subgraph Proc["nvmeof-csi process (Linux)"]
        MAIN["main.go<br/>--socket --target-addr --port --pool --mode"]
        SRV["CSI gRPC server<br/>unix socket"]
        ID["Identity"]
        CTRL["Controller"]
        NODE["Node"]
        DRV["Driver"]
        NVMF["nvmet (kernel)<br/>ports / subsystems / namespaces"]
        ZFS["ZFS zvol"]
        NVME["nvme CLI<br/>connect / disconnect / list"]
    end
    MAIN --> SRV
    SRV --> ID
    SRV --> CTRL
    SRV --> NODE
    CTRL --> DRV
    NODE --> DRV
    DRV --> NVMF
    DRV --> ZFS
    DRV --> NVME
```

### 4.2 Full PV lifecycle with external components

```mermaid
flowchart TB
    subgraph K8s["Kubernetes control plane / nodes"]
        API["API server"]
        EP["external-provisioner"]
        ND["CSI node driver<br/>(on worker node)"]
        POD["Pod (initiator)"]
    end

    subgraph CSI["CSI driver: nvmeof-csi"]
        C["Controller: CreateVolume / DeleteVolume"]
        N["Node: Stage / Publish / Unstage / Unpublish"]
    end

    subgraph BE["Storage backend (may be remote / external to the node)"]
        ZFS["ZFS zvol"]
        NVMF["NVMe-oF/TCP target"]
        ARRAY["Remote disk array"]
    end

    PVC["PVC default/vol-a"] --> EP
    EP --> C
    C --> ZFS
    C --> NVMF
    NVMF --> ARRAY
    ND --> N
    N --> NVMF
    POD --> ND
```

### 4.3 Sequence: provision + publish when storage is external

```mermaid
sequenceDiagram
    autonumber
    participant EP as external-provisioner
    participant C as CSI Controller
    participant N as CSI Node
    participant T as NVMe-oF/TCP backend
    participant I as Initiator (pod)

    EP->>C: CreateVolume(cap=[SINGLE_NODE_WRITER, block], req-bytes, params ns/name)
    C->>T: zfs create zvol
    C->>T: nvmet create subsystem / namespace / port
    C-->>EP: CreateVolumeResponse (volume context: nqn, dataset, nsid)

    EP->>N: NodeStageVolume / NodePublishVolume
    N->>T: nvme connect -t tcp -n NQN -a addr -s trsvcid
    N->>I: block device appears (/dev/nvmeXnY)
    N-->>EP: published
```

## 5. The "disks not on the node" case

- Controller provisions the zvol on the backend (ZFS) and exposes it as an
  NVMe-oF/TCP subsystem/Namespace/Port.
- Node driver attaches the initiator over TCP; block volumes are bind-mounted
  as the raw device, filesystem volumes are `mkfs`'d (if needed) and mounted
  at stage time, then bind-mounted into the pod.
- The controller never needs to "know" about the node's local disks; the node
  handles the local attach.

## 6. Observations from implementation review

- Driver builds cleanly; manual e2e flow exists (`test/manual/e2e.sh`).
- Capability advertisement vs. implemented RPCs should be kept in sync
  (identity: controller capability advertised for modes serving the
  controller; node: stage/unstage advertised via NodeGetCapabilities,
  publish/unpublish implemented).
- `ControllerPublishVolume`/`ControllerUnpublishVolume` are no-ops here while
  node publish does the real work — an architectural simplification worth
  documenting.
