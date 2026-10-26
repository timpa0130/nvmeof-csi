//go:build linux

package node

import (
	"reflect"
	"testing"
)

func TestMkfsArgsDefaultsToExt4(t *testing.T) {
	got, err := mkfsArgs("/dev/nvme0n1", "")
	if err != nil {
		t.Fatalf("mkfsArgs: %v", err)
	}
	want := []string{"mkfs.ext4", "-F", "/dev/nvme0n1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mkfsArgs = %v, want %v", got, want)
	}
}

func TestMkfsArgsExtAndXfs(t *testing.T) {
	cases := []struct {
		fstype string
		want   []string
	}{
		{"ext4", []string{"mkfs.ext4", "-F", "/dev/sda"}},
		{"ext3", []string{"mkfs.ext3", "-F", "/dev/sda"}},
		{"ext2", []string{"mkfs.ext2", "-F", "/dev/sda"}},
		{"xfs", []string{"mkfs.xfs", "-f", "/dev/sda"}},
	}
	for _, c := range cases {
		got, err := mkfsArgs("/dev/sda", c.fstype)
		if err != nil {
			t.Fatalf("mkfsArgs(%q): %v", c.fstype, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("mkfsArgs(%q) = %v, want %v", c.fstype, got, c.want)
		}
	}
}

func TestMkfsArgsUnsupportedType(t *testing.T) {
	if _, err := mkfsArgs("/dev/sda", "btrfs"); err == nil {
		t.Fatal("mkfsArgs(btrfs) should fail")
	}
}
