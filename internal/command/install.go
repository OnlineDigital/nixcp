package command

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/spf13/cobra"
)

func newInstallCommand(runtime Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "install", Short: "Initialize NixCP local state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.Platform == nil {
			return apperrors.New("platform_unavailable", "platform inspector is not configured", "", apperrors.ExitCodeRuntime)
		}
		if err := runtime.Platform.Check(); err != nil {
			return apperrors.New("unsupported_platform", err.Error(), "NixCP supports unprivileged NixOS x86_64 systems with systemd", apperrors.ExitCodePlatform)
		}
		u, err := user.Current()
		if err != nil {
			return apperrors.New("user_lookup_failed", err.Error(), "", apperrors.ExitCodeRuntime)
		}
		uid, gid := 0, 0
		fmt.Sscanf(u.Uid, "%d", &uid)
		fmt.Sscanf(u.Gid, "%d", &gid)
		cfg := state.ConfigSnapshot{SchemaVersion: 1, Owner: state.Owner{Username: u.Username, UID: uid, GID: gid, Group: u.Username, Home: u.HomeDir}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{DesiredState: "stopped"}, MariaDB: state.ServiceConfig{DesiredState: "stopped"}, Redis: state.ServiceConfig{DesiredState: "stopped"}}}
		if flake, _ := cmd.Flags().GetString("flake"); flake != "" {
			cfg.Rebuild.Mode = "flake"
			cfg.Rebuild.Target = flake
		}
		cfg.Rebuild.Impure, _ = cmd.Flags().GetBool("impure")
		store := state.NewStore(u.HomeDir)
		changed := false
		if _, err := store.Load(); err != nil {
			if !os.IsNotExist(err) {
				return apperrors.New("invalid_state", err.Error(), "Repair ~/.nixcp before retrying", apperrors.ExitCodePrecond)
			}
			if err := store.Initialize(cfg); err != nil {
				return apperrors.New("state_write_failed", err.Error(), "", apperrors.ExitCodeRuntime)
			}
			changed = true
		}
		snap, err := store.Load()
		if err != nil {
			return apperrors.New("invalid_state", err.Error(), "", apperrors.ExitCodePrecond)
		}
		module, err := runtime.Renderer.Render(snap)
		if err != nil {
			return apperrors.New("render_failed", err.Error(), "", apperrors.ExitCodeBuild)
		}
		if err := writeGenerated(filepath.Join(store.Root, "generated", "nixcp-module.nix"), module); err != nil {
			return apperrors.New("state_write_failed", err.Error(), "", apperrors.ExitCodeRuntime)
		}
		payload := map[string]any{"stateDir": store.Root, "module": filepath.Join(store.Root, "generated", "nixcp-module.nix"), "import": "imports = [ " + filepath.Join(store.Root, "generated", "nixcp-module.nix") + " ];"}
		if commandJSON(cmd) {
			return emitJSON(cmd, output.Success("install", changed, payload, nil))
		}
		cmd.Printf("Add to your NixOS configuration:\n%s\n", payload["import"])
		return nil
	}}
	cmd.Flags().String("flake", "", "NixOS flake reference for import snippet")
	cmd.Flags().Bool("impure", false, "Allow impure evaluation when required")
	cmd.Flags().Bool("confirm-import", false, "Confirm an already imported module")
	return cmd
}
