//go:build linux

package csi

import (
	"context"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/timpa0130/nvmeof-csi/pkg/ids"
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
	nqn := ids.NQN(volumeID)
	dataset := ids.Dataset(s.d.cfg.Pool, namespace, name)
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

	// Idempotency: an existing zvol with matching size is returned.
	if s.d.zfs.Exists(dataset) {
		existing, err := s.d.zfs.VolSize(dataset)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "create volume: %v", err)
		}
		if existing != size {
			return nil, status.Errorf(codes.AlreadyExists, "volume %s exists with different size", volumeID)
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
	if err := s.d.target.CreateSubsystem(nqn); err != nil {
		return fail(err)
	}
	if err := s.d.target.CreateNamespace(nqn, 1, s.d.zfs.DevicePath(dataset)); err != nil {
		return fail(err)
	}
	if err := s.d.target.LinkSubsystemToPort(nqn); err != nil {
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
	ns, name := ids.SplitVolumeID(volumeID)
	nqn := ids.NQN(volumeID)
	dataset := ids.Dataset(s.d.cfg.Pool, ns, name)
	if err := s.d.target.TeardownVolume(nqn); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume: %v", err)
	}
	if err := s.d.zfs.DestroyVolume(dataset); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume: %v", err)
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (s *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (s *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (s *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volumeID := req.GetVolumeId()
	ns, name := ids.SplitVolumeID(volumeID)
	if !s.d.zfs.Exists(ids.Dataset(s.d.cfg.Pool, ns, name)) {
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
		size, err := s.d.zfs.VolSize(dataset)
		if err != nil {
			continue
		}
		volumeID := strings.TrimPrefix(dataset, s.d.cfg.Pool+"/")
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
		},
	}, nil
}

func (s *controllerServer) volumeInfo(volumeID, dataset string, size int64) *csi.Volume {
	return &csi.Volume{
		VolumeId:      volumeID,
		CapacityBytes: size,
		VolumeContext: map[string]string{
			"nqn":     ids.NQN(volumeID),
			"nsid":    "1",
			"dataset": dataset,
		},
	}
}
