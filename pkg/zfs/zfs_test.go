package zfs

import "testing"

func TestDevicePath(t *testing.T) {
	z := New("nvme")
	if got := z.DevicePath("nvme/default/abc"); got != "/dev/zvol/nvme/default/abc" {
		t.Fatalf("device path = %q", got)
	}
}

func TestExistsFalseForBogus(t *testing.T) {
	z := New("nvme")
	if z.Exists("nvme/definitely-not-a-real-zvol-xyz") {
		t.Fatal("expected Exists to be false")
	}
}

func TestFreeBytesParses(t *testing.T) {
	z := New("nvme")
	free, err := z.FreeBytes()
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if free <= 0 {
		t.Fatalf("expected positive free space, got %d", free)
	}
}
