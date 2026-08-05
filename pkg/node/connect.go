package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const deviceWaitTimeout = 10 * time.Second

// nsid of every subsystem's namespace. v1 exposes exactly one namespace
// per subsystem, always with nsid 1.
const nsid = 1

// Connect establishes an NVMe-oF/TCP connection to the subsystem nqn at
// traddr:trsvcid and waits for the controller device to appear.
// Idempotent: an existing connection is a no-op.
func Connect(ctx context.Context, nqn, traddr, trsvcid string) error {
	if _, err := FindDeviceByNQN(nqn); err == nil {
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
		if _, ferr := FindDeviceByNQN(nqn); ferr == nil {
			return nil
		}
		return fmt.Errorf("nvme connect: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return WaitForDevice(ctx, nqn)
}

// WaitForDevice polls until the controller for nqn appears.
func WaitForDevice(ctx context.Context, nqn string) error {
	deadline := time.Now().Add(deviceWaitTimeout)
	for time.Now().Before(deadline) {
		if _, err := FindDeviceByNQN(nqn); err == nil {
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
		if _, ferr := FindDeviceByNQN(nqn); ferr != nil {
			return nil
		}
		return fmt.Errorf("nvme disconnect: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// FindDeviceByNQN returns the /dev/nvmeXnY device path for the given
// subsystem NQN by scanning `nvme list-subsys -o json` for the
// controller name and `nvme list -o json` for the device path. It never
// hardcodes a device name.
func FindDeviceByNQN(nqn string) (string, error) {
	ctrl, err := controllerForNQN(nqn)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("nvme", "list", "-o", "json").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nvme list: %w", err)
	}
	var list struct {
		Devices []struct {
			DevicePath string `json:"DevicePath"`
		} `json:"Devices"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("parse nvme list: %w", err)
	}
	want := "/dev/" + ctrl + "n" + strconv.Itoa(nsid)
	for _, d := range list.Devices {
		if d.DevicePath == want {
			return d.DevicePath, nil
		}
	}
	return "", fmt.Errorf("no device %s found for %s", want, nqn)
}

// controllerForNQN returns the controller name (e.g. "nvme1") that is
// connected to the given subsystem NQN.
func controllerForNQN(nqn string) (string, error) {
	out, err := exec.Command("nvme", "list-subsys", "-o", "json").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nvme list-subsys: %w", err)
	}
	var subsys []struct {
		Subsystems []struct {
			NQN   string `json:"NQN"`
			Paths []struct {
				Name string `json:"Name"`
			} `json:"Paths"`
		} `json:"Subsystems"`
	}
	if err := json.Unmarshal(out, &subsys); err != nil {
		return "", fmt.Errorf("parse nvme list-subsys: %w", err)
	}
	for _, host := range subsys {
		for _, s := range host.Subsystems {
			if s.NQN == nqn && len(s.Paths) > 0 {
				return s.Paths[0].Name, nil
			}
		}
	}
	return "", fmt.Errorf("no controller found for %s", nqn)
}
