// Package platform contains the non-privileged host admission checks.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Inspector interface{ Check() error }
type HostInspector struct{}

func (HostInspector) Check() error {
	if os.Geteuid() == 0 || os.Getenv("SUDO_USER") != "" {
		return fmt.Errorf("ncp must run as an unprivileged user")
	}
	if runtime.GOARCH != "amd64" {
		return fmt.Errorf("NixCP requires x86_64-linux")
	}
	b, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains(string(b), "ID=nixos") {
		return fmt.Errorf("NixCP requires NixOS")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return fmt.Errorf("NixCP requires systemd: %w", err)
	}
	for _, name := range []string{"nix", "nixos-rebuild", "systemctl", "flock"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("required tool %q is unavailable", name)
		}
	}
	return nil
}
