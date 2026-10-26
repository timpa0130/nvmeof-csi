//go:build linux

package node

import (
	"fmt"
	"os/exec"
	"strings"
)

// HasFilesystem reports whether dev carries a filesystem signature,
// probed via blkid.
func HasFilesystem(dev string) bool {
	out, err := exec.Command("blkid", "-o", "value", "-s", "TYPE", dev).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// MakeFilesystem creates a filesystem of the given type on dev. An
// empty fstype defaults to ext4.
func MakeFilesystem(dev, fstype string) error {
	args, err := mkfsArgs(dev, fstype)
	if err != nil {
		return err
	}
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// mkfsArgs builds the mkfs invocation for a device. ext* filesystems
// get -F (proceed on non-empty targets / non-tty); xfs gets -f. An
// empty fstype defaults to ext4.
func mkfsArgs(dev, fstype string) ([]string, error) {
	if fstype == "" {
		fstype = "ext4"
	}
	var force string
	switch {
	case strings.HasPrefix(fstype, "ext"):
		force = "-F"
	case fstype == "xfs":
		force = "-f"
	default:
		return nil, fmt.Errorf("unsupported filesystem type %q (ext2|ext3|ext4|xfs)", fstype)
	}
	return []string{"mkfs." + fstype, force, dev}, nil
}