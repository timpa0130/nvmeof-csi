package ids

import (
	"strings"
	"testing"
)

// single is the legacy scope: no cluster ID, so every name keeps the layout
// volumes already on disk have.
var single = Scope{Pool: "nvme"}

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
	if got, want := single.HostNQN("worker-1"), NQNRoot+".host:worker-1"; got != want {
		t.Fatalf("HostNQN = %q, want %q", got, want)
	}
	if _, ok := single.ParseNQN(single.HostNQN("worker-1")); ok {
		t.Fatal("HostNQN parsed as a volume NQN")
	}
	id := single.HostID("worker-1")
	if id != single.HostID("worker-1") {
		t.Fatal("HostID is not stable")
	}
	if id == single.HostID("worker-2") {
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
	nqn := single.NQN(id)
	want := "nqn.2026-08.io.example.nvmeof:default.vol-a"
	if nqn != want {
		t.Fatalf("NQN = %q, want %q", nqn, want)
	}
	got, ok := single.ParseNQN(nqn)
	if !ok || got != id {
		t.Fatalf("ParseNQN = %q ok=%v", got, ok)
	}
	// Foreign NQNs are rejected.
	if _, ok := single.ParseNQN("nqn.2014-08.org.nvmexpress:uuid:xyz"); ok {
		t.Fatal("expected foreign NQN to be rejected")
	}
	if _, ok := single.ParseNQN("garbage"); ok {
		t.Fatal("expected garbage to be rejected")
	}
	// NQN must not contain '/' (configfs subsystem names are paths).
	if n := single.NQN("ns/pvc"); strings.Contains(n, "/") {
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
		nqn := single.NQN(id)
		if strings.Contains(nqn, "/") {
			t.Fatalf("NQN for %q contains '/': %q", id, nqn)
		}
		got, ok := single.ParseNQN(nqn)
		if !ok || got != id {
			t.Fatalf("round trip %q -> %q ok=%v (nqn %q)", id, got, ok, nqn)
		}
	}
}

func TestBareNameParse(t *testing.T) {
	// A bare volumeID leaf with no separator defaults to the namespace.
	nqn := NQNRoot + ":vol-a"
	got, ok := single.ParseNQN(nqn)
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
	if d := single.Dataset("ns1", "pvc-1"); d != "nvme/ns1/pvc-1" {
		t.Fatalf("Dataset = %q", d)
	}
	if d := single.Dataset("", "vol-a"); d != "nvme/default/vol-a" {
		t.Fatalf("Dataset with empty ns = %q", d)
	}
}

func TestParseDataset(t *testing.T) {
	if id, ok := single.ParseDataset(single.Dataset("ns1", "pvc.1")); !ok || id != "ns1/pvc.1" {
		t.Fatalf("ParseDataset = %q, %v", id, ok)
	}
	for _, foreign := range []string{"nvme/swap", "nvme/a/b/c", "nvme/a.b/c", "nvme/a/", "nvme//c", "other/a/b", "nvme-2/a/b"} {
		if id, ok := single.ParseDataset(foreign); ok {
			t.Fatalf("ParseDataset(%q) = %q, want not ours", foreign, id)
		}
	}
}

func TestNamespaceUUID(t *testing.T) {
	id := single.NamespaceUUID("default/a")
	if id != single.NamespaceUUID("default/a") {
		t.Fatal("NamespaceUUID is not stable")
	}
	if id == single.NamespaceUUID("default/b") {
		t.Fatal("NamespaceUUID collides across volumes")
	}
	if len(id) != 36 {
		t.Fatalf("NamespaceUUID not a UUID: %q", id)
	}
}

// TestSingleScopeIsLegacy is the backward-compatibility contract: an empty
// cluster ID must keep naming the volumes that already exist on a pool.
func TestSingleScopeIsLegacy(t *testing.T) {
	if got, want := single.DatasetRoot(), "nvme"; got != want {
		t.Fatalf("DatasetRoot = %q, want %q", got, want)
	}
	if got, want := single.NQN("default/vol-a"), "nqn.2026-08.io.example.nvmeof:default.vol-a"; got != want {
		t.Fatalf("NQN = %q, want %q", got, want)
	}
	if got, want := single.Dataset("default", "vol-a"), "nvme/default/vol-a"; got != want {
		t.Fatalf("Dataset = %q, want %q", got, want)
	}
	if got, want := single.HostNQN("worker-1"), "nqn.2026-08.io.example.nvmeof.host:worker-1"; got != want {
		t.Fatalf("HostNQN = %q, want %q", got, want)
	}
}

func TestValidCluster(t *testing.T) {
	for _, ok := range []string{"c1", "cluster-1", "hosts", "0"} {
		if !ValidCluster(ok) {
			t.Fatalf("ValidCluster(%q) = false, want true", ok)
		}
	}
	// '/' adds a dataset level, ':' breaks the NQN separator, '.' lets a
	// volume root collide with the host root, and "host" is the host root of
	// the single-cluster layout, so a cluster named host would name the legacy
	// initiators' NQNs.
	for _, bad := range []string{"a/b", "a:b", "a.b", "eu.west", "host"} {
		if ValidCluster(bad) {
			t.Fatalf("ValidCluster(%q) = true, want false", bad)
		}
	}
}

// TestClusterScopesAreIsolated is what keeps one cluster's controller from
// exporting, granting or tearing down a sibling cluster's objects on a shared
// pool and a shared nvmet target.
func TestClusterScopesAreIsolated(t *testing.T) {
	a := Scope{Pool: "nvme", Cluster: "cluster1"}
	b := Scope{Pool: "nvme", Cluster: "cluster2"}
	const volumeID = "default/vol-a"

	if a.NQN(volumeID) == b.NQN(volumeID) {
		t.Fatal("same NQN in two clusters")
	}
	if a.Dataset("default", "vol-a") != "nvme/cluster1/default/vol-a" {
		t.Fatalf("Dataset = %q", a.Dataset("default", "vol-a"))
	}
	if a.DatasetRoot() == b.DatasetRoot() {
		t.Fatal("same dataset root in two clusters")
	}
	if a.NamespaceUUID(volumeID) == b.NamespaceUUID(volumeID) {
		t.Fatal("same namespace UUID in two clusters")
	}
	if a.HostNQN("worker-1") == b.HostNQN("worker-1") {
		t.Fatal("same host NQN in two clusters")
	}
	if a.HostID("worker-1") == b.HostID("worker-1") {
		t.Fatal("same host ID in two clusters")
	}

	// Neither scope can read the other's names, which is what makes Reconcile
	// and ListVolumes leave a sibling cluster alone.
	if _, ok := a.ParseNQN(b.NQN(volumeID)); ok {
		t.Fatal("cluster 1 parsed cluster 2's NQN as its own")
	}
	if _, ok := b.ParseNQN(a.NQN(volumeID)); ok {
		t.Fatal("cluster 2 parsed cluster 1's NQN as its own")
	}
	if _, ok := a.ParseDataset(b.Dataset("default", "vol-a")); ok {
		t.Fatal("cluster 1 parsed cluster 2's dataset as its own")
	}
	// The single-cluster scope owns neither: it rejects both.
	if _, ok := single.ParseNQN(a.NQN(volumeID)); ok {
		t.Fatal("legacy scope parsed a cluster NQN as its own")
	}
	if _, ok := single.ParseDataset(a.Dataset("default", "vol-a")); ok {
		t.Fatal("legacy scope parsed a cluster dataset as its own")
	}

	// Round trip inside the scope.
	got, ok := a.ParseNQN(a.NQN(volumeID))
	if !ok || got != volumeID {
		t.Fatalf("ParseNQN = %q ok=%v", got, ok)
	}
	if got, ok := a.ParseDataset(a.Dataset("default", "vol-a")); !ok || got != volumeID {
		t.Fatalf("ParseDataset = %q ok=%v", got, ok)
	}
}

// TestClusterHostRootNeverCollides checks that no scope reads another scope's
// initiator NQNs as volume NQNs: the host root stays separate from every
// cluster's volume root. A cluster ID of "host" would break that separation and
// is rejected by ValidCluster.
func TestClusterHostRootNeverCollides(t *testing.T) {
	other := Scope{Pool: "nvme", Cluster: "other"}
	for _, cluster := range []string{"cluster1", "c1", "x", "0"} {
		s := Scope{Pool: "nvme", Cluster: cluster}
		host := s.HostNQN("worker-1")
		if _, ok := s.ParseNQN(host); ok {
			t.Fatalf("cluster %q: host NQN %q parsed as a volume NQN", cluster, host)
		}
		if _, ok := other.ParseNQN(host); ok && cluster != "other" {
			t.Fatalf("cluster %q's host NQN parsed by cluster other", cluster)
		}
		if _, ok := single.ParseNQN(host); ok {
			t.Fatalf("cluster %q's host NQN parsed by the legacy scope", cluster)
		}
	}
}
