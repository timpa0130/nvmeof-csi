# Design: nvmeof-csi container image

## Purpose

Package the `nvmeof-csi` Go binary as a container image so the driver can run
in Kubernetes via the Helm chart (`charts/nvmeof-csi`).

## Components

- `Dockerfile` — runtime image: `alpine` base + the statically linked
  `bin/nvmeof-csi` Go binary (built via
  `CGO_ENABLED=0 go build -o bin/nvmeof-csi ./cmd/nvmeof-csi`), plus the
  runtime userspace packages `nvme-cli` (initiator connect/list) and
  `e2fsprogs` (`mkfs.ext4` + `blkid` for filesystem-mode staging).
- `.github/workflows/build.yml` — CI workflow: builds the Go binary, then the
  container image; publishes to `ghcr.io/timpa0130/nvmeof-csi` on push to main.

## Build flow

```mermaid
flowchart LR
    SRC["Source: cmd/nvmeof-csi"] --> BIN["CGO_ENABLED=0 go build -o bin/nvmeof-csi ./cmd/nvmeof-csi"]
    BIN --> IMG["Dockerfile<br/>alpine + nvmeof-csi + nvme-cli + e2fsprogs"]
    IMG --> REG["ghcr.io/timpa0130/nvmeof-csi"]
```

## CI behavior

- Triggered by changes to `Dockerfile`, `cmd/**`, `pkg/**`, `go.mod`, `go.sum`.
- Pull requests: build image only (no push).
- Push to `main`: push `latest` + `sha-<short>` tags.
- Release published: build Go binary and upload to the GitHub release
  (`gh release upload <tag> bin/nvmeof-csi`).

## Deployment

- Helm `image.repository` / `image.tag` reference the published image
  (e.g. `ghcr.io/timpa0130/nvmeof-csi:latest`).
- Controller Deployment passes `--mode controller`; node DaemonSet passes
  `--mode node` (+ `--target-addr`).

## Design notes

- Go binary is statically linked (no CGO) → `alpine` base is sufficient runtime.
- Userspace packages are limited to what the driver execs at runtime:
  `nvme` (connect/list/disconnect) and `mkfs.*`/`blkid` (filesystem staging).
  ZFS tools are only needed by controller mode, which runs on the external
  storage node, not in this image.

## Open questions

- Multi-arch (amd64/arm64) image build via QEMU.
