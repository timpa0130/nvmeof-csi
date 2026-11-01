package ids

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Naming constants for the single-cluster stateless design. Everything
// is derived deterministically from the volumeID, so no metadata store
// is needed.
const (
	// NQNRoot is the fixed reverse-DNS prefix for this driver. It is a
	// constant because only one cluster is supported; it becomes
	// configurable if a second cluster is ever added.
	NQNRoot = "nqn.2026-08.io.example.nvmeof"
	// DefaultNamespace is used when a request carries no namespace.
	DefaultNamespace = "default"
	// MaxNQN is the NVMe NQN length limit in bytes.
	MaxNQN = 223
	// MaxDataset is the ZFS dataset name limit (MAXNAMELEN, 255 + NUL).
	MaxDataset = 255
)

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

// NQN returns the NVMe-oF subsystem NQN for a volume ID. The volume ID
// uses '/' as its namespace/name separator, but configfs subsystem
// names cannot contain '/', so it is encoded with a single '.'
// separator. This is injective because namespaces (DNS-1123 labels)
// never contain a dot, so the first dot in the leaf is always the
// namespace/name boundary; names may themselves contain dots.
func NQN(volumeID string) string {
	return NQNRoot + ":" + encodeLeaf(volumeID)
}

// ParseNQN extracts the volume ID from one of our NQNs. It returns
// ok=false for any foreign NQN.
func ParseNQN(nqn string) (volumeID string, ok bool) {
	const prefix = NQNRoot + ":"
	if !strings.HasPrefix(nqn, prefix) {
		return "", false
	}
	return decodeLeaf(strings.TrimPrefix(nqn, prefix)), true
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
// root differs from NQNRoot, so it can never equal a volume NQN.
func HostNQN(nodeID string) string {
	return NQNRoot + ".host:" + nodeID
}

// ParseHostNQN extracts the node ID from a HostNQN. It returns ok=false
// for any foreign host NQN.
func ParseHostNQN(hostNQN string) (nodeID string, ok bool) {
	return strings.CutPrefix(hostNQN, NQNRoot+".host:")
}

// HostID returns the NVMe host identifier (a UUID) paired with HostNQN.
// The kernel initiator rejects one host NQN with two host IDs, and one
// host ID with two host NQNs, so it must be stable per node.
func HostID(nodeID string) string {
	sum := sha256.Sum256([]byte(HostNQN(nodeID)))
	sum[6] = sum[6]&0x0f | 0x80 // version 8: custom
	sum[8] = sum[8]&0x3f | 0x80 // RFC 9562 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// Dataset returns the ZFS dataset for a namespace and PVC name.
func Dataset(pool, namespace, name string) string {
	return pool + "/" + VolumeID(namespace, name)
}

// ParseDataset extracts the volume ID from a dataset in the pool. It
// returns ok=false for any dataset this driver could not have named.
func ParseDataset(pool, dataset string) (volumeID string, ok bool) {
	id, found := strings.CutPrefix(dataset, pool+"/")
	if !found {
		return "", false
	}
	namespace, name, found := strings.Cut(id, "/")
	if !found || namespace == "" || name == "" || !ValidNamespace(namespace) || !ValidName(name) {
		return "", false
	}
	return id, true
}
