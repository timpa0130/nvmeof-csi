package zfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ZFS wraps the `zfs` command-line tool.
type ZFS struct {
	Pool string
}

// New returns a ZFS helper targeting the given pool.
func New(pool string) *ZFS {
	return &ZFS{Pool: pool}
}

// CreateVolume creates a zvol at dataset with the given size in bytes
// and 4k volblocksize. Intermediate parent datasets are created with
// mountpoint=none and persist. Idempotent: an existing volume is a
// no-op.
func (z *ZFS) CreateVolume(dataset string, sizeBytes int64) error {
	if z.Exists(dataset) {
		return nil
	}
	parent := filepath.Dir(dataset)
	if !z.existsDataset(parent) {
		cmd := exec.Command("zfs", "create", "-p", "-o", "mountpoint=none", parent)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("zfs create parent %s: %w (%s)", parent, err, out)
		}
	}
	cmd := exec.Command("zfs", "create", "-V", fmt.Sprintf("%d", sizeBytes), "-o", "volblocksize=4k", dataset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs create %s: %w (%s)", dataset, err, out)
	}
	// `zfs create` returns before udev creates /dev/zvol/<dataset>;
	// nvmet enable requires the device node to exist.
	return z.WaitForDevice(dataset, 5*time.Second)
}

// DestroyVolume destroys the zvol at dataset. Idempotent: a missing
// volume is not an error. Parent datasets are left in place.
func (z *ZFS) DestroyVolume(dataset string) error {
	if !z.Exists(dataset) {
		return nil
	}
	cmd := exec.Command("zfs", "destroy", dataset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs destroy %s: %w (%s)", dataset, err, out)
	}
	return nil
}

// DevicePath returns the block device path for a zvol dataset.
func (z *ZFS) DevicePath(dataset string) string {
	return "/dev/zvol/" + dataset
}

// Exists reports whether the given dataset exists.
func (z *ZFS) Exists(dataset string) bool {
	return z.existsDataset(dataset)
}

// VolSize returns the logical size of a zvol in bytes.
func (z *ZFS) VolSize(dataset string) (int64, error) {
	cmd := exec.Command("zfs", "list", "-H", "-p", "-o", "volsize", dataset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("zfs list volsize %s: %w (%s)", dataset, err, out)
	}
	var size int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &size); err != nil {
		return 0, fmt.Errorf("parse volsize %s: %w", dataset, err)
	}
	return size, nil
}

// Volumes returns the zvol dataset names under this pool.
func (z *ZFS) Volumes() ([]string, error) {
	cmd := exec.Command("zfs", "list", "-t", "volume", "-H", "-o", "name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("zfs list volumes: %w (%s)", err, out)
	}
	var vols []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, z.Pool+"/") {
			vols = append(vols, line)
		}
	}
	return vols, nil
}

// FreeBytes returns the free space available on the pool, in bytes.
func (z *ZFS) FreeBytes() (int64, error) {
	cmd := exec.Command("zpool", "list", "-H", "-p", "-o", "free", z.Pool)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("zpool list %s: %w (%s)", z.Pool, err, out)
	}
	var free int64
	if _, err := fmt.Sscanf(string(out), "%d", &free); err != nil {
		return 0, fmt.Errorf("parse zpool free: %w", err)
	}
	return free, nil
}

// WaitForDevice polls until the zvol device node appears or times out.
func (z *ZFS) WaitForDevice(dataset string, timeout time.Duration) error {
	dev := z.DevicePath(dataset)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dev); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("device %s did not appear within %s", dev, timeout)
}

func (z *ZFS) existsDataset(dataset string) bool {
	cmd := exec.Command("zfs", "list", "-H", "-o", "name", dataset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return len(out) > 0
}
