# Design: nvmeof-csi container image

## Purpose

Package the `nvmeof-csi` Go binary as a container image so the driver can run
in Kubernetes via the Helm chart (`charts/nvmeof-csi`).

## Components

- `Dockerfile` — runtime image: `alpine:3.20` + the statically linked
  `bin/nvmeof-csi` Go binary (built via
  `go build -o bin/nvmeof-csi ./cmd/nvmeof-csi`).
- `.github/workflows/build.yml` — CI workflow: builds the Go binary, then the
  container image; publishes to `ghcr.io/timpa0130/nvmeof-csi` on push to main.

## Build flow

```mermaid
flowchart LR
    SRC["Source: cmd/nvmeof-csi"] --> BIN["go build -o bin/nvmeof-csi ./cmd/nvmeof-csi"]
    BIN --> IMG["Dockerfile<br/>alpine + nvmeof-csi"]
    IMG --> REG["ghcr.io/timpa0130/nvmeof-csi"]
```

## CI behavior

- Triggered by changes to `Dockerfile`, `cmd/**`, `pkg/**`, `go.mod`, `go.sum`.
- Pull requests: build image only (no push).
- Push to `main`: push `latest` + `sha-<short>` tags.

## Deployment

- Helm `image.repository` / `image.tag` reference the published image
  (e.g. `ghcr.io/timpa0130/nvmeof-csi:latest`).
- Controller Deployment passes `--mode controller`; node DaemonSet passes
  `--mode node` (+ `--target-addr`).

## Design notes

- Go binary is statically linked (no CGO) → `alpine` base is sufficient runtime.
- No userspace packages required beyond the base image.

## Open questions

- Multi-arch (amd64/arm64) image build via QEMU.
