//go:build linux

package nvmet

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNvmetLifecycle exercises the full create/link/teardown flow
// against a scratch configfs tree.
func TestNvmetLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nvmet")
	os.MkdirAll(filepath.Join(root, "ports"), 0o755)
	os.MkdirAll(filepath.Join(root, "subsystems"), 0o755)

	tgt := New(root)
	nqn := "nqn.2014-08.org.nvmexpress:uuid:test-1"

	if err := tgt.EnsurePort("192.168.1.86", "4420"); err != nil {
		t.Fatalf("EnsurePort: %v", err)
	}

	// Idempotent create.
	for i := 0; i < 2; i++ {
		if err := tgt.CreateSubsystem(nqn); err != nil {
			t.Fatalf("CreateSubsystem: %v", err)
		}
		if err := tgt.CreateNamespace(nqn, 1, "/dev/zvol/nvme/test-1"); err != nil {
			t.Fatalf("CreateNamespace: %v", err)
		}
		if err := tgt.LinkSubsystemToPort(nqn); err != nil {
			t.Fatalf("LinkSubsystemToPort: %v", err)
		}
	}

	// Verify the symlink target and port attrs.
	link := filepath.Join(root, "ports", "1", "subsystems", nqn)
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("expected port link: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "ports", "1", "addr_traddr"))
	if string(got) != "192.168.1.86\n" {
		t.Fatalf("traddr = %q", got)
	}
	enable, _ := os.ReadFile(filepath.Join(root, "subsystems", nqn, "namespaces", "1", "enable"))
	if string(enable) != "1\n" {
		t.Fatalf("enable = %q", enable)
	}

	// Tolerant teardown, then idempotent again.
	if err := tgt.TeardownVolume(nqn); err != nil {
		t.Fatalf("TeardownVolume: %v", err)
	}
	if err := tgt.TeardownVolume(nqn); err != nil {
		t.Fatalf("TeardownVolume (2nd): %v", err)
	}

	subs, err := tgt.ListSubsystems()
	if err != nil {
		t.Fatalf("ListSubsystems: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected no subsystems, got %v", subs)
	}
}

func TestTeardownPartialState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nvmet")
	os.MkdirAll(filepath.Join(root, "ports"), 0o755)
	os.MkdirAll(filepath.Join(root, "subsystems"), 0o755)

	tgt := New(root)
	// Create a subsystem but no namespace at all.
	if err := tgt.CreateSubsystem("nqn.2014-08.org.nvmexpress:uuid:partial"); err != nil {
		t.Fatalf("CreateSubsystem: %v", err)
	}
	if err := tgt.TeardownVolume("nqn.2014-08.org.nvmexpress:uuid:partial"); err != nil {
		t.Fatalf("TeardownVolume on partial state: %v", err)
	}
}

func TestListSubsystemsMissingRoot(t *testing.T) {
	tgt := New(filepath.Join(t.TempDir(), "no", "nvmet"))
	subs, err := tgt.ListSubsystems()
	if err != nil {
		t.Fatalf("ListSubsystems on missing root: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected empty, got %v", subs)
	}
}
