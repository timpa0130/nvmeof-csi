//go:build linux

package csi

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/timpa0130/nvmeof-csi/pkg/nvmet"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type identityServer struct {
	d *Driver
	csi.UnimplementedIdentityServer
}

func (s *identityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          PluginName,
		VendorVersion: PluginVersion,
	}, nil
}

func (s *identityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	caps := []*csi.PluginCapability{}
	// The CSI GetPluginCapabilities response carries controller service
	// capabilities; the node service capability (e.g. STAGE_UNSTAGE_VOLUME)
	// is advertised via the node service's NodeGetCapabilities instead. Only
	// advertise what the configured mode actually serves.
	if s.d.cfg.Mode == ModeAll || s.d.cfg.Mode == ModeController {
		caps = append(caps, controllerServiceCapability())
	}
	return &csi.GetPluginCapabilitiesResponse{Capabilities: caps}, nil
}

// controllerServiceCapability advertises the controller service capability
// (create/delete volume) via the identity service.
func controllerServiceCapability() *csi.PluginCapability {
	return &csi.PluginCapability{
		Type: &csi.PluginCapability_Service_{
			Service: &csi.PluginCapability_Service{Type: csi.PluginCapability_Service_CONTROLLER_SERVICE},
		},
	}
}

func (s *identityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	ready := nvmet.Available()
	return &csi.ProbeResponse{Ready: wrapperspb.Bool(ready)}, nil
}
