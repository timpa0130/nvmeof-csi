//go:build linux

package node

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const testNQN = "nqn.test:vol-a"

// fakeSysfs builds a /sys/block tree: name -> {subsysnqn, max_sectors_kb}.
func fakeSysfs(t *testing.T, devs map[string][2]string) string {
	t.Helper()
	root := t.TempDir()
	for name, d := range devs {
		for file, val := range map[string]string{
			"device/subsysnqn":     d[0],
			"queue/max_sectors_kb": d[1],
		} {
			path := root + "/" + name + "/" + file
			if err := os.MkdirAll(path[:strings.LastIndex(path, "/")], 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(val+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestLookupNamespace(t *testing.T) {
	// Head and path numbers differ (nvme1 subsystem, controller 3), as on
	// a node where instance counters drift apart.
	root := fakeSysfs(t, map[string][2]string{
		"nvme1n1":   {testNQN, "32768"},
		"nvme1c3n1": {testNQN, "32768"},
		"nvme0n1":   {"nqn.other:x", "32768"},
		"nvme0c0n1": {"nqn.other:x", "32768"},
	})
	ns, err := lookupNamespace(root, testNQN)
	if err != nil {
		t.Fatalf("lookupNamespace: %v", err)
	}
	if ns.Head != "/dev/nvme1n1" {
		t.Fatalf("Head = %q, want /dev/nvme1n1", ns.Head)
	}
	got := append([]string(nil), ns.devices...)
	sort.Strings(got)
	if want := []string{"nvme1c3n1", "nvme1n1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("devices = %v, want %v", got, want)
	}

	if _, err := lookupNamespace(root, "nqn.missing:y"); err == nil {
		t.Fatal("lookupNamespace (unknown nqn) should fail")
	}
}

func TestCapIO(t *testing.T) {
	root := fakeSysfs(t, map[string][2]string{
		"nvme1n1":   {testNQN, "32768"},
		"nvme1c3n1": {testNQN, "512"}, // already below the cap: never raised
	})
	ns, err := lookupNamespace(root, testNQN)
	if err != nil {
		t.Fatalf("lookupNamespace: %v", err)
	}
	for i := 0; i < 2; i++ { // second pass: idempotent
		if err := ns.CapIO(); err != nil {
			t.Fatalf("CapIO pass %d: %v", i, err)
		}
	}
	for dev, want := range map[string]string{"nvme1n1": "1024", "nvme1c3n1": "512"} {
		b, err := os.ReadFile(root + "/" + dev + "/queue/max_sectors_kb")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(b)); got != want {
			t.Fatalf("%s max_sectors_kb = %s, want %s", dev, got, want)
		}
	}

	// Garbage content: loud error, not a silent skip.
	if err := os.WriteFile(root+"/nvme1n1/queue/max_sectors_kb", []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ns.CapIO(); err == nil {
		t.Fatal("CapIO (garbage) should fail")
	}
}
