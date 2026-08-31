package command

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/output"
	rebuildpkg "github.com/nixcp/nixcp/internal/rebuild"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
	"github.com/spf13/cobra"
)

func newServiceCommand(runtime Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Manage allowlisted platform services"}
	for _, name := range []service.Name{service.Nginx, service.MariaDB, service.Redis} {
		cmd.AddCommand(newServiceSubcommand(runtime, name))
	}
	return cmd
}
func newServiceAliasCommand(runtime Runtime, name service.Name) *cobra.Command {
	cmd := &cobra.Command{Use: string(name), Short: string(name) + " service alias"}
	for _, action := range []string{"install", "start", "status", "stop", "restart"} {
		cmd.AddCommand(newServiceAction(runtime, name, action))
	}
	return cmd
}
func newServiceSubcommand(runtime Runtime, name service.Name) *cobra.Command {
	cmd := &cobra.Command{Use: string(name), Short: string(name) + " service"}
	for _, action := range []string{"install", "start", "status", "stop", "restart"} {
		cmd.AddCommand(newServiceAction(runtime, name, action))
	}
	return cmd
}
func newServiceAction(runtime Runtime, name service.Name, action string) *cobra.Command {
	return &cobra.Command{Use: action, Short: action + " service " + string(name), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return runService(cmd, runtime, name, action) }}
}

func runService(cmd *cobra.Command, runtime Runtime, name service.Name, action string) error {
	if os.Geteuid() == 0 {
		return apperrors.New("root_not_allowed", "NixCP service commands must run as a non-root user", "Run ncp as the configured NixCP owner; privileged steps use controlled sudo", apperrors.ExitCodePlatform)
	}
	if runtime.Services == nil {
		return apperrors.New("systemd_unavailable", "systemd adapter is not configured", "Retry on a supported NixOS host", apperrors.ExitCodeRuntime)
	}
	u, err := user.Current()
	if err != nil {
		return apperrors.New("user_lookup_failed", err.Error(), "", apperrors.ExitCodeRuntime)
	}
	home := u.HomeDir
	if runtime.StateHome != "" {
		home = runtime.StateHome
	}
	store := state.NewStore(home)
	snap, err := store.Load()
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	cfg := serviceConfig(&snap.Config, name)
	if action == "status" {
		return emitServiceStatus(cmd, runtime.Services, name, cfg)
	}
	if action == "restart" {
		if !cfg.Installed || cfg.DesiredState != "running" {
			return apperrors.New("service_not_running", string(name)+" must be installed and desired running before restart", "Run: ncp service "+string(name)+" start", apperrors.ExitCodePrecond)
		}
		actual, err := runtime.Services.Status(cmd.Context(), name)
		if err != nil {
			return systemdError(err)
		}
		if !actual.Active {
			return apperrors.New("service_not_active", string(name)+" is not active", "Run: ncp service "+string(name)+" start", apperrors.ExitCodePrecond)
		}
		if err := runtime.Services.Restart(cmd.Context(), name); err != nil {
			return systemdError(err)
		}
		return emitServiceResult(cmd, name, action, true, nil, "restarted")
	}
	changed, warning, err := mutateService(cfg, name, action, len(snap.Sites) > 0)
	if err != nil {
		return err
	}
	if !changed {
		return emitServiceResult(cmd, name, action, false, nil, "already in desired state")
	}
	module, err := runtime.Renderer.Render(snap)
	if err != nil {
		return apperrors.New("render_failed", err.Error(), "", apperrors.ExitCodeBuild)
	}
	configBytes, err := state.MarshalConfig(snap.Config)
	if err != nil {
		return apperrors.New("invalid_state", err.Error(), "", apperrors.ExitCodePrecond)
	}
	manager := runtime.Transactions
	if manager == nil {
		manager = defaultServiceTransaction(store.Root, runtime, snap.Config.Rebuild, desiredHealth{systemd: runtime.Services, name: name, running: cfg.DesiredState == "running"})
	}
	secretFiles, _ := mariaDBSecretFiles(snap)
	files := map[string][]byte{"config.yaml": configBytes, "generated/nixcp-module.nix": module}
	for k, v := range secretFiles {
		files[k] = v
	}
	result, err := manager.Apply(cmd.Context(), transaction.Request{Files: files, CandidateModule: "generated/nixcp-module.nix", Affected: []string{string(name)}})
	if err != nil {
		return transactionError(err)
	}
	warnings := []string{}
	if warning != "" {
		warnings = append(warnings, warning)
	}
	return emitServiceResult(cmd, name, action, result.Changed, warnings, result.Phase)
}
func serviceConfig(c *state.ConfigSnapshot, n service.Name) *state.ServiceConfig {
	switch n {
	case service.Nginx:
		return &c.Services.Nginx
	case service.MariaDB:
		return &c.Services.MariaDB
	default:
		return &c.Services.Redis
	}
}
func mutateService(cfg *state.ServiceConfig, n service.Name, action string, hasSites bool) (bool, string, error) {
	before := *cfg
	warning := ""
	switch action {
	case "install":
		cfg.Installed = true
		cfg.DesiredState = "running"
	case "start":
		if !cfg.Installed {
			return false, "", apperrors.New("service_not_installed", string(n)+" is not installed", "Run: ncp service "+string(n)+" install", apperrors.ExitCodePrecond)
		}
		cfg.DesiredState = "running"
	case "stop":
		if !cfg.Installed {
			return false, "", apperrors.New("service_not_installed", string(n)+" is not installed", "Run: ncp service "+string(n)+" install", apperrors.ExitCodePrecond)
		}
		cfg.DesiredState = "stopped"
		if n == service.Nginx && hasSites {
			warning = "nginx_stopped_with_enabled_sites"
		}
	default:
		return false, "", apperrors.New("invalid_action", "unsupported service action", "", apperrors.ExitCodeUsage)
	}
	return before != *cfg, warning, nil
}
func emitServiceStatus(cmd *cobra.Command, sys service.Systemd, n service.Name, desired *state.ServiceConfig) error {
	actual, err := sys.Status(cmd.Context(), n)
	if err != nil {
		return systemdError(err)
	}
	drift := desired.DesiredState == "running" != actual.Active || (desired.DesiredState == "running") != actual.Enabled
	data := map[string]any{"service": n, "desired": desired, "actual": actual, "drift": drift}
	if commandJSON(cmd) {
		return emitJSON(cmd, output.Success("service."+string(n)+".status", false, data, nil))
	}
	cmd.Printf("%s: desired=%s installed=%t actual=%t enabled=%t health=%s drift=%t\n", n, desired.DesiredState, desired.Installed, actual.Active, actual.Enabled, actual.Health, drift)
	return nil
}
func emitServiceResult(cmd *cobra.Command, n service.Name, action string, changed bool, warnings []string, phase string) error {
	data := map[string]any{"service": n, "action": action, "phase": phase}
	if commandJSON(cmd) {
		return emitJSON(cmd, output.Success("service."+string(n)+"."+action, changed, data, warnings))
	}
	cmd.Printf("%s %s: %s\n", n, action, phase)
	return nil
}

type desiredHealth struct {
	systemd service.Systemd
	name    service.Name
	running bool
}

func (h desiredHealth) Check(ctx context.Context, _ []string) error {
	a, e := h.systemd.Status(ctx, h.name)
	if e != nil {
		return e
	}
	if a.Active != h.running {
		return fmt.Errorf("%s active state does not match desired state", h.name)
	}
	return nil
}
func defaultServiceTransaction(root string, rt Runtime, rebuild state.RebuildConfig, health transaction.HealthChecker) *transaction.Manager {
	args := []string{"switch"}
	if rebuild.Mode == "flake" {
		args = append(args, "--flake", rebuild.Target)
		if rebuild.Impure {
			args = append(args, "--impure")
		}
	}
	return &transaction.Manager{Root: root, Locker: transaction.FlockLocker{Path: filepath.Join(root, "lock")}, Rebuilder: rebuildpkg.NixOS{Runner: rt.Runner, SwitchArgs: args}, Health: health}
}
func systemdError(err error) error {
	return apperrors.New("systemd_error", err.Error(), "Inspect the unit journal and retry", apperrors.ExitCodeHealth)
}
func transactionError(err error) error {
	return apperrors.New("service_transaction_failed", err.Error(), "The previous state was restored when possible", apperrors.ExitCodeHealth)
}

var _ = context.Background
