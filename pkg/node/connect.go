//go:build linux

package node

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const deviceWaitTimeout = 10 * time.Second

// Connect establishes an NVMe-oF/TCP connection to the subsystem nqn at
// traddr:trsvcid and waits for the namespace device to appear.
// Idempotent: an existing connection is a no-op.
func Connect(ctx context.Context, nqn, traddr, trsvcid string) error {
	if _, err := LookupNamespace(nqn); err == nil {
		return nil
	}
	args := []string{"connect", "-t", "tcp", "-n", nqn, "-a", traddr}
	if trsvcid != "" {
		args = append(args, "-s", trsvcid)
	}
	out, err := exec.CommandContext(ctx, "nvme", args...).CombinedOutput()
	if err != nil {
		// nvme connect fails with EEXIST when the controller is already
		// attached; treat that as success.
		if _, ferr := LookupNamespace(nqn); ferr == nil {
			return nil
		}
		return fmt.Errorf("nvme connect: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return WaitForDevice(ctx, nqn)
}

// WaitForDevice polls until the namespace head device for nqn appears.
func WaitForDevice(ctx context.Context, nqn string) error {
	deadline := time.Now().Add(deviceWaitTimeout)
	for time.Now().Before(deadline) {
		if _, err := LookupNamespace(nqn); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for device for %s", nqn)
}

// Disconnect removes the NVMe-oF/TCP connection for nqn. Idempotent: a
// missing controller is not an error.
func Disconnect(nqn string) error {
	out, err := exec.Command("nvme", "disconnect", "-n", nqn).CombinedOutput()
	if err != nil {
		// Controller already gone -> success.
		if _, ferr := LookupNamespace(nqn); ferr != nil {
			return nil
		}
		return fmt.Errorf("nvme disconnect: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
