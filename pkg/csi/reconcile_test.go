//go:build linux

package csi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/timpa0130/nvmeof-csi/pkg/ids"
	"github.com/timpa0130/nvmeof-csi/pkg/nvmet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeZFS is a pool of zvols, by dataset, with their sizes. Datasets in
// noDevice have no device node yet.
type fakeZFS struct {
	zvolStore
	vols     map[string]int64
	noDevice map[string]bool
	holders  map[string]string
	setErr   error
	onWait   func(dataset string)
}

func (f *fakeZFS) Holder(dataset string) (string, error) { return f.holders[dataset], nil }

func (f *fakeZFS) SetHolder(dataset, nodeID string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.holders[dataset] = nodeID
	return nil
}

func (f *fakeZFS) ClearHolder(dataset string) error {
	delete(f.holders, dataset)
	return nil
}

func (f *fakeZFS) Exists(dataset string) bool { _, ok := f.vols[dataset]; return ok }

func (f *fakeZFS) DevicePath(dataset string) string { return "/dev/zvol/" + dataset }

func (f *fakeZFS) VolSize(dataset string) (int64, error) { return f.vols[dataset], nil }

func (f *fakeZFS) Volumes() ([]string, error) {
	var out []string
	for dataset := range f.vols {
		out = append(out, dataset)
	}
	slices.Sort(out)
	return out, nil
}

func (f *fakeZFS) WaitForDevice(dataset string, _ time.Duration) error {
	if f.onWait != nil {
		f.onWait(dataset)
	}
	if f.noDevice[dataset] {
		return errors.New("no device")
	}
	return nil
}

func (f *fakeZFS) DestroyVolume(dataset string) error {
	delete(f.vols, dataset)
	return nil
}

// reconcileFixture is a driver over a fake pool and a scratch configfs,
// where reboot empties configfs the way a reboot of the target host does.
type reconcileFixture struct {
	t    *testing.T
	root string
	zfs  *fakeZFS
	d    *Driver
	ctrl *controllerServer
}

func newReconcileFixture(t *testing.T, vols ...string) *reconcileFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "nvmet")
	z := &fakeZFS{vols: map[string]int64{}, noDevice: map[string]bool{}, holders: map[string]string{}}
	for _, v := range vols {
		z.vols[v] = 1 << 30
	}
	d := &Driver{cfg: Config{Pool: "nvme", TargetAddr: "192.0.2.1", Port: "4420"}, target: nvmet.New(root), zfs: z}
	return &reconcileFixture{t: t, root: root, zfs: z, d: d, ctrl: &controllerServer{d: d}}
}

func (f *reconcileFixture) start() {
	f.t.Helper()
	if err := f.d.EnsurePort(); err != nil {
		f.t.Fatalf("EnsurePort: %v", err)
	}
	if err := f.d.Reconcile(); err != nil {
		f.t.Fatalf("Reconcile: %v", err)
	}
}

func (f *reconcileFixture) reboot() {
	f.t.Helper()
	if err := os.RemoveAll(f.root); err != nil {
		f.t.Fatal(err)
	}
}

func (f *reconcileFixture) publish(volumeID, nodeID string) error {
	_, err := f.ctrl.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId: volumeID,
		NodeId:   nodeID,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
			AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
		},
	})
	return err
}

// published returns each listed volume's granted hosts, as volume ID -> node IDs.
func (f *reconcileFixture) published() map[string][]string {
	f.t.Helper()
	resp, err := f.ctrl.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
	if err != nil {
		f.t.Fatalf("ListVolumes: %v", err)
	}
	out := map[string][]string{}
	for _, e := range resp.GetEntries() {
		volumeID := e.GetVolume().GetVolumeId()
		hosts, err := f.d.target.AllowedHosts(ids.NQN(volumeID))
		if err != nil {
			f.t.Fatalf("AllowedHosts: %v", err)
		}
		var nodes []string
		for _, host := range hosts {
			nodes = append(nodes, strings.TrimPrefix(host, ids.HostNQN("")))
		}
		out[volumeID] = nodes
	}
	return out
}

func (f *reconcileFixture) namespaceUUID(volumeID string) string {
	f.t.Helper()
	got, err := os.ReadFile(filepath.Join(f.root, "subsystems", ids.NQN(volumeID), "namespaces", "1", "device_uuid"))
	if err != nil {
		f.t.Fatalf("read device_uuid: %v", err)
	}
	return string(got)
}

func (f *reconcileFixture) exported(volumeID string) bool {
	nqn := ids.NQN(volumeID)
	enable, err := os.ReadFile(filepath.Join(f.root, "subsystems", nqn, "namespaces", "1", "enable"))
	if err != nil || string(enable) != "1\n" {
		return false
	}
	_, err = os.Lstat(filepath.Join(f.root, "ports", "1", "subsystems", nqn))
	return err == nil
}

// TestReconcileAfterReboot walks a reboot of the target host: exports and the
// recorded grant come back with no publish and the namespace keeps its UUID, so
// the holder's first reconnect is accepted and it keeps its device.
func TestReconcileAfterReboot(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a", "nvme/team/b")
	f.start()
	if err := f.publish("default/a", "node-1"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	want := map[string][]string{"default/a": {"node-1"}, "team/b": nil}
	if got := f.published(); !reflect.DeepEqual(got, want) {
		t.Fatalf("published before reboot = %v", got)
	}
	uuid := f.namespaceUUID("default/a")

	f.reboot()
	f.start()

	for _, id := range []string{"default/a", "team/b"} {
		if !f.exported(id) {
			t.Fatalf("%s not re-exported", id)
		}
	}
	if got := f.namespaceUUID("default/a"); got != uuid {
		t.Fatalf("namespace UUID changed across reboot: %q -> %q", uuid, got)
	}
	if got := f.published(); !reflect.DeepEqual(got, want) {
		t.Fatalf("published after reboot = %v", got)
	}
	if err := f.publish("default/a", "node-2"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("publish to second node = %v", err)
	}
}

// TestReconcileLinksPortLast checks no host can reach any subsystem until every
// volume is rebuilt with its grant: a host refused with do-not-retry would
// remove its controller, while a closed port is retried.
func TestReconcileLinksPortLast(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a", "nvme/team/b")
	f.start()
	if err := f.publish("default/a", "node-1"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	f.reboot()

	checked := false
	f.zfs.onWait = func(dataset string) {
		if dataset != "nvme/team/b" {
			return
		}
		checked = true
		hosts, err := f.d.target.AllowedHosts(ids.NQN("default/a"))
		if err != nil || len(hosts) != 1 {
			t.Fatalf("earlier volume's grant while restoring = %v, %v", hosts, err)
		}
		links, err := os.ReadDir(filepath.Join(f.root, "ports", "1", "subsystems"))
		if err == nil && len(links) != 0 {
			t.Fatalf("port linked while restoring: %v", links)
		}
	}
	f.start()
	if !checked {
		t.Fatal("second volume never restored")
	}
}

// TestUnpublishClearsHolder checks a detached volume is not granted back by a
// reboot, and that a stale unpublish keeps the newer holder's record.
func TestUnpublishClearsHolder(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a")
	f.start()
	unpublish := func(nodeID string) {
		t.Helper()
		if _, err := f.ctrl.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{VolumeId: "default/a", NodeId: nodeID}); err != nil {
			t.Fatalf("unpublish %q: %v", nodeID, err)
		}
	}
	if err := f.publish("default/a", "node-1"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	unpublish("node-1")
	if err := f.publish("default/a", "node-2"); err != nil {
		t.Fatalf("publish to new holder: %v", err)
	}
	unpublish("node-1")
	f.reboot()
	f.start()
	want := map[string][]string{"default/a": {"node-2"}}
	if got := f.published(); !reflect.DeepEqual(got, want) {
		t.Fatalf("published after stale unpublish and reboot = %v", got)
	}

	unpublish("")
	f.reboot()
	f.start()
	want = map[string][]string{"default/a": nil}
	if got := f.published(); !reflect.DeepEqual(got, want) {
		t.Fatalf("published after unpublish all and reboot = %v", got)
	}
}

// TestPublishFailsUntilHolderRecorded: reporting attached without a record
// would lose the grant at the next reboot. A retry completes it.
func TestPublishFailsUntilHolderRecorded(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a")
	f.start()
	f.zfs.setErr = errors.New("zfs set failed")
	if err := f.publish("default/a", "node-1"); status.Code(err) != codes.Internal {
		t.Fatalf("publish without record = %v", err)
	}
	f.zfs.setErr = nil
	if err := f.publish("default/a", "node-1"); err != nil {
		t.Fatalf("publish retry: %v", err)
	}
	if f.zfs.holders["nvme/default/a"] != "node-1" {
		t.Fatalf("holder = %q", f.zfs.holders["nvme/default/a"])
	}
}

// TestReconcileKeepsLiveExports covers a controller restart without a
// reboot: grants and exports stay, so initiators keep their I/O.
func TestReconcileKeepsLiveExports(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a")
	f.start()
	if err := f.publish("default/a", "node-1"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	f.start()
	want := map[string][]string{"default/a": {"node-1"}}
	if got := f.published(); !reflect.DeepEqual(got, want) {
		t.Fatalf("published after restart = %v", got)
	}
	if !f.exported("default/a") {
		t.Fatal("export lost across restart")
	}
}

func TestReconcileTearsDownOrphan(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a", "nvme/default/gone")
	f.start()
	delete(f.zfs.vols, "nvme/default/gone")
	foreign := "nqn.2014-08.org.nvmexpress:foreign"
	if err := f.d.target.CreateSubsystem(foreign); err != nil {
		t.Fatal(err)
	}
	f.start()
	subs, err := f.d.target.ListSubsystems()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(subs)
	want := []string{foreign, ids.NQN("default/a")}
	slices.Sort(want)
	if !slices.Equal(subs, want) {
		t.Fatalf("subsystems = %v, want %v", subs, want)
	}
}

func TestReconcileSkipsForeignZvols(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a", "nvme/swap", "nvme/a/b/c")
	f.start()
	subs, err := f.d.target.ListSubsystems()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{ids.NQN("default/a")}; !slices.Equal(subs, want) {
		t.Fatalf("subsystems = %v, want %v", subs, want)
	}
	if got, want := f.published(), map[string][]string{"default/a": nil}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListVolumes = %v", got)
	}
}

// TestReconcileContinuesPastBadZvol checks that one zvol without a device
// neither stops startup nor the other exports, and that publish retries it.
func TestReconcileContinuesPastBadZvol(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/bad", "nvme/default/good")
	f.zfs.noDevice["nvme/default/bad"] = true
	f.start()
	if f.exported("default/bad") {
		t.Fatal("bad zvol exported without a device")
	}
	if !f.exported("default/good") {
		t.Fatal("good zvol not exported")
	}
	if err := f.publish("default/bad", "node-1"); status.Code(err) != codes.Internal {
		t.Fatalf("publish without device = %v", err)
	}

	delete(f.zfs.noDevice, "nvme/default/bad")
	if err := f.publish("default/bad", "node-1"); err != nil {
		t.Fatalf("publish once device appeared: %v", err)
	}
	if !f.exported("default/bad") {
		t.Fatal("publish did not export")
	}
}

// TestCreateVolumeIdempotentReexports covers a retry after an earlier
// attempt left the zvol without its export; a failure must keep the zvol.
func TestCreateVolumeIdempotentReexports(t *testing.T) {
	f := newReconcileFixture(t, "nvme/default/a")
	req := &csi.CreateVolumeRequest{
		Name:          "a",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: []*csi.VolumeCapability{{
			AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
			AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
		}},
	}

	f.zfs.noDevice["nvme/default/a"] = true
	if _, err := f.ctrl.CreateVolume(context.Background(), req); status.Code(err) != codes.Internal {
		t.Fatalf("CreateVolume without device = %v", err)
	}
	if !f.zfs.Exists("nvme/default/a") {
		t.Fatal("failed retry destroyed an existing zvol")
	}

	delete(f.zfs.noDevice, "nvme/default/a")
	if _, err := f.ctrl.CreateVolume(context.Background(), req); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if !f.exported("default/a") {
		t.Fatal("idempotent CreateVolume did not export")
	}
}
