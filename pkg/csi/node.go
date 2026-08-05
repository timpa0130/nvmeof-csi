package csi

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/timpa0130/nvmeof-csi/pkg/ids"
	"github.com/timpa0130/nvmeof-csi/pkg/node"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nodeServer struct {
	d *Driver
	csi.UnimplementedNodeServer
}

func (s *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: s.d.cfg.NodeID}, nil
}

func (s *nodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME},
				},
			},
		},
	}, nil
}

// NodeStageVolume connects the initiator to the target subsystem. In
// block mode this is all staging does: no filesystem is created.
func (s *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if err := validateCapabilities([]*csi.VolumeCapability{req.GetVolumeCapability()}); err != nil {
		return nil, err
	}
	volumeID, err := requireVolumeID(req)
	if err != nil {
		return nil, err
	}
	nqn := ids.NQN(volumeID)
	if err := node.Connect(ctx, nqn, s.d.cfg.TargetAddr, s.d.cfg.Port); err != nil {
		return nil, status.Errorf(codes.Internal, "stage volume: %v", err)
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume disconnects the initiator from the target subsystem.
func (s *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID, err := requireVolumeID(req)
	if err != nil {
		return nil, err
	}
	nqn := ids.NQN(volumeID)
	if err := node.Disconnect(nqn); err != nil {
		return nil, status.Errorf(codes.Internal, "unstage volume: %v", err)
	}
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume bind-mounts the block device into the target path.
func (s *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if err := validateCapabilities([]*csi.VolumeCapability{req.GetVolumeCapability()}); err != nil {
		return nil, err
	}
	target := req.GetTargetPath()
	if target == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	volumeID, err := requireVolumeID(req)
	if err != nil {
		return nil, err
	}
	nqn := ids.NQN(volumeID)
	dev, err := node.FindDeviceByNQN(nqn)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "find device: %v", err)
	}
	// For block volumes the target path is a file (the device is
	// bind-mounted onto it), not a directory.
	if st, serr := os.Stat(target); serr == nil {
		if st.IsDir() {
			return nil, status.Errorf(codes.InvalidArgument, "target path %s for a block volume must be a file, not a directory", target)
		}
	} else if !os.IsNotExist(serr) {
		return nil, status.Errorf(codes.Internal, "stat target: %v", serr)
	} else {
		f, cerr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o600)
		if cerr != nil {
			return nil, status.Errorf(codes.Internal, "create target file: %v", cerr)
		}
		_ = f.Close()
	}
	if isMounted(target) {
		return &csi.NodePublishVolumeResponse{}, nil
	}
	if err := unix.Mount(dev, target, "", unix.MS_BIND, ""); err != nil {
		return nil, status.Errorf(codes.Internal, "bind mount %s -> %s: %v", dev, target, err)
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmounts the bind mount at the target path.
func (s *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	target := req.GetTargetPath()
	if target == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	if err := unix.Unmount(target, 0); err != nil {
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOENT) {
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "unmount %s: %v", target, err)
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// isMounted reports whether path is currently a mountpoint.
func isMounted(path string) bool {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && unescapeMountPath(fields[4]) == path {
			return true
		}
	}
	return false
}

func unescapeMountPath(s string) string {
	return strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\").Replace(s)
}
