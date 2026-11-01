//go:build linux

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

func TestHolderRoundTrip(t *testing.T) {
	z := New("nvme")
	dataset := "nvme/zfs-test/holder"
	if err := z.CreateVolume(dataset, 1<<20); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	t.Cleanup(func() { _ = z.DestroyVolume(dataset) })

	want := func(expected string) {
		t.Helper()
		got, err := z.Holder(dataset)
		if err != nil {
			t.Fatalf("Holder: %v", err)
		}
		if got != expected {
			t.Fatalf("Holder = %q, want %q", got, expected)
		}
	}
	want("")
	if err := z.SetHolder(dataset, "node-1"); err != nil {
		t.Fatalf("SetHolder: %v", err)
	}
	want("node-1")
	for i := 0; i < 2; i++ {
		if err := z.ClearHolder(dataset); err != nil {
			t.Fatalf("ClearHolder: %v", err)
		}
	}
	want("")
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
