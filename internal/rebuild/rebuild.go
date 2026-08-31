// Package rebuild contains the production adapter for NixOS generation
// builds, switches, and rollbacks.
package rebuild

import (
	"context"
	"fmt"
	"strings"

	"github.com/nixcp/nixcp/internal/execx"
)

// NixOS is the sole production adapter for privileged rebuild calls. It emits
// fixed, allowlisted argv shapes; request data is never interpreted by a shell.
type NixOS struct {
	Runner execx.Runner
	// SwitchArgs are configured by the composition root from validated rebuild
	// state, e.g. {"switch"} or {"switch", "--flake", ".#host", "--impure"}.
	SwitchArgs []string
}

func (r NixOS) CurrentGeneration(ctx context.Context) (string, error) {
	result, err := r.run(ctx, "readlink", []string{"-f", "/run/current-system"})
	if err != nil {
		return "", commandError("current generation", result, err)
	}
	generation := strings.TrimSpace(result.Stdout)
	if generation == "" || !strings.HasPrefix(generation, "/nix/store/") {
		return "", fmt.Errorf("current system generation is unsafe or unavailable")
	}
	return generation, nil
}

func (r NixOS) Build(ctx context.Context, candidate string) error {
	if candidate == "" {
		return fmt.Errorf("candidate path is required")
	}
	result, err := r.run(ctx, "nixos-rebuild", []string{"build", "-I", "nixos-config=" + candidate})
	if err != nil {
		return commandError("candidate build", result, err)
	}
	return nil
}

func (r NixOS) Switch(ctx context.Context) error {
	if len(r.SwitchArgs) == 0 || r.SwitchArgs[0] != "switch" {
		return fmt.Errorf("invalid rebuild switch configuration")
	}
	for _, arg := range r.SwitchArgs {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return fmt.Errorf("unsafe rebuild argument")
		}
	}
	result, err := r.run(ctx, "sudo", append([]string{"--", "nixos-rebuild"}, r.SwitchArgs...))
	if err != nil {
		return commandError("nixos switch", result, err)
	}
	return nil
}

func (r NixOS) Rollback(ctx context.Context, generation string) error {
	if !strings.HasPrefix(generation, "/nix/store/") || strings.ContainsAny(generation, "\x00\r\n") {
		return fmt.Errorf("unsafe rollback generation")
	}
	result, err := r.run(ctx, "sudo", []string{"--", generation + "/bin/switch-to-configuration", "switch"})
	if err != nil {
		return commandError("generation rollback", result, err)
	}
	return nil
}

func (r NixOS) run(ctx context.Context, name string, args []string) (execx.Result, error) {
	if r.Runner == nil {
		return execx.Result{}, fmt.Errorf("command runner unavailable")
	}
	return r.Runner.Run(ctx, &execx.Command{Name: name, Args: args, StdoutMax: execx.DefaultStdoutLimit, StderrMax: execx.DefaultStderrLimit})
}

func commandError(step string, result execx.Result, err error) error {
	diagnostic := strings.TrimSpace(result.Stderr)
	if diagnostic == "" {
		diagnostic = strings.TrimSpace(result.Stdout)
	}
	if diagnostic != "" {
		return fmt.Errorf("%s failed: %w: %s", step, err, diagnostic)
	}
	return fmt.Errorf("%s failed: %w", step, err)
}
