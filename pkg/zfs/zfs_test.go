//go:build linux

package zfs

import (
	"os/exec"
	"testing"
)

func TestDevicePath(t *testing.T) {
	z := New("nvme", "nvme")
	if got := z.DevicePath("nvme/default/abc"); got != "/dev/zvol/nvme/default/abc" {
		t.Fatalf("device path = %q", got)
	}
}

func TestExistsFalseForBogus(t *testing.T) {
	z := New("nvme", "nvme")
	if z.Exists("nvme/definitely-not-a-real-zvol-xyz") {
		t.Fatal("expected Exists to be false")
	}
}

func TestHolderRoundTrip(t *testing.T) {
	z := New("nvme", "nvme")
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
	z := New("nvme", "nvme")
	free, err := z.FreeBytes()
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if free <= 0 {
		t.Fatalf("expected positive free space, got %d", free)
	}
}

// TestFreeBytesHonoursClusterQuota is the per-cluster capacity contract: a
// quota on the cluster's dataset root bounds what that cluster's driver
// advertises and what it can create, while the pool itself still reports more
// than the quota.
func TestFreeBytesHonoursClusterQuota(t *testing.T) {
	const root = "nvme/zfs-test/quota"
	z := New("nvme", root)
	// The zvol creation is what brings the cluster root into existence.
	if err := z.CreateVolume(root+"/vol", 1<<20); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("zfs", "destroy", "-r", root).Run() })

	if out, err := exec.Command("zfs", "set", "quota=64M", root).CombinedOutput(); err != nil {
		t.Fatalf("zfs set quota: %v (%s)", err, out)
	}

	free, err := z.FreeBytes()
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if free <= 0 || free > 64<<20 {
		t.Fatalf("FreeBytes = %d, want within the 64 MiB quota", free)
	}
	pool, err := New("nvme", "nvme").FreeBytes()
	if err != nil {
		t.Fatalf("FreeBytes on pool: %v", err)
	}
	if pool <= free {
		t.Fatalf("pool free %d <= cluster free %d: the quota did not bound the cluster", pool, free)
	}

	// The quota is a limit, not only a number the cluster reports.
	if err := z.CreateVolume(root+"/too-big", 128<<20); err == nil {
		t.Fatal("a zvol larger than the cluster quota was created")
	}
}

// TestFreeBytesBeforeFirstVolume covers a cluster whose root dataset does not
// exist yet: it reports the pool, not an error.
func TestFreeBytesBeforeFirstVolume(t *testing.T) {
	free, err := New("nvme", "nvme/definitely-not-a-zvol-xyz").FreeBytes()
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if free <= 0 {
		t.Fatalf("expected positive free space, got %d", free)
	}
}
