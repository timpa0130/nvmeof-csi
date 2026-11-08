//go:build linux

package csi

import (
	"context"
	"errors"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/timpa0130/nvmeof-csi/pkg/ids"
	"github.com/timpa0130/nvmeof-csi/pkg/nvmet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultVolumeSize int64 = 1 << 30

// K8s external-provisioner parameter keys.
const (
	paramPVCNamespace = "csi.storage.k8s.io/pvc/namespace"
	paramPVCName      = "csi.storage.k8s.io/pvc/name"
)

type controllerServer struct {
	d *Driver
	csi.UnimplementedControllerServer
}

// CreateVolume provisions a ZFS zvol and exposes it as a single-namespace
// NVMe-oF/TCP subsystem on the shared port. Everything (volume ID, NQN,
// dataset) is derived deterministically from the request parameters.
func (s *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if err := validateCapabilities(req.GetVolumeCapabilities()); err != nil {
		return nil, err
	}

	namespace := req.GetParameters()[paramPVCNamespace]
	name := req.GetParameters()[paramPVCName]
	if name == "" {
		name = req.GetName()
	}
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}
	if !ids.ValidNamespace(namespace) || !ids.ValidName(name) {
		return nil, status.Error(codes.InvalidArgument, "volume name must not contain '/' and namespace must not contain '/' or '.'")
	}

	volumeID := ids.VolumeID(namespace, name)
	scope := s.d.scope()
	nqn := scope.NQN(volumeID)
	dataset := scope.Dataset(namespace, name)
	if len(nqn) > ids.MaxNQN {
		return nil, status.Errorf(codes.InvalidArgument, "derived NQN too long (%d > %d bytes); use a shorter volume name", len(nqn), ids.MaxNQN)
	}
	if len(dataset) > ids.MaxDataset {
		return nil, status.Errorf(codes.InvalidArgument, "derived dataset name too long (%d > %d)", len(dataset), ids.MaxDataset)
	}

	size := req.GetCapacityRange().GetRequiredBytes()
	if size <= 0 {
		size = defaultVolumeSize
	}

	// Idempotency: an existing zvol with matching size is returned, with its
	// export completed in case an earlier attempt stopped short.
	if s.d.zfs.Exists(dataset) {
		existing, err := s.d.zfs.VolSize(dataset)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "create volume: %v", err)
		}
		if existing != size {
			return nil, status.Errorf(codes.AlreadyExists, "volume %s exists with different size", volumeID)
		}
		if err := s.d.exportVolume(volumeID); err != nil {
			return nil, status.Errorf(codes.Internal, "create volume: %v", err)
		}
		return &csi.CreateVolumeResponse{Volume: s.volumeInfo(volumeID, dataset, size)}, nil
	}

	// Clean up any half-provisioned state on failure.
	fail := func(err error) (*csi.CreateVolumeResponse, error) {
		_ = s.d.target.TeardownVolume(nqn)
		_ = s.d.zfs.DestroyVolume(dataset)
		return nil, status.Errorf(codes.Internal, "create volume: %v", err)
	}

	if err := s.d.zfs.CreateVolume(dataset, size); err != nil {
		return fail(err)
	}
	if err := s.d.exportVolume(volumeID); err != nil {
		return fail(err)
	}
	return &csi.CreateVolumeResponse{Volume: s.volumeInfo(volumeID, dataset, size)}, nil
}

// DeleteVolume tears down the subsystem and destroys the zvol. Missing
// volumes are reported as success.
func (s *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volumeID, err := requireVolumeID(req)
	if err != nil {
		return nil, err
	}
	nqn := s.d.scope().NQN(volumeID)
	dataset := s.d.scope().Dataset(ids.SplitVolumeID(volumeID))
	if err := s.d.target.TeardownVolume(nqn); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume: %v", err)
	}
	if err := s.d.zfs.DestroyVolume(dataset); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume: %v", err)
	}
	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume admits the node's host NQN to the volume's
// subsystem. A volume held by another node is refused, which is what
// enforces single-node access across nodes. The export is completed first,
// so a retry heals a volume Reconcile could not export, and the holder is
// recorded on the zvol so the grant survives a reboot of the target host.
func (s *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	volumeID, err := requireVolumeID(req)
	if err != nil {
		return nil, err
	}
	nodeID := req.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}
	if err := validateCapabilities([]*csi.VolumeCapability{req.GetVolumeCapability()}); err != nil {
		return nil, err
	}
	scope := s.d.scope()
	hostNQN := scope.HostNQN(nodeID)
	if len(hostNQN) > ids.MaxNQN {
		return nil, status.Errorf(codes.InvalidArgument, "derived host NQN too long (%d > %d bytes)", len(hostNQN), ids.MaxNQN)
	}
	dataset := scope.Dataset(ids.SplitVolumeID(volumeID))
	if !s.d.zfs.Exists(dataset) {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
	}
	if err := s.d.exportVolume(volumeID); err != nil {
		return nil, status.Errorf(codes.Internal, "publish volume: %v", err)
	}
	if err := s.d.target.GrantHost(scope.NQN(volumeID), hostNQN); err != nil {
		if errors.Is(err, nvmet.ErrHeldByOtherHost) {
			return nil, status.Errorf(codes.FailedPrecondition, "publish volume %s to %s: %v", volumeID, nodeID, err)
		}
		return nil, status.Errorf(codes.Internal, "publish volume: %v", err)
	}
	if err := s.d.zfs.SetHolder(dataset, nodeID); err != nil {
		return nil, status.Errorf(codes.Internal, "publish volume: %v", err)
	}
	return &csi.ControllerPublishVolumeResponse{}, nil
}

// ControllerUnpublishVolume revokes the node's access and drops any
// connection it still has. An empty node ID revokes every node, per the
// CSI spec. The holder is cleared before the revoke: until the revoke no other
// node can be granted, so a retry cannot clear a newer holder's record.
func (s *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	volumeID, err := requireVolumeID(req)
	if err != nil {
		return nil, err
	}
	nodeID := req.GetNodeId()
	scope := s.d.scope()
	dataset := scope.Dataset(ids.SplitVolumeID(volumeID))
	if s.d.zfs.Exists(dataset) {
		holder, err := s.d.zfs.Holder(dataset)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "unpublish volume: %v", err)
		}
		if holder != "" && (nodeID == "" || holder == nodeID) {
			if err := s.d.zfs.ClearHolder(dataset); err != nil {
				return nil, status.Errorf(codes.Internal, "unpublish volume: %v", err)
			}
		}
	}
	var hostNQN string
	if nodeID != "" {
		hostNQN = scope.HostNQN(nodeID)
	}
	if err := s.d.target.RevokeHost(scope.NQN(volumeID), hostNQN); err != nil {
		return nil, status.Errorf(codes.Internal, "unpublish volume: %v", err)
	}
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (s *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volumeID := req.GetVolumeId()
	if !s.d.zfs.Exists(s.d.scope().Dataset(ids.SplitVolumeID(volumeID))) {
		return nil, status.Error(codes.NotFound, "volume not found")
	}
	if err := validateCapabilities(req.GetVolumeCapabilities()); err != nil {
		return &csi.ValidateVolumeCapabilitiesResponse{Message: err.Error()}, nil
	}
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.GetVolumeCapabilities(),
		},
	}, nil
}

func (s *controllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	datasets, err := s.d.zfs.Volumes()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list volumes: %v", err)
	}
	entries := make([]*csi.ListVolumesResponse_Entry, 0, len(datasets))
	for _, dataset := range datasets {
		volumeID, ok := s.d.scope().ParseDataset(dataset)
		if !ok {
			continue // not ours
		}
		size, err := s.d.zfs.VolSize(dataset)
		if err != nil {
			continue
		}
		entries = append(entries, &csi.ListVolumesResponse_Entry{
			Volume: s.volumeInfo(volumeID, dataset, size),
		})
	}
	return &csi.ListVolumesResponse{Entries: entries}, nil
}

func (s *controllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	free, err := s.d.zfs.FreeBytes()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get capacity: %v", err)
	}
	return &csi.GetCapacityResponse{AvailableCapacity: free}, nil
}

func (s *controllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME},
				},
			},
		},
	}, nil
}

func (s *controllerServer) volumeInfo(volumeID, dataset string, size int64) *csi.Volume {
	return &csi.Volume{
		VolumeId:      volumeID,
		CapacityBytes: size,
		VolumeContext: map[string]string{
			"nqn":     s.d.scope().NQN(volumeID),
			"nsid":    "1",
			"dataset": dataset,
		},
	}
}
