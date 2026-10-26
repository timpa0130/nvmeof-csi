//go:build linux

package csi

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

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
)

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
	Listen string
	TargetAddr string
	Port       string
	Pool       string
	NodeID     string
	Mode       Mode
}

// Driver wires the CSI servers to the backing subsystems. It is
// stateless: everything is derived from the volume ID.
type Driver struct {
	cfg    Config
	target *nvmet.Target
	zfs    *zfs.ZFS
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
	return &Driver{
		cfg:    cfg,
		target: nvmet.New(),
		zfs:    zfs.New(cfg.Pool),
	}, nil
}

// EnsurePort creates the shared TCP target port.
func (d *Driver) EnsurePort() error {
	return d.target.EnsurePort(d.cfg.TargetAddr, d.cfg.Port)
}

// Reconcile tears down configfs subsystems that carry our NQN root but
// whose backing zvol no longer exists. configfs is in-memory, so this
// heals stale entries across restarts. Foreign subsystems are left
// alone.
func (d *Driver) Reconcile() error {
	subs, err := d.target.ListSubsystems()
	if err != nil {
		return err
	}
	for _, nqn := range subs {
		volumeID, ok := ids.ParseNQN(nqn)
		if !ok {
			continue // not ours
		}
		ns, name := ids.SplitVolumeID(volumeID)
		dataset := ids.Dataset(d.cfg.Pool, ns, name)
		if d.zfs.Exists(dataset) {
			continue
		}
		log.Printf("reconcile: tearing down orphan subsystem %s", nqn)
		if err := d.target.TeardownVolume(nqn); err != nil {
			return fmt.Errorf("teardown orphan %s: %w", nqn, err)
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
