//go:build linux

package csi

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// requireVolumeID returns the request's volume ID or an InvalidArgument
// error if it is missing.
func requireVolumeID(req interface{ GetVolumeId() string }) (string, error) {
	id := req.GetVolumeId()
	if id == "" {
		return "", status.Error(codes.InvalidArgument, "volume id is required")
	}
	return id, nil
}

// validateCapabilities enforces v1 scope: block access only, single node
// writer access mode only.
func validateCapabilities(caps []*csi.VolumeCapability) error {
	if len(caps) == 0 {
		return status.Error(codes.InvalidArgument, "volume capabilities are required")
	}
	for _, c := range caps {
		if c == nil {
			return status.Error(codes.InvalidArgument, "nil volume capability")
		}
		if c.GetMount() != nil {
			return status.Error(codes.InvalidArgument, "filesystem (mount) mode is not supported; block mode only")
		}
		if c.GetBlock() == nil {
			return status.Error(codes.InvalidArgument, "volume capability must specify block access")
		}
		mode := c.GetAccessMode().GetMode()
		if mode != csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
			return status.Errorf(codes.InvalidArgument, "unsupported access mode %s", mode)
		}
	}
	return nil
}
