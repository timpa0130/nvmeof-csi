# Design: CSI identity service

## Purpose

Plugin metadata, capabilities, and readiness probe.

## RPCs

- `GetPluginInfo` — returns `PluginName` (`com.timpa0130.nvmeof-csi`) and
  `PluginVersion` (`1.0.0`); no `Vendor` set.
- `GetPluginCapabilities` — advertises `CONTROLLER_SERVICE` only.
- `Probe` — ready iff `nvmet.Available()` (nvmet kernel configfs present).

## Design notes

- Identity always registered, regardless of mode.
- Probe gates readiness on the NVMe-oF kernel backend being up.
