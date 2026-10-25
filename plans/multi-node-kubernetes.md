# Plan: Multi-Node Kubernetes Support for nvmeof-csi (single-node-writer scope)

## Purpose

Define the implementation plan for running the `nvmeof-csi` CSI driver
across multiple Kubernetes nodes while keeping the supported access mode at
`SINGLE_NODE_WRITER`.

## Background / current state

- The driver already supports a mode split (`all|controller|node`) so a
  controller-side process and per-host node processes can exist separately
  (`cmd/nvmeof-csi/main.go`, `design/csi-main-design.md`, `design/csi-server-design.md`).
- With `SINGLE_NODE_WRITER` chosen (see `pkg/csi/helpers.go`,
  `design/csi-helpers-design.md`), multi-node *volume sharing* is out of
  scope; the remaining work is making the driver usable when initiator pods
  run on multiple K8s nodes.

## Remaining gaps identified (from review)

1. **Identity capability advertisement**
   - Gap: `pkg/csi/identity.go` `GetPluginCapabilities` advertises only
     `CONTROLLER_SERVICE`, although the design docs (`design/csi-identity-design.md`,
     registration matrix in `design/csi-server-design.md`) intend node services
     to be advertised in `all`/`node` modes.
   - Work: advertise the node service capability (RPC stage/unstage; publish/unpublish
     implemented but not yet advertised) alongside the controller capability.

2. **Node identity**
   - Gap: `NodeGetInfo` returns `os.Hostname()` (`cmd/nvmeof-csi/main.go`), which is
     a host-level identifier, not a K8s node identifier.
   - Work: source `NodeID` from the K8s node (e.g. downward API
     `spec.nodeName` via CSI node driver registrar / `--node-id` flag).

3. **Kubernetes deployment artifacts**
   - Gap: no CSI driver manifests/charts exist in the repo yet.
   - Work: add manifests for:
     - controller deployment (`--mode controller` or `all`) with ZFS backend
     - node daemonset (`--mode node`) per worker node, pointing at the
       controller/backend via `--target-addr`
     - `CSIDriver` object wiring external-provisioner / node-driver sidecars.

4. **Attach semantics**
   - Gap: `ControllerPublishVolume`/`ControllerUnpublishVolume` are no-ops by design;
     attach is realized via NVMe-oF/TCP connect at stage time.
   - Work: confirm external-attacher expectations; no change expected if design intent
     holds.

## Proposed implementation steps

1. Update `GetPluginCapabilities` in `pkg/csi/identity.go` to advertise node
   service capabilities; sync design docs (`design/csi-identity-design.md`,
   `design/csi-server-design.md`) in the same change.
2. Add node ID override (`--node-id` flag or downward-API sourced value) and wire
   it through `cmd/nvmeof-csi/main.go` into `csi.Config.NodeID`.
3. Add Kubernetes deployment manifests under `deploy/` (controller + node +
   CSIDriver).
4. Run build/tests (`go build ./...`, `go test ./pkg/...`) and manual e2e
   (`test/manual/e2e.sh`) before K8s integration testing.

## Non-goals

- Multi-node multi-writer volume sharing (requires different access modes /
  namespace topology; explicitly out of scope for single-node-writer design).
- Second storage cluster (fixed NQN root in `pkg/ids/ids.go` is single-cluster).

## Verification

- Unit: `go test ./pkg/...`.
- Manual e2e: `sudo ./test/manual/e2e.sh` (csc flow, `SINGLE_NODE_WRITER,block`).
- K8s: deploy controller + node daemonset across ≥2 worker nodes; stage/publish a
  volume from a pod on each node; verify independent attach per node.

## Open questions

- Should identity advertise the full node capability set (stage/unstage + publish/unpublish)
  or just stage/unstage for v1?
- Exact K8s node identity source for `NodeID` (downward API vs flag).
- Whether the external-attacher requires controller attach RPCs despite the
  node-side attach design.
