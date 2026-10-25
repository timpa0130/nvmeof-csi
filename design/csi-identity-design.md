# Design: CSI identity service

## Purpose

Plugin metadata, capabilities, and readiness probe.

## RPCs

- `GetPluginInfo` — returns `PluginName` (`com.timpa0130.nvmeof-csi`) and
  `PluginVersion` (`1.0.0`); no `Vendor` set.
- `GetPluginCapabilities` — advertises `CONTROLLER_SERVICE` when the process
  serves the controller service (modes `all` and `controller`); no capabilities
  in `node`-only mode (controller not served there).
- `Probe` — ready iff `nvmet.Available()` (nvmet kernel configfs present).

## Design notes

- Identity always registered, regardless of mode.
- Capability advertisement mirrors the mode registration matrix (see
  `design/csi-server-design.md`): only services served by the active mode are
  advertised. In spec v1.13 the `GetPluginCapabilities` response carries
  controller service capabilities; node service capabilities (RPC
  `STAGE_UNSTAGE_VOLUME`) are advertised through the node service's
  `NodeGetCapabilities` instead.
- Probe gates readiness on the NVMe-oF kernel backend being up.
