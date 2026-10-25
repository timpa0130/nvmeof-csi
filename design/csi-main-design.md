# Design: nvmeof-csi entrypoint and process lifecycle

## Purpose

Owns `cmd/nvmeof-csi/main.go`: flag parsing, driver construction, mode-based
behavior, and shutdown.

## Startup flow

```mermaid
flowchart TD
    START["nvmeof-csi (linux)<br/>os.Args"] --> PARSE["flag.Parse<br/>--socket --target-addr --port --pool --mode --node-id"]
    PARSE --> TADDR{"--target-addr empty?"}
    TADDR -->|"yes"| AUTO["defaultIPv4()<br/>first non-loopback IPv4"]
    TADDR -->|"no"| USE["use flag value"]
    AUTO --> NEW["csi.NewDriver(Config{...})"]
    USE --> NEW
    PARSE --> NODEID{"--node-id empty?"}
    NODEID -->|"yes"| HOST["os.Hostname()<br/>(fallback)"]
    NODEID -->|"no"| NODEIDVAL["--node-id value"]
    HOST --> NEW
    NODEIDVAL --> NEW
    NEW --> MODE{"mode == node?"}
    MODE -->|"yes"| SKIP["skip EnsurePort / Reconcile"]
    MODE -->|"no"| RUN["driver.EnsurePort()<br/>driver.Reconcile()"]
    RUN --> LISTEN["driver.Run():<br/>unix socket, register, serve"]
```

## Flags

- `--socket` default `/run/csi/nvmeof-csi.sock`
- `--target-addr` default `""` → auto-detect via `defaultIPv4()` (first
  non-loopback IPv4)
- `--port` default `csi.DefaultPort` (`4420`)
- `--pool` default `nvme`
- `--mode` default `all`; validated to `all|controller|node`
- `--node-id` default `""` → falls back to `os.Hostname()` (K8s node name)

## Node-only behavior

Node-only processes have no target side → skip `EnsurePort()` and `Reconcile()`.

## Shutdown

Context from `signal.NotifyContext(SIGINT, SIGTERM)`; `driver.Run` graceful-stops
the gRPC server when the context is done.
