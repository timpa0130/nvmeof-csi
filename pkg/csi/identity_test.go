//go:build linux

package csi

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		mode     Mode
		wantCaps int
	}{
		{name: "all serves controller", mode: ModeAll, wantCaps: 1},
		{name: "controller serves controller", mode: ModeController, wantCaps: 1},
		{name: "node does not serve controller", mode: ModeNode, wantCaps: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &identityServer{d: &Driver{cfg: Config{Mode: tt.mode}}}
			resp, err := s.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
			if err != nil {
				t.Fatalf("GetPluginCapabilities: %v", err)
			}
			if got := len(resp.GetCapabilities()); got != tt.wantCaps {
				t.Fatalf("capabilities = %d, want %d (%v)", got, tt.wantCaps, resp.GetCapabilities())
			}
			for _, c := range resp.GetCapabilities() {
				if c.GetService().GetType() != csi.PluginCapability_Service_CONTROLLER_SERVICE {
					t.Fatalf("unexpected capability: %v", c)
				}
			}
		})
	}
}
