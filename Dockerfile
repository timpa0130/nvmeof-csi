# Build the Go binary first: go build -o bin/nvmeof-csi ./cmd/nvmeof-csi

FROM alpine:latest

# nvme-cli: initiator connect/list for the node service.
# e2fsprogs: mkfs.ext4 + blkid for filesystem-mode staging.
RUN apk add --no-cache nvme-cli e2fsprogs

COPY bin/nvmeof-csi /usr/local/bin/nvmeof-csi

ENTRYPOINT ["/usr/local/bin/nvmeof-csi"]