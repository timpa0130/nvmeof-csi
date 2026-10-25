# Build the Go binary first: go build -o bin/nvmeof-csi ./cmd/nvmeof-csi

FROM alpine:3.20

COPY bin/nvmeof-csi /usr/local/bin/nvmeof-csi

ENTRYPOINT ["/usr/local/bin/nvmeof-csi"]
