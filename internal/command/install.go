package command

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/spf13/cobra"
)

const (
	laravelTemplate   = "# NixCP Laravel location template\ntry_files $uri $uri/ /index.php?$query_string;\n"
	wordpressTemplate = "# NixCP WordPress location template\ntry_files $uri $uri/ /index.php?$args;\n"
	bashIntegration   = "# Source manually: source ~/.nixcp/shell/bash.sh\n"
	zshIntegration    = "# Source manually: source ~/.nixcp/shell/zsh.sh\n"
	fishIntegration   = "# Source manually: source ~/.nixcp/shell/fish.fish\n"
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
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return apperrors.New("user_lookup_failed", "invalid current UID", "", apperrors.ExitCodeRuntime)
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return apperrors.New("user_lookup_failed", "invalid current GID", "", apperrors.ExitCodeRuntime)
		}
		group := u.Username
		if g, groupErr := user.LookupGroupId(u.Gid); groupErr == nil {
			group = g.Name
		}
		flake, _ := cmd.Flags().GetString("flake")
		impure, _ := cmd.Flags().GetBool("impure")
		cfg := initialConfig(u, uid, gid, group, flake, impure)
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
		if err := installStaticArtifacts(store); err != nil {
			return apperrors.New("state_write_failed", err.Error(), "", apperrors.ExitCodeRuntime)
		}
		module, err := runtime.Renderer.Render(snap)
		if err != nil {
			return apperrors.New("render_failed", err.Error(), "", apperrors.ExitCodeBuild)
		}
		modulePath := filepath.Join(store.Root, "generated", "nixcp-module.nix")
		if err := writeGenerated(modulePath, module); err != nil {
			return apperrors.New("state_write_failed", err.Error(), "", apperrors.ExitCodeRuntime)
		}

		confirmed, _ := cmd.Flags().GetBool("confirm-import")
		if confirmed {
			if err := confirmImport(cmd, runtime.Runner, snap.Config.Rebuild); err != nil {
				return apperrors.New("import_not_confirmed", err.Error(), "Import the generated module manually, then retry --confirm-import", apperrors.ExitCodeBuild)
			}
			if !snap.Config.Rebuild.ImportConfirmed {
				snap.Config.Rebuild.ImportConfirmed = true
				if err := store.WriteSnapshot(snap); err != nil {
					return apperrors.New("state_write_failed", err.Error(), "", apperrors.ExitCodeRuntime)
				}
				changed = true
			}
		}
		payload := installPayload(store.Root, modulePath, snap.Config.Rebuild)
		payload["importConfirmed"] = confirmed || snap.Config.Rebuild.ImportConfirmed
		if commandJSON(cmd) {
			return emitJSON(cmd, output.Success("install", changed, payload, nil))
		}
		cmd.Printf("NixCP initialized at %s\n%s\n%s\n", store.Root, payload["importInstruction"], payload["shellInstruction"])
		return nil
	}}
	cmd.Flags().String("flake", "", "NixOS flake reference for future rebuilds")
	cmd.Flags().Bool("impure", false, "Allow impure flake evaluation for a manually chosen absolute import")
	cmd.Flags().Bool("confirm-import", false, "Evaluate the manually imported module and record confirmation")
	return cmd
}

func initialConfig(u *user.User, uid, gid int, group, flake string, impure bool) state.ConfigSnapshot {
	cfg := state.ConfigSnapshot{SchemaVersion: 1, Owner: state.Owner{Username: u.Username, UID: uid, GID: gid, Group: group, Home: u.HomeDir}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{DesiredState: "stopped"}, MariaDB: state.ServiceConfig{DesiredState: "stopped"}, Redis: state.ServiceConfig{DesiredState: "stopped"}}}
	if flake != "" {
		cfg.Rebuild.Mode, cfg.Rebuild.Target, cfg.Rebuild.Impure = "flake", flake, impure
	}
	return cfg
}

func installStaticArtifacts(store *state.Store) error {
	artifacts := map[string][]byte{
		filepath.Join(store.Root, "nginx-templates", "laravel.conf"):   []byte(laravelTemplate),
		filepath.Join(store.Root, "nginx-templates", "wordpress.conf"): []byte(wordpressTemplate),
		filepath.Join(store.Root, "shell", "bash.sh"):                  []byte(bashIntegration),
		filepath.Join(store.Root, "shell", "zsh.sh"):                   []byte(zshIntegration),
		filepath.Join(store.Root, "shell", "fish.fish"):                []byte(fishIntegration),
	}
	for path, contents := range artifacts {
		if err := writeGenerated(path, contents); err != nil {
			return err
		}
	}
	return nil
}

func installPayload(root, module string, rebuild state.RebuildConfig) map[string]any {
	importLine := "imports = [ " + nixPath(module) + " ];"
	instruction := "In /etc/nixos/configuration.nix, add:\n" + importLine + "\nThen run: ncp install --confirm-import"
	if rebuild.Mode == "flake" {
		instruction = "In the NixOS module for " + rebuild.Target + ", add:\n" + importLine + "\nAbsolute paths outside a flake need --impure; a path tracked in the flake is the purer alternative. NixCP does not edit the flake.\nThen run: ncp install --confirm-import"
	}
	return map[string]any{"stateDir": root, "module": module, "import": importLine, "importInstruction": instruction, "shellInstruction": "Optionally source one generated shell file manually (for example: source " + filepath.Join(root, "shell/bash.sh") + "). NixCP does not edit shell startup files."}
}

func nixPath(path string) string {
	return "\"" + strings.ReplaceAll(strings.ReplaceAll(path, "\\", "\\\\"), "\"", "\\\"") + "\""
}

func confirmImport(cmd *cobra.Command, runner execx.Runner, rebuild state.RebuildConfig) error {
	if runner == nil {
		return fmt.Errorf("command runner is unavailable")
	}
	args := []string{"build"}
	if rebuild.Mode == "flake" {
		args = append(args, "--flake", rebuild.Target)
		if rebuild.Impure {
			args = append(args, "--impure")
		}
	}
	result, err := runner.Run(cmd.Context(), &execx.Command{Name: "nixos-rebuild", Args: args})
	if err != nil {
		return fmt.Errorf("nixos-rebuild %s failed: %s", strings.Join(args, " "), strings.TrimSpace(result.Stderr))
	}
	// nixos-rebuild evaluates the effective host configuration, which contains
	// the module marker assertion and /etc marker generated by this module. We
	// intentionally do not switch or activate anything during confirmation.
	return nil
}
