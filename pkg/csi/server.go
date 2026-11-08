//go:build linux

package csi

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/timpa0130/nvmeof-csi/pkg/ids"
	"github.com/timpa0130/nvmeof-csi/pkg/nvmet"
	"github.com/timpa0130/nvmeof-csi/pkg/zfs"
	"google.golang.org/grpc"
)

// Plugin identity.
const (
	PluginName    = "com.timpa0130.nvmeof-csi"
	PluginVersion = "1.0.0"
	DefaultPort   = "4420"
	// DefaultNVMePort is the configfs nvmet port number used by the single
	// cluster layout; a second cluster on the same target host needs another.
	DefaultNVMePort = "1"
)

// deviceWait bounds the wait for udev to create a zvol's device node.
const deviceWait = 5 * time.Second

// Mode selects which CSI services a process serves.
type Mode string

const (
	ModeAll        Mode = "all"
	ModeController Mode = "controller"
	ModeNode       Mode = "node"
)

// Config configures the driver.
type Config struct {
	SocketPath string
	// Listen is an optional TCP address (host:port) to also serve the
	// CSI gRPC API on, for sidecars running on other hosts (e.g. an
	// external-provisioner dialing a controller that lives off-cluster).
	// Empty disables TCP serving.
	Listen     string
	TargetAddr string
	Port       string
	Pool       string
	// Cluster separates this cluster's zvols, subsystems and host grants from
	// those of other clusters sharing the same pool and target host. It lands
	// in the dataset path and the NQN authority (see ids.Scope). Empty keeps
	// the original single-cluster names.
	Cluster string
	// NVMePort is the nvmet configfs port number (ports/<NVMePort>) this
	// process serves. Processes sharing one nvmet instance must differ; the
	// kernel numbers ports, so it has to be numeric.
	NVMePort string
	NodeID   string
	Mode     Mode
}

// zvolStore is the ZFS side of the driver; tests replace it.
type zvolStore interface {
	CreateVolume(dataset string, sizeBytes int64) error
	DestroyVolume(dataset string) error
	DevicePath(dataset string) string
	Exists(dataset string) bool
	VolSize(dataset string) (int64, error)
	Volumes() ([]string, error)
	FreeBytes() (int64, error)
	WaitForDevice(dataset string, timeout time.Duration) error
	Holder(dataset string) (string, error)
	SetHolder(dataset, nodeID string) error
	ClearHolder(dataset string) error
}

// Driver wires the CSI servers to the backing subsystems. It holds no state
// of its own: names are derived from the volume ID, and the one fact that
// cannot be derived, which node holds a volume, is a property on its zvol.
type Driver struct {
	cfg    Config
	target *nvmet.Target
	zfs    zvolStore
}

// scope is the naming identity of this process: its pool and cluster. It is
// derived from cfg so a Driver built anywhere names exactly what cfg says.
func (d *Driver) scope() ids.Scope {
	return ids.Scope{Pool: d.cfg.Pool, Cluster: d.cfg.Cluster}
}

// NewDriver builds a Driver.
func NewDriver(cfg Config) (*Driver, error) {
	if cfg.Mode == "" {
		cfg.Mode = ModeAll
	}
	switch cfg.Mode {
	case ModeAll, ModeController, ModeNode:
	default:
		return nil, fmt.Errorf("invalid mode %q (all|controller|node)", cfg.Mode)
	}
	if cfg.NVMePort == "" {
		cfg.NVMePort = DefaultNVMePort
	}
	if _, err := strconv.ParseUint(cfg.NVMePort, 10, 16); err != nil {
		return nil, fmt.Errorf("invalid nvme port %q: configfs port numbers are 0-65535", cfg.NVMePort)
	}
	if !ids.ValidCluster(cfg.Cluster) {
		return nil, fmt.Errorf("invalid cluster %q: must not contain '/', '.' or ':', and must not be \"host\"", cfg.Cluster)
	}
	scope := ids.Scope{Pool: cfg.Pool, Cluster: cfg.Cluster}
	d := &Driver{
		cfg:    cfg,
		target: nvmet.New(cfg.NVMePort, ""),
		zfs:    zfs.New(cfg.Pool, scope.DatasetRoot()),
	}
	return d, nil
}

// EnsurePort creates the shared TCP target port.
func (d *Driver) EnsurePort() error {
	return d.target.EnsurePort(d.cfg.TargetAddr, d.cfg.Port)
}

// exportVolume builds the nvmet export of an existing zvol. Idempotent;
// a live export keeps its connections and grants.
func (d *Driver) exportVolume(volumeID string) error {
	dataset := d.scope().Dataset(ids.SplitVolumeID(volumeID))
	if err := d.zfs.WaitForDevice(dataset, deviceWait); err != nil {
		return err
	}
	return d.target.ExportVolume(d.scope().NQN(volumeID), d.zfs.DevicePath(dataset), d.scope().NamespaceUUID(volumeID))
}

// restoreVolume rebuilds a zvol's export without its port link and admits its
// recorded holder.
func (d *Driver) restoreVolume(volumeID string) error {
	scope := d.scope()
	dataset := scope.Dataset(ids.SplitVolumeID(volumeID))
	if err := d.zfs.WaitForDevice(dataset, deviceWait); err != nil {
		return err
	}
	nqn := scope.NQN(volumeID)
	if err := d.target.PrepareVolume(nqn, d.zfs.DevicePath(dataset), scope.NamespaceUUID(volumeID)); err != nil {
		return err
	}
	holder, err := d.zfs.Holder(dataset)
	if err != nil || holder == "" {
		return err
	}
	return d.target.GrantHost(nqn, scope.HostNQN(holder))
}

// Reconcile makes configfs match this cluster's dataset root. configfs is
// in-memory, so it tears down subsystems that carry our NQN root but whose
// zvol no longer exists, and rebuilds every zvol's export and grant after a
// reboot of this host emptied it. Foreign subsystems and datasets, other
// clusters' included, are left alone.
//
// Port links come last and together. An initiator refused with do-not-retry
// removes its controller for good, which would cost a running pod its volume.
// With no link the port is closed, and a refused TCP connection is retried.
//
// A zvol that cannot be restored is logged, not fatal: exiting would keep
// every other volume down. ControllerPublishVolume retries the export.
func (d *Driver) Reconcile() error {
	subs, err := d.target.ListSubsystems()
	if err != nil {
		return err
	}
	for _, nqn := range subs {
		volumeID, ok := d.scope().ParseNQN(nqn)
		if !ok {
			continue // not ours, another cluster's included
		}
		dataset := d.scope().Dataset(ids.SplitVolumeID(volumeID))
		if d.zfs.Exists(dataset) {
			continue
		}
		log.Printf("reconcile: tearing down orphan subsystem %s", nqn)
		if err := d.target.TeardownVolume(nqn); err != nil {
			return fmt.Errorf("teardown orphan %s: %w", nqn, err)
		}
	}

	datasets, err := d.zfs.Volumes()
	if err != nil {
		return err
	}
	var restored []string
	for _, dataset := range datasets {
		volumeID, ok := d.scope().ParseDataset(dataset)
		if !ok {
			continue // not ours
		}
		if err := d.restoreVolume(volumeID); err != nil {
			log.Printf("reconcile: restore %s: %v", volumeID, err)
			continue
		}
		restored = append(restored, d.scope().NQN(volumeID))
	}
	for _, nqn := range restored {
		if err := d.target.LinkSubsystemToPort(nqn); err != nil {
			log.Printf("reconcile: link %s: %v", nqn, err)
		}
	}
	return nil
}

// Run serves the CSI gRPC API over the Unix socket until ctx is done.
// When cfg.Listen is set, it additionally serves the same API over TCP
// (same server, same registration) for remote sidecars.
func (d *Driver) Run(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(d.cfg.SocketPath), 0o755); err != nil {
		return fmt.Errorf("mkdir socket dir: %w", err)
	}
	_ = os.Remove(d.cfg.SocketPath)
	lis, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", d.cfg.SocketPath, err)
	}
	if err := os.Chmod(d.cfg.SocketPath, 0o770); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	srv := grpc.NewServer()
	csi.RegisterIdentityServer(srv, &identityServer{d: d})
	switch d.cfg.Mode {
	case ModeNode:
		csi.RegisterNodeServer(srv, &nodeServer{d: d})
	case ModeController:
		csi.RegisterControllerServer(srv, &controllerServer{d: d})
	case ModeAll:
		csi.RegisterControllerServer(srv, &controllerServer{d: d})
		csi.RegisterNodeServer(srv, &nodeServer{d: d})
	}

	// Serve the unix listener; the TCP listener (if any) is served by the
	// same server in a goroutine. One GracefulStop covers both.
	errCh := make(chan error, 2)
	go func() { errCh <- srv.Serve(lis) }()
	if d.cfg.Listen != "" {
		tcpLis, err := net.Listen("tcp", d.cfg.Listen)
		if err != nil {
			srv.Stop()
			return fmt.Errorf("listen tcp %s: %w", d.cfg.Listen, err)
		}
		go func() { errCh <- srv.Serve(tcpLis) }()
	}

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()

	log.Printf("serving CSI (%s mode) on %s", d.cfg.Mode, d.cfg.SocketPath)
	if d.cfg.Listen != "" {
		log.Printf("serving CSI (%s mode) on tcp %s", d.cfg.Mode, d.cfg.Listen)
	}
	if err := <-errCh; err != nil {
		srv.Stop()
		return err
	}
	return nil
}
