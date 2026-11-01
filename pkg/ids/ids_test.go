package ids

import (
	"strings"
	"testing"
)

func TestVolumeIDRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"ns1", "pvc-1"},
		{"default", "x"},
		{"a", "b"},
	}
	for _, c := range cases {
		id := VolumeID(c[0], c[1])
		ns, name := SplitVolumeID(id)
		if ns != c[0] || name != c[1] {
			t.Fatalf("round trip %q -> %q/%q", id, ns, name)
		}
	}
}

func TestHostIdentity(t *testing.T) {
	if got, want := HostNQN("worker-1"), NQNRoot+".host:worker-1"; got != want {
		t.Fatalf("HostNQN = %q, want %q", got, want)
	}
	if _, ok := ParseNQN(HostNQN("worker-1")); ok {
		t.Fatal("HostNQN parsed as a volume NQN")
	}
	id := HostID("worker-1")
	if id != HostID("worker-1") {
		t.Fatal("HostID is not stable")
	}
	if id == HostID("worker-2") {
		t.Fatal("HostID collides across nodes")
	}
	parts := strings.Split(id, "-")
	if len(id) != 36 || len(parts) != 5 || parts[2][0] != '8' {
		t.Fatalf("HostID not a version 8 UUID: %q", id)
	}
}

func TestDefaultNamespace(t *testing.T) {
	if id := VolumeID("", "vol-a"); id != "default/vol-a" {
		t.Fatalf("VolumeID with empty ns = %q", id)
	}
	ns, name := SplitVolumeID("vol-a")
	if ns != DefaultNamespace || name != "vol-a" {
		t.Fatalf("SplitVolumeID of bare name = %q/%q", ns, name)
	}
}

func TestNQNParse(t *testing.T) {
	id := "default/vol-a"
	nqn := NQN(id)
	want := "nqn.2026-08.io.example.nvmeof:default.vol-a"
	if nqn != want {
		t.Fatalf("NQN = %q, want %q", nqn, want)
	}
	got, ok := ParseNQN(nqn)
	if !ok || got != id {
		t.Fatalf("ParseNQN = %q ok=%v", got, ok)
	}
	// Foreign NQNs are rejected.
	if _, ok := ParseNQN("nqn.2014-08.org.nvmexpress:uuid:xyz"); ok {
		t.Fatal("expected foreign NQN to be rejected")
	}
	if _, ok := ParseNQN("garbage"); ok {
		t.Fatal("expected garbage to be rejected")
	}
	// NQN must not contain '/' (configfs subsystem names are paths).
	if n := NQN("ns/pvc"); strings.Contains(n, "/") {
		t.Fatalf("NQN contains '/': %q", n)
	}
}

func TestDottedNamesRoundTrip(t *testing.T) {
	// Names may contain dots; the first dot is always the boundary
	// because namespaces cannot contain dots.
	cases := []string{
		"librechat/test.pvc",
		"default/my.volume",
		"team-a/data.v2",
		"default/plain",
	}
	for _, id := range cases {
		nqn := NQN(id)
		if strings.Contains(nqn, "/") {
			t.Fatalf("NQN for %q contains '/': %q", id, nqn)
		}
		got, ok := ParseNQN(nqn)
		if !ok || got != id {
			t.Fatalf("round trip %q -> %q ok=%v (nqn %q)", id, got, ok, nqn)
		}
	}
}

func TestBareNameParse(t *testing.T) {
	// A bare volumeID leaf with no separator defaults to the namespace.
	nqn := NQNRoot + ":vol-a"
	got, ok := ParseNQN(nqn)
	if !ok || got != "default/vol-a" {
		t.Fatalf("ParseNQN of bare leaf = %q ok=%v", got, ok)
	}
}

func TestValidNamespaceAndName(t *testing.T) {
	if !ValidNamespace("my-ns") || !ValidNamespace("default") {
		t.Fatal("expected valid namespaces to pass")
	}
	if ValidNamespace("bad/ns") || ValidNamespace("bad.ns") {
		t.Fatal("expected slashes/dots in namespace to be invalid")
	}
	if !ValidName("my.pvc") || !ValidName("my-pvc") {
		t.Fatal("expected dotted/dashed names to be valid")
	}
	if ValidName("bad/name") {
		t.Fatal("expected slash in name to be invalid")
	}
}

func TestDataset(t *testing.T) {
	if d := Dataset("nvme", "ns1", "pvc-1"); d != "nvme/ns1/pvc-1" {
		t.Fatalf("Dataset = %q", d)
	}
	if d := Dataset("nvme", "", "vol-a"); d != "nvme/default/vol-a" {
		t.Fatalf("Dataset with empty ns = %q", d)
	}
}

func TestParseDataset(t *testing.T) {
	if id, ok := ParseDataset("nvme", Dataset("nvme", "ns1", "pvc.1")); !ok || id != "ns1/pvc.1" {
		t.Fatalf("ParseDataset = %q, %v", id, ok)
	}
	for _, foreign := range []string{"nvme/swap", "nvme/a/b/c", "nvme/a.b/c", "nvme/a/", "nvme//c", "other/a/b", "nvme-2/a/b"} {
		if id, ok := ParseDataset("nvme", foreign); ok {
			t.Fatalf("ParseDataset(%q) = %q, want not ours", foreign, id)
		}
	}
}

func TestParseHostNQN(t *testing.T) {
	if id, ok := ParseHostNQN(HostNQN("node.a:1")); !ok || id != "node.a:1" {
		t.Fatalf("ParseHostNQN = %q, %v", id, ok)
	}
	if id, ok := ParseHostNQN(NQN("default/a")); ok {
		t.Fatalf("ParseHostNQN on a volume NQN = %q", id)
	}
}
