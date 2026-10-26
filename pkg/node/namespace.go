//go:build linux

package node

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Namespace is the initiator's view of a subsystem's namespace (v1: one
// per subsystem). With native multipath it has a head device, which
// userspace opens, and one path device per controller, which carries the
// I/O on the wire. Their names come from independent instance counters
// (head nvme<subsys>n1, path nvme<subsys>c<ctrl>n1), so they are
// discovered from sysfs by subsystem NQN, never derived from each other.
type Namespace struct {
	// Head is the device node mkfs/mount/bind-mount use, e.g. /dev/nvme1n1.
	Head string

	sysfs   string
	devices []string // sysfs names of the head and every path device
}

const sysfsBlock = "/sys/block"

// maxSectorsKB caps the I/O size of every namespace device (see CapIO).
const maxSectorsKB = 1024

// LookupNamespace finds the namespace of the subsystem nqn: every
// /sys/block entry whose device/subsysnqn matches. The head is the entry
// without a controller ("c") segment; without multipath it is the only
// entry and carries the I/O itself.
func LookupNamespace(nqn string) (*Namespace, error) {
	return lookupNamespace(sysfsBlock, nqn)
}

func lookupNamespace(sysfs, nqn string) (*Namespace, error) {
	entries, err := os.ReadDir(sysfs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sysfs, err)
	}
	ns := &Namespace{sysfs: sysfs}
	for _, e := range entries {
		b, err := os.ReadFile(sysfs + "/" + e.Name() + "/device/subsysnqn")
		if err != nil || strings.TrimSpace(string(b)) != nqn {
			continue
		}
		ns.devices = append(ns.devices, e.Name())
		if !strings.Contains(e.Name(), "c") {
			ns.Head = "/dev/" + e.Name()
		}
	}
	if ns.Head == "" {
		return nil, fmt.Errorf("no namespace device found for %s", nqn)
	}
	return ns, nil
}

// CapIO lowers max_sectors_kb to 1 MiB on every namespace device. The
// target advertises mdts=0 (unlimited), so the initiator merges writeback
// into multi-MiB commands; the target kernel rejects any command above
// its 4 MiB H2CData bound with Data SGL Length Invalid
// (NVME_SC_SGL_INVALID_DATA, status 0x0f, DNR) — verified at the boundary:
// a 4 MiB write succeeds, 4 MiB + 4 KiB fails. Any cap ≤ 4 MiB works;
// 1 MiB leaves 4× margin.
//
// Every device is capped because the path queues merge the head's bios
// back up to their own limit. Only lowers: a device already at or below
// the cap is left untouched, so a smaller future bound is never fought.
func (ns *Namespace) CapIO() error {
	for _, dev := range ns.devices {
		path := ns.sysfs + "/" + dev + "/queue/max_sectors_kb"
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		cur, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if cur <= maxSectorsKB {
			continue
		}
		if err := os.WriteFile(path, []byte(strconv.Itoa(maxSectorsKB)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
