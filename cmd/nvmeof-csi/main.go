//go:build linux

package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/timpa0130/nvmeof-csi/pkg/csi"
)

func main() {
	var (
		socket     = flag.String("socket", "/run/csi/nvmeof-csi.sock", "Unix socket path for the CSI gRPC endpoint")
		listen     = flag.String("listen", "", "optional TCP address (host:port) to also serve gRPC on, for remote sidecars")
		targetAddr = flag.String("target-addr", "", "IP address initiators connect to (default: first non-loopback IPv4)")
		port       = flag.String("port", csi.DefaultPort, "NVMe-oF/TCP port (trsvcid)")
		pool       = flag.String("pool", "nvme", "ZFS pool for zvols")
		mode       = flag.String("mode", "all", "services to serve: all|controller|node")
		nodeID     = flag.String("node-id", "", "K8s node ID for NodeGetInfo (default: os hostname)")
	)
	flag.Parse()

	if *targetAddr == "" {
		ip, err := defaultIPv4()
		if err != nil {
			log.Fatalf("detect target address: %v", err)
		}
		*targetAddr = ip
	}

	nodeIDValue := *nodeID
	if nodeIDValue == "" {
		host, herr := os.Hostname()
		if herr != nil {
			log.Fatalf("hostname: %v", herr)
		}
		nodeIDValue = host
	}

	driver, err := csi.NewDriver(csi.Config{
		SocketPath: *socket,
		Listen:     *listen,
		TargetAddr: *targetAddr,
		Port:       *port,
		Pool:       *pool,
		NodeID:     nodeIDValue,
		Mode:       csi.Mode(*mode),
	})
	if err != nil {
		log.Fatalf("init driver: %v", err)
	}

	// Node-only processes on worker hosts have no target side.
	if csi.Mode(*mode) != csi.ModeNode {
		if err := driver.EnsurePort(); err != nil {
			log.Fatalf("ensure port: %v", err)
		}
		if err := driver.Reconcile(); err != nil {
			log.Fatalf("reconcile: %v", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := driver.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func defaultIPv4() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String(), nil
			}
		}
	}
	return "", os.ErrInvalid
}
