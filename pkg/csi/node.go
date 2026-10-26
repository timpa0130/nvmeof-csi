//go:build linux

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
// block mode this is all staging does: no filesystem is created. In
// filesystem mode it also creates the requested filesystem (default
// ext4) when the device has none and mounts the device at the staging
// path.
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
	mountCap := req.GetVolumeCapability().GetMount()
	if mountCap == nil {
		// Block: the connection is the staging — but still cap the
		// device's I/O size (target rejects chained SGLs otherwise).
		if err := node.CapMaxSectors(nqn); err != nil {
			return nil, status.Errorf(codes.Internal, "cap max sectors: %v", err)
		}
		return &csi.NodeStageVolumeResponse{}, nil
	}
	staging := req.GetStagingTargetPath()
	if staging == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is required")
	}
	if isMounted(staging) {
		return &csi.NodeStageVolumeResponse{}, nil
	}
	dev, err := node.FindDeviceByNQN(nqn)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "find device: %v", err)
	}
	if err := node.CapMaxSectors(nqn); err != nil {
		return nil, status.Errorf(codes.Internal, "cap max sectors: %v", err)
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return nil, status.Errorf(codes.Internal, "create staging dir: %v", err)
	}
	fstype := mountCap.GetFsType()
	if fstype == "" {
		fstype = "ext4"
	}
	if !node.HasFilesystem(dev) {
		if err := node.MakeFilesystem(dev, fstype); err != nil {
			return nil, status.Errorf(codes.Internal, "make filesystem: %v", err)
		}
	}
	if err := unix.Mount(dev, staging, fstype, 0, ""); err != nil {
		return nil, status.Errorf(codes.Internal, "mount %s -> %s: %v", dev, staging, err)
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume unmounts the staging path (filesystem mode) and
// disconnects the initiator from the target subsystem.
func (s *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID, err := requireVolumeID(req)
	if err != nil {
		return nil, err
	}
	if staging := req.GetStagingTargetPath(); staging != "" && isMounted(staging) {
		if err := unix.Unmount(staging, 0); err != nil {
			if !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOENT) {
				return nil, status.Errorf(codes.Internal, "unmount %s: %v", staging, err)
			}
		}
	}
	nqn := ids.NQN(volumeID)
	if err := node.Disconnect(nqn); err != nil {
		return nil, status.Errorf(codes.Internal, "unstage volume: %v", err)
	}
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume bind-mounts the volume into the target path: block
// mode mounts the device node onto a file; filesystem mode bind-mounts
// the staged filesystem directory onto the target directory.
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
	if req.GetVolumeCapability().GetMount() != nil {
		staging := req.GetStagingTargetPath()
		if staging == "" {
			return nil, status.Error(codes.InvalidArgument, "staging target path is required")
		}
		return s.publishFilesystem(staging, target)
	}
	return s.publishBlock(req, ids.NQN(volumeID), target)
}

// publishFilesystem bind-mounts the staged filesystem onto the target
// directory.
func (s *nodeServer) publishFilesystem(staging, target string) (*csi.NodePublishVolumeResponse, error) {
	if err := os.MkdirAll(target, 0o750); err != nil {
		return nil, status.Errorf(codes.Internal, "create target dir: %v", err)
	}
	if isMounted(target) {
		return &csi.NodePublishVolumeResponse{}, nil
	}
	if err := unix.Mount(staging, target, "", unix.MS_BIND, ""); err != nil {
		return nil, status.Errorf(codes.Internal, "bind mount %s -> %s: %v", staging, target, err)
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

// publishBlock bind-mounts the block device onto the target file.
func (s *nodeServer) publishBlock(req *csi.NodePublishVolumeRequest, nqn, target string) (*csi.NodePublishVolumeResponse, error) {
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
