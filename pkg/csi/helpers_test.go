//go:build linux

package csi

import (
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func blockCap() *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
}

func mountCap() *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
}

func TestValidateCapabilitiesAcceptsBlockAndMount(t *testing.T) {
	for name, cap := range map[string]*csi.VolumeCapability{"block": blockCap(), "mount": mountCap()} {
		if err := validateCapabilities([]*csi.VolumeCapability{cap}); err != nil {
			t.Fatalf("%s capability should be accepted: %v", name, err)
		}
	}
}

func TestValidateCapabilitiesRejectsNoAccessType(t *testing.T) {
	cap := &csi.VolumeCapability{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
	if err := validateCapabilities([]*csi.VolumeCapability{cap}); err == nil {
		t.Fatal("capability without block/mount access type should be rejected")
	}
}

func TestValidateCapabilitiesRejectsWrongAccessMode(t *testing.T) {
	for _, cap := range []*csi.VolumeCapability{blockCap(), mountCap()} {
		cap.AccessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
		if err := validateCapabilities([]*csi.VolumeCapability{cap}); err == nil {
			t.Fatal("non SINGLE_NODE_WRITER capability should be rejected")
		}
	}
}

func TestValidateCapabilitiesRejectsEmptyAndNil(t *testing.T) {
	if err := validateCapabilities(nil); err == nil {
		t.Fatal("empty capability list should be rejected")
	}
	if err := validateCapabilities([]*csi.VolumeCapability{nil}); err == nil {
		t.Fatal("nil capability should be rejected")
	}
}