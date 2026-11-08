//go:build linux

package nvmet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testUUID = "00000000-0000-8000-8000-000000000001"

// TestNvmetLifecycle exercises the full create/link/teardown flow
// against a scratch configfs tree.
func TestNvmetLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nvmet")
	os.MkdirAll(filepath.Join(root, "ports"), 0o755)
	os.MkdirAll(filepath.Join(root, "subsystems"), 0o755)

	tgt := New("1", root)
	nqn := "nqn.2014-08.org.nvmexpress:uuid:test-1"

	if err := tgt.EnsurePort("192.168.1.86", "4420"); err != nil {
		t.Fatalf("EnsurePort: %v", err)
	}

	// Idempotent create.
	for i := 0; i < 2; i++ {
		if err := tgt.CreateSubsystem(nqn); err != nil {
			t.Fatalf("CreateSubsystem: %v", err)
		}
		if err := tgt.CreateNamespace(nqn, 1, "/dev/zvol/nvme/test-1", testUUID); err != nil {
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

	tgt := New("1", root)
	// Create a subsystem but no namespace at all.
	if err := tgt.CreateSubsystem("nqn.2014-08.org.nvmexpress:uuid:partial"); err != nil {
		t.Fatalf("CreateSubsystem: %v", err)
	}
	if err := tgt.TeardownVolume("nqn.2014-08.org.nvmexpress:uuid:partial"); err != nil {
		t.Fatalf("TeardownVolume on partial state: %v", err)
	}
}

// TestHostAccess covers the single-holder rule, revoke's retry safety and
// teardown of a subsystem that still has a holder.
func TestHostAccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nvmet")
	tgt := New("1", root)
	nqn := "nqn.2014-08.org.nvmexpress:uuid:acl"
	hostA, hostB := "nqn.host:a", "nqn.host:b"

	if err := tgt.GrantHost(nqn, hostA); !errors.Is(err, ErrNoSubsystem) {
		t.Fatalf("GrantHost without subsystem = %v", err)
	}
	if err := tgt.CreateSubsystem(nqn); err != nil {
		t.Fatalf("CreateSubsystem: %v", err)
	}
	if err := tgt.LinkSubsystemToPort(nqn); err != nil {
		t.Fatalf("LinkSubsystemToPort: %v", err)
	}
	allowAny := filepath.Join(root, "subsystems", nqn, "attr_allow_any_host")
	if got, _ := os.ReadFile(allowAny); string(got) != "0\n" {
		t.Fatalf("new subsystem attr_allow_any_host = %q", got)
	}

	// A subsystem from before host access control is closed by the grant.
	if err := os.WriteFile(allowAny, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := tgt.GrantHost(nqn, hostA); err != nil {
			t.Fatalf("GrantHost: %v", err)
		}
	}
	if got, _ := os.ReadFile(allowAny); string(got) != "0\n" {
		t.Fatalf("granted subsystem attr_allow_any_host = %q", got)
	}
	if err := tgt.GrantHost(nqn, hostB); !errors.Is(err, ErrHeldByOtherHost) {
		t.Fatalf("GrantHost to second host = %v", err)
	}

	// Revoking a non-holder leaves the holder in place.
	if err := tgt.RevokeHost(nqn, hostB); err != nil {
		t.Fatalf("RevokeHost non-holder: %v", err)
	}
	if err := tgt.GrantHost(nqn, hostB); !errors.Is(err, ErrHeldByOtherHost) {
		t.Fatalf("GrantHost after non-holder revoke = %v", err)
	}

	// Revoking the holder frees the subsystem and restores the port link,
	// including on a retry after a crash left the port unlinked.
	if err := tgt.RevokeHost(nqn, hostA); err != nil {
		t.Fatalf("RevokeHost: %v", err)
	}
	link := filepath.Join(root, "ports", "1", "subsystems", nqn)
	if err := os.Remove(link); err != nil {
		t.Fatalf("expected port link: %v", err)
	}
	if err := tgt.RevokeHost(nqn, hostA); err != nil {
		t.Fatalf("RevokeHost retry: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("port link not restored: %v", err)
	}
	if err := tgt.GrantHost(nqn, hostB); err != nil {
		t.Fatalf("GrantHost after revoke: %v", err)
	}

	// An empty host revokes whoever holds it.
	if err := tgt.RevokeHost(nqn, ""); err != nil {
		t.Fatalf("RevokeHost all: %v", err)
	}
	if err := tgt.GrantHost(nqn, hostA); err != nil {
		t.Fatalf("GrantHost after revoke all: %v", err)
	}

	if err := tgt.TeardownVolume(nqn); err != nil {
		t.Fatalf("TeardownVolume with holder: %v", err)
	}
	if err := tgt.RevokeHost(nqn, hostA); err != nil {
		t.Fatalf("RevokeHost on missing subsystem: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoke on missing subsystem recreated port link: %v", err)
	}
}

// TestExportVolume covers the rebuild after configfs was emptied and the
// re-export of a live volume, which must keep its device and grant.
func TestExportVolume(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nvmet")
	tgt := New("1", root)
	nqn := "nqn.2014-08.org.nvmexpress:uuid:export"
	host := "nqn.host:a"
	nsDir := filepath.Join(root, "subsystems", nqn, "namespaces", "1")
	link := filepath.Join(root, "ports", "1", "subsystems", nqn)

	if err := tgt.ExportVolume(nqn, "/dev/zvol/nvme/a", testUUID); err != nil {
		t.Fatalf("ExportVolume: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(nsDir, "device_path")); string(got) != "/dev/zvol/nvme/a\n" {
		t.Fatalf("device_path = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(nsDir, "device_uuid")); string(got) != testUUID+"\n" {
		t.Fatalf("device_uuid = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(nsDir, "enable")); string(got) != "1\n" {
		t.Fatalf("enable = %q", got)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("expected port link: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "subsystems", nqn, "attr_allow_any_host")); string(got) != "0\n" {
		t.Fatalf("attr_allow_any_host = %q", got)
	}
	if err := tgt.GrantHost(nqn, host); err != nil {
		t.Fatalf("GrantHost: %v", err)
	}

	// configfs refuses device_path writes on an enabled namespace.
	if err := tgt.ExportVolume(nqn, "/dev/zvol/nvme/other", "other-uuid"); err != nil {
		t.Fatalf("ExportVolume live: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(nsDir, "device_path")); string(got) != "/dev/zvol/nvme/a\n" {
		t.Fatalf("live device_path rewritten: %q", got)
	}
	hosts, err := tgt.AllowedHosts(nqn)
	if err != nil {
		t.Fatalf("AllowedHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != host {
		t.Fatalf("AllowedHosts after re-export = %v", hosts)
	}

	// A partial tree is completed.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := tgt.ExportVolume(nqn, "/dev/zvol/nvme/a", testUUID); err != nil {
		t.Fatalf("ExportVolume partial: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("port link not restored: %v", err)
	}

	if hosts, err := tgt.AllowedHosts("nqn.missing"); err != nil || len(hosts) != 0 {
		t.Fatalf("AllowedHosts on missing subsystem = %v, %v", hosts, err)
	}
}

func TestListSubsystemsMissingRoot(t *testing.T) {
	tgt := New("1", filepath.Join(t.TempDir(), "no", "nvmet"))
	subs, err := tgt.ListSubsystems()
	if err != nil {
		t.Fatalf("ListSubsystems on missing root: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected empty, got %v", subs)
	}
}

// TestPortsAreIndependent covers two drivers sharing one nvmet instance, one
// per cluster: each serves its own configfs port, a subsystem is reachable
// only through its own port, and tearing one down leaves the other's link.
func TestPortsAreIndependent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nvmet")
	os.MkdirAll(filepath.Join(root, "ports"), 0o755)
	os.MkdirAll(filepath.Join(root, "subsystems"), 0o755)

	c1, c2 := New("1", root), New("2", root)
	if err := c1.EnsurePort("192.0.2.1", "4420"); err != nil {
		t.Fatalf("cluster 1 EnsurePort: %v", err)
	}
	if err := c2.EnsurePort("192.0.2.1", "4421"); err != nil {
		t.Fatalf("cluster 2 EnsurePort: %v", err)
	}

	const nqn1 = "nqn.2026-08.io.example.nvmeof.c1:default.a"
	const nqn2 = "nqn.2026-08.io.example.nvmeof.c2:default.a"
	for _, tc := range []struct {
		tgt *Target
		nqn string
	}{{c1, nqn1}, {c2, nqn2}} {
		if err := tc.tgt.CreateSubsystem(tc.nqn); err != nil {
			t.Fatalf("CreateSubsystem %s: %v", tc.nqn, err)
		}
		if err := tc.tgt.LinkSubsystemToPort(tc.nqn); err != nil {
			t.Fatalf("LinkSubsystemToPort %s: %v", tc.nqn, err)
		}
	}

	for _, want := range []struct{ dir, nqn string }{{"1", nqn1}, {"2", nqn2}} {
		entries, err := os.ReadDir(filepath.Join(root, "ports", want.dir, "subsystems"))
		if err != nil {
			t.Fatalf("read port %s: %v", want.dir, err)
		}
		if len(entries) != 1 || entries[0].Name() != want.nqn {
			t.Fatalf("port %s links %v, want just %s", want.dir, entries, want.nqn)
		}
	}

	if err := c1.TeardownVolume(nqn1); err != nil {
		t.Fatalf("TeardownVolume: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "ports", "2", "subsystems", nqn2)); err != nil {
		t.Fatalf("cluster 2's port link was removed with cluster 1's volume: %v", err)
	}

	// One port cannot be reconfigured under a running driver: that is what
	// refuses a second cluster the first cluster's port number.
	if err := c1.EnsurePort("192.0.2.1", "4421"); err == nil {
		t.Fatal("EnsurePort accepted a different trsvcid on port 1")
	}
}
