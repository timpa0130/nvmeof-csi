package ids

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Naming constants for the stateless design. Everything is derived
// deterministically from the volume ID inside a cluster Scope, so no
// metadata store is needed.
const (
	// NQNRoot is the fixed reverse-DNS prefix for this driver. A cluster
	// ID becomes one more DNS label under it (Scope.nqnRoot).
	NQNRoot = "nqn.2026-08.io.example.nvmeof"
	// hostLabel separates a host NQN from a volume NQN.
	hostLabel = "host"
	// DefaultNamespace is used when a request carries no namespace.
	DefaultNamespace = "default"
	// MaxNQN is the NVMe NQN length limit in bytes.
	MaxNQN = 223
	// MaxDataset is the ZFS dataset name limit (MAXNAMELEN, 255 + NUL).
	MaxDataset = 255
)

// Scope names one cluster: the ZFS pool holding its zvols, and its cluster
// ID, which separates it from other clusters that share the pool and the
// nvmet target. The cluster ID goes into both namespaces a volume name has
// to live in, the ZFS dataset path and the NQN authority, so a sibling
// cluster's volumes and subsystems can never be mistaken for ours.
//
// An empty Cluster is the original single-cluster layout: no dataset
// component, no extra NQN label, so names of existing volumes are unchanged.
type Scope struct {
	Pool    string
	Cluster string
}

// ValidCluster reports whether s can be a cluster ID. The ID is used as one
// ZFS dataset path component and one DNS label of the NQN authority at the
// same time: '/' would add a dataset level, ':' would break the NQN
// authority/leaf separator, and '.' would let a volume root collide with the
// host root ("...nvmeof.<a>.host" for a cluster named "<a>"). The ID "host"
// is reserved outright, because that label is the host root of the
// single-cluster layout.
func ValidCluster(s string) bool {
	return !strings.ContainsAny(s, "/.:") && s != hostLabel
}

// nqnRoot returns the NQN authority of this scope's volumes.
func (s Scope) nqnRoot() string {
	if s.Cluster == "" {
		return NQNRoot
	}
	return NQNRoot + "." + s.Cluster
}

// hostRoot returns the NQN authority of this scope's initiators. Because a
// cluster ID never contains a dot, the label after it is always "host", so
// this can never equal nqnRoot.
func (s Scope) hostRoot() string {
	if s.Cluster == "" {
		return NQNRoot + "." + hostLabel
	}
	return NQNRoot + "." + s.Cluster + "." + hostLabel
}

// DatasetRoot returns the dataset that holds every volume in this scope: the
// pool for the single-cluster layout, otherwise the pool plus the cluster
// component. This is where a per-cluster ZFS quota is set, and it bounds
// which datasets this driver may list, export or remove.
func (s Scope) DatasetRoot() string {
	if s.Cluster == "" {
		return s.Pool
	}
	return s.Pool + "/" + s.Cluster
}

// VolumeID returns the CSI volume ID for a namespace and PVC name.
func VolumeID(namespace, name string) string {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	return namespace + "/" + name
}

// SplitVolumeID splits a volume ID into its namespace and name.
func SplitVolumeID(id string) (namespace, name string) {
	namespace, name, found := strings.Cut(id, "/")
	if !found {
		return DefaultNamespace, id
	}
	if namespace == "" {
		namespace = DefaultNamespace
	}
	return namespace, name
}

// Dataset returns the zvol dataset for a namespace and PVC name.
func (s Scope) Dataset(namespace, name string) string {
	return s.DatasetRoot() + "/" + VolumeID(namespace, name)
}

// ParseDataset extracts the volume ID from a dataset in this scope. It
// returns ok=false for any dataset this driver could not have named,
// including a sibling cluster's.
func (s Scope) ParseDataset(dataset string) (volumeID string, ok bool) {
	id, found := strings.CutPrefix(dataset, s.DatasetRoot()+"/")
	if !found {
		return "", false
	}
	namespace, name, found := strings.Cut(id, "/")
	if !found || namespace == "" || name == "" || !ValidNamespace(namespace) || !ValidName(name) {
		return "", false
	}
	return id, true
}

// NQN returns the NVMe-oF subsystem NQN for a volume ID. The volume ID
// uses '/' as its namespace/name separator, but configfs subsystem
// names cannot contain '/', so it is encoded with a single '.'
// separator. This is injective because namespaces (DNS-1123 labels)
// never contain a dot, so the first dot in the leaf is always the
// namespace/name boundary; names may themselves contain dots.
func (s Scope) NQN(volumeID string) string {
	return s.nqnRoot() + ":" + encodeLeaf(volumeID)
}

// ParseNQN extracts the volume ID from one of this scope's NQNs. It returns
// ok=false for any foreign NQN, including a sibling cluster's.
func (s Scope) ParseNQN(nqn string) (volumeID string, ok bool) {
	leaf, found := strings.CutPrefix(nqn, s.nqnRoot()+":")
	if !found {
		return "", false
	}
	return decodeLeaf(leaf), true
}

// encodeLeaf encodes a volume ID as the NQN leaf ("namespace.name").
func encodeLeaf(volumeID string) string {
	namespace, name := SplitVolumeID(volumeID)
	return namespace + "." + name
}

// decodeLeaf decodes an NQN leaf back into a volume ID.
func decodeLeaf(leaf string) string {
	namespace, name, found := strings.Cut(leaf, ".")
	if !found {
		return DefaultNamespace + "/" + leaf
	}
	return namespace + "/" + name
}

// ValidNamespace reports whether a namespace can be used as part of a
// volume ID. Namespaces must not contain '/' or '.', which would break
// volume ID parsing and NQN round-tripping.
func ValidNamespace(s string) bool {
	return !strings.ContainsAny(s, "/.")
}

// ValidName reports whether a PVC name can be used as part of a volume
// ID. Names must not contain '/', which would break volume ID parsing.
func ValidName(s string) bool {
	return !strings.Contains(s, "/")
}

// HostNQN returns the initiator NQN for a node. The node connects with it
// and the controller grants it, each deriving it from the CSI node ID, so
// nodes need no host-specific NQN file and cloned hosts stay distinct. Its
// root differs from the volume root, so it can never equal a volume NQN, and
// it carries the cluster ID, so two clusters on one target never share a
// host's grant.
func (s Scope) HostNQN(nodeID string) string {
	return s.hostRoot() + ":" + nodeID
}

// HostID returns the NVMe host identifier (a UUID) paired with HostNQN.
// The kernel initiator rejects one host NQN with two host IDs, and one
// host ID with two host NQNs, so it must be stable per node.
func (s Scope) HostID(nodeID string) string {
	return derivedUUID(s.HostNQN(nodeID))
}

// NamespaceUUID returns the UUID of a volume's namespace. nvmet would pick a
// random one each time the namespace is rebuilt, and an initiator removes a
// namespace whose identifiers changed when it reconnects.
func (s Scope) NamespaceUUID(volumeID string) string {
	return derivedUUID(s.NQN(volumeID))
}

// derivedUUID hashes seed into a stable UUID.
func derivedUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	sum[6] = sum[6]&0x0f | 0x80 // version 8: custom
	sum[8] = sum[8]&0x3f | 0x80 // RFC 9562 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
