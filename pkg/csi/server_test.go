//go:build linux

package csi

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/timpa0130/nvmeof-csi/pkg/zfs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestRunServesOverTCP starts a driver with both a unix socket and a TCP
// listener, then makes an Identity Probe RPC over TCP.
func TestRunServesOverTCP(t *testing.T) {
	// Reserve a free port, then release it for the driver to bind.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	d, err := NewDriver(Config{
		SocketPath: filepath.Join(t.TempDir(), "csi.sock"),
		Listen:     addr,
		Mode:       ModeAll,
	})
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	callCtx, callCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer callCancel()
	// Run opens the TCP listener in its goroutine, so wait for it to come up.
	resp, err := csi.NewIdentityClient(conn).Probe(callCtx, &csi.ProbeRequest{}, grpc.WaitForReady(true))
	if err != nil {
		t.Fatalf("Probe over TCP: %v", err)
	}
	// Probe must succeed over TCP; readiness depends on nvmet configfs,
	// which is absent in the test environment.
	if resp.GetReady() == nil {
		t.Fatal("Probe over TCP returned no ready field")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// TestNewDriverValidatesClusterAndPort covers the startup checks behind the
// multi-cluster flags: a cluster ID that could collide with another scope's
// names, and a configfs port number the kernel could not create.
func TestNewDriverValidatesClusterAndPort(t *testing.T) {
	if _, err := NewDriver(Config{Pool: "nvme", Cluster: "c1", NVMePort: "tcp"}); err == nil {
		t.Fatal("non-numeric nvme port accepted")
	}
	if _, err := NewDriver(Config{Pool: "nvme", Cluster: "c1.host"}); err == nil {
		t.Fatal("cluster ID that collides with the host root accepted")
	}

	d, err := NewDriver(Config{Pool: "nvme", Cluster: "c1", NVMePort: "2"})
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	if d.cfg.NVMePort != "2" {
		t.Fatalf("nvme port = %q", d.cfg.NVMePort)
	}
	z, ok := d.zfs.(*zfs.ZFS)
	if !ok {
		t.Fatalf("driver store is %T", d.zfs)
	}
	if z.Root != "nvme/c1" {
		t.Fatalf("dataset root = %q, want nvme/c1", z.Root)
	}

	// Empty cluster keeps the single-cluster layout: the pool is the root.
	legacy, err := NewDriver(Config{Pool: "nvme"})
	if err != nil {
		t.Fatalf("NewDriver (legacy): %v", err)
	}
	if legacy.cfg.NVMePort != DefaultNVMePort {
		t.Fatalf("default nvme port = %q, want %q", legacy.cfg.NVMePort, DefaultNVMePort)
	}
	if lz := legacy.zfs.(*zfs.ZFS); lz.Root != "nvme" || lz.Pool != "nvme" {
		t.Fatalf("legacy root = %q, pool = %q", lz.Root, lz.Pool)
	}
}
