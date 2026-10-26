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
	resp, err := csi.NewIdentityClient(conn).Probe(callCtx, &csi.ProbeRequest{})
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