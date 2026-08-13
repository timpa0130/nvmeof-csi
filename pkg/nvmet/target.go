//go:build linux

package nvmet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Configfs root where nvmet exposes its tree.
const (
	configfsRoot = "/sys/kernel/config/nvmet"
	portID       = "1"
	nsID         = "1"
)

// Target manipulates the in-memory nvmet configfs tree.
type Target struct {
	root string
}

// New returns a Target rooted at the nvmet configfs dir. A non-empty
// override is used by tests to point at a scratch dir.
func New(root ...string) *Target {
	r := configfsRoot
	if len(root) > 0 && root[0] != "" {
		r = root[0]
	}
	return &Target{root: r}
}

// Available reports whether the nvmet configfs tree exists (i.e. the
// nvmet kernel modules are loaded and configfs is mounted).
func Available() bool {
	st, err := os.Stat(configfsRoot)
	return err == nil && st.IsDir()
}

// EnsurePort creates the shared TCP port and (re)writes its address
// attributes. Idempotent: attributes are only written when unset, since
// configfs refuses to rewrite them once the port is active.
func (t *Target) EnsurePort(traddr, trsvcid string) error {
	portDir := filepath.Join(t.root, "ports", portID)
	if err := os.MkdirAll(portDir, 0o755); err != nil {
		return fmt.Errorf("mkdir port: %w", err)
	}
	attrs := map[string]string{
		"addr_traddr":  traddr,
		"addr_trsvcid": trsvcid,
		"addr_trtype":  "tcp",
		"addr_adrfam":  "ipv4",
	}
	for name, want := range attrs {
		path := filepath.Join(portDir, name)
		cur, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if errors.Is(err, os.ErrNotExist) || strings.TrimSpace(string(cur)) == "" {
			if err := writeFile(path, want+"\n"); err != nil {
				return fmt.Errorf("write %s: %w", name, err)
			}
			continue
		}
		if strings.TrimSpace(string(cur)) == want {
			continue
		}
		return fmt.Errorf("port %s already configured to %q, cannot change to %q", name, strings.TrimSpace(string(cur)), want)
	}
	return nil
}

// CreateSubsystem creates subsystems/<nqn> and allows any host.
// Idempotent.
func (t *Target) CreateSubsystem(nqn string) error {
	subDir := filepath.Join(t.root, "subsystems", nqn)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return fmt.Errorf("mkdir subsystem: %w", err)
	}
	if err := writeFile(filepath.Join(subDir, "attr_allow_any_host"), "1\n"); err != nil {
		return fmt.Errorf("write attr_allow_any_host: %w", err)
	}
	return nil
}

// CreateNamespace creates subsystems/<nqn>/namespaces/<nsid>, points it
// at devicePath and enables it. Idempotent.
func (t *Target) CreateNamespace(nqn string, nsid int32, devicePath string) error {
	nsDir := filepath.Join(t.root, "subsystems", nqn, "namespaces", fmt.Sprintf("%d", nsid))
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir namespace: %w", err)
	}
	if err := writeFile(filepath.Join(nsDir, "device_path"), devicePath+"\n"); err != nil {
		return fmt.Errorf("write device_path: %w", err)
	}
	if err := writeFile(filepath.Join(nsDir, "enable"), "1\n"); err != nil {
		return fmt.Errorf("write enable: %w", err)
	}
	return nil
}

// LinkSubsystemToPort symlinks ports/<portID>/subsystems/<nqn> to the
// subsystem. Idempotent.
func (t *Target) LinkSubsystemToPort(nqn string) error {
	portSubs := filepath.Join(t.root, "ports", portID, "subsystems")
	if err := os.MkdirAll(portSubs, 0o755); err != nil {
		return fmt.Errorf("mkdir port subsystems: %w", err)
	}
	link := filepath.Join(portSubs, nqn)
	if _, err := os.Lstat(link); err == nil {
		return nil
	}
	// configfs only accepts an absolute symlink target; the kernel
	// rewrites it relative to the configfs root on read.
	target := filepath.Join(t.root, "subsystems", nqn)
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("link subsystem to port: %w", err)
	}
	return nil
}

// TeardownVolume removes the subsystem for nqn from the shared port and
// deletes it. Idempotent and tolerant of a partially-created tree.
func (t *Target) TeardownVolume(nqn string) error {
	// 1. Unlink the subsystem from every port.
	portsDir := filepath.Join(t.root, "ports")
	entries, err := os.ReadDir(portsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			link := filepath.Join(portsDir, e.Name(), "subsystems", nqn)
			if _, lerr := os.Lstat(link); lerr == nil {
				if rerr := os.Remove(link); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
					return fmt.Errorf("remove port link: %w", rerr)
				}
			}
		}
	}

	subDir := filepath.Join(t.root, "subsystems", nqn)

	// 2. Disable each namespace.
	nsDir := filepath.Join(subDir, "namespaces")
	if entries, nerr := os.ReadDir(nsDir); nerr == nil {
		for _, e := range entries {
			enablePath := filepath.Join(nsDir, e.Name(), "enable")
			// Best-effort: a missing enable file is fine.
			if data, rerr := os.ReadFile(enablePath); rerr == nil && strings.TrimSpace(string(data)) == "1" {
				_ = writeFile(enablePath, "0\n")
			}
		}
		// 3. rmdir namespaces.
		for _, e := range entries {
			_ = removeGroup(filepath.Join(nsDir, e.Name()))
		}
		// rmdir the (now empty) namespaces dir.
		_ = removeGroup(nsDir)
	}

	// 4. rmdir subsystem.
	if err := removeGroup(subDir); err != nil {
		return fmt.Errorf("rmdir subsystem: %w", err)
	}
	return nil
}

// removeGroup removes a configfs group directory. On real configfs a
// plain rmdir succeeds even with attribute files present; on regular
// filesystems the directory must first be emptied of files.
func removeGroup(dir string) error {
	err := os.Remove(dir)
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if !errors.Is(err, syscall.ENOTEMPTY) {
		return err
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return os.Remove(dir)
}

// ListSubsystems returns the NQNs currently present in configfs.
func (t *Target) ListSubsystems() ([]string, error) {
	subDir := filepath.Join(t.root, "subsystems")
	entries, err := os.ReadDir(subDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read subsystems: %w", err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
