//go:build linux

package zfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// holderProperty is the ZFS user property recording which node holds a zvol.
const holderProperty = "com.timpa0130.nvmeof-csi:holder"

// ZFS wraps the `zfs` command-line tool on behalf of one cluster. Pool is the
// ZFS pool; Root is the dataset under it that holds this cluster's zvols, and
// the dataset a per-cluster quota is set on. Root equals Pool for the
// single-cluster layout.
type ZFS struct {
	Pool string
	Root string
}

// New returns a ZFS helper for one cluster's pool and dataset root.
func New(pool, root string) *ZFS {
	return &ZFS{Pool: pool, Root: root}
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
	cmd := exec.Command("zfs", "create", "-V", fmt.Sprintf("%d", sizeBytes), "-o", "volblocksize=16K", dataset)
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

// Volumes returns the zvol dataset names under this cluster's root. A
// sibling cluster's zvols are not listed, so they are never exported, restored
// or removed by this driver.
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
		if strings.HasPrefix(line, z.Root+"/") {
			vols = append(vols, line)
		}
	}
	return vols, nil
}

// Holder returns the node ID recorded on the zvol by SetHolder, or "" when
// none is.
func (z *ZFS) Holder(dataset string) (string, error) {
	cmd := exec.Command("zfs", "get", "-H", "-o", "value", holderProperty, dataset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("zfs get holder %s: %w (%s)", dataset, err, out)
	}
	holder := strings.TrimSpace(string(out))
	if holder == "-" {
		return "", nil
	}
	return holder, nil
}

// SetHolder records the node ID that holds the zvol. Unlike nvmet configfs,
// the pool survives a reboot of this host.
func (z *ZFS) SetHolder(dataset, nodeID string) error {
	cmd := exec.Command("zfs", "set", holderProperty+"="+nodeID, dataset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs set holder %s: %w (%s)", dataset, err, out)
	}
	return nil
}

// ClearHolder removes the recorded holder. Idempotent.
func (z *ZFS) ClearHolder(dataset string) error {
	cmd := exec.Command("zfs", "inherit", holderProperty, dataset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs clear holder %s: %w (%s)", dataset, err, out)
	}
	return nil
}

// FreeBytes returns the bytes available to this cluster's volumes. It is read
// from the dataset root, so a quota set there (zfs set quota=... nvme/<cluster>)
// bounds what the cluster advertises while sharing a pool; quota, not refquota,
// because this cluster's zvols are descendants of that root. Without a quota the
// pool's own free space is reported. A root that does not exist yet, before the
// first volume, falls back to the pool.
func (z *ZFS) FreeBytes() (int64, error) {
	dataset := z.Root
	if dataset != z.Pool && !z.existsDataset(dataset) {
		dataset = z.Pool
	}
	cmd := exec.Command("zfs", "get", "-H", "-p", "-o", "value", "available", dataset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("zfs get available %s: %w (%s)", dataset, err, out)
	}
	var free int64
	if _, err := fmt.Sscanf(string(out), "%d", &free); err != nil {
		return 0, fmt.Errorf("parse available %s: %w", dataset, err)
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
