package command

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/php"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
	"github.com/spf13/cobra"
)

func newPHPCommand(runtime Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "php", Short: "PHP operations", Args: cobra.ArbitraryArgs, DisableFlagParsing: true, RunE: func(c *cobra.Command, a []string) error { return dispatchPHP(c, runtime, a) }}
	cmd.AddCommand(&cobra.Command{Use: "install <version>", Short: "Install supported PHP version", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error { return mutatePHP(c, runtime, "install", a[0]) }})
	ext := &cobra.Command{Use: "ext", Short: "Nixpkgs PHP extensions"}
	ext.AddCommand(&cobra.Command{Use: "install <name>", Short: "Install nixpkgs PHP extension", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error { return mutatePHP(c, runtime, "extension", a[0]) }})
	cmd.AddCommand(ext)
	use := &cobra.Command{Use: "use <version>", Short: "Set active PHP version", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		global, _ := c.Flags().GetBool("global")
		if global {
			return mutatePHP(c, runtime, "global", a[0])
		}
		return useLocalPHP(c, runtime, a[0])
	}}
	use.Flags().Bool("global", false, "Set global default for new shells")
	use.Flags().String("shell-emit", "", "internal shell activation protocol")
	_ = use.Flags().MarkHidden("shell-emit")
	return cmd
}
func dispatchPHP(cmd *cobra.Command, runtime Runtime, args []string) error {
	if len(args) == 2 && args[0] == "install" {
		return mutatePHP(cmd, runtime, "install", args[1])
	}
	if len(args) == 3 && args[0] == "ext" && args[1] == "install" {
		return mutatePHP(cmd, runtime, "extension", args[2])
	}
	if len(args) >= 2 && args[0] == "use" {
		if len(args) == 3 && args[2] == "--global" {
			return mutatePHP(cmd, runtime, "global", args[1])
		}
		if len(args) == 3 && strings.HasPrefix(args[2], "--shell-emit=") {
			v, err := php.NormalizeVersion(args[1])
			if err != nil {
				return err
			}
			code, err := shellActivation(strings.TrimPrefix(args[2], "--shell-emit="), v)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), code)
			return err
		}
		if len(args) == 2 {
			return useLocalPHP(cmd, runtime, args[1])
		}
	}
	return runPHP(cmd, runtime, args)
}

func phpStore(runtime Runtime) (*state.Store, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	home := u.HomeDir
	if runtime.StateHome != "" {
		home = runtime.StateHome
	}
	return state.NewStore(home), nil
}
func loadPHP(runtime Runtime) (*state.Store, state.Snapshot, error) {
	store, err := phpStore(runtime)
	if err != nil {
		return nil, state.Snapshot{}, err
	}
	snap, err := store.Load()
	return store, snap, err
}
func mutatePHP(cmd *cobra.Command, runtime Runtime, action, raw string) error {
	if os.Geteuid() == 0 {
		return apperrors.New("root_not_allowed", "NixCP PHP commands must run as a non-root user", "Run ncp as the configured NixCP owner", apperrors.ExitCodePlatform)
	}
	store, snap, err := loadPHP(runtime)
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	var value string
	if action == "extension" {
		value, err = php.NormalizeExtension(raw)
	} else {
		value, err = php.NormalizeVersion(raw)
	}
	if err != nil {
		return apperrors.New("unsupported_php", err.Error(), "Use a supported nixpkgs PHP version or extension", apperrors.ExitCodePrecond)
	}
	changed := false
	switch action {
	case "install":
		if !containsString(snap.Config.PHP.Installed, value) {
			snap.Config.PHP.Installed = append(snap.Config.PHP.Installed, value)
			changed = true
		}
	case "extension":
		if !containsString(snap.Config.PHP.Extensions, value) {
			snap.Config.PHP.Extensions = append(snap.Config.PHP.Extensions, value)
			changed = true
		}
	case "global":
		if !containsString(snap.Config.PHP.Installed, value) {
			return apperrors.New("php_version_not_installed", "PHP "+value+" is not installed", "Run: ncp php install "+value, apperrors.ExitCodePrecond)
		}
		changed = snap.Config.PHP.GlobalDefault != value
		snap.Config.PHP.GlobalDefault = value
	}
	if !changed {
		return emitPHPResult(cmd, action, value, false, nil, "committed")
	}
	snap.Config.Canonicalize()
	if err := snap.Validate(); err != nil {
		return apperrors.New("invalid_state", err.Error(), "", apperrors.ExitCodePrecond)
	}
	module, err := runtime.Renderer.Render(snap)
	if err != nil {
		return apperrors.New("render_failed", err.Error(), "", apperrors.ExitCodeBuild)
	}
	config, err := state.MarshalConfig(snap.Config)
	if err != nil {
		return apperrors.New("invalid_state", err.Error(), "", apperrors.ExitCodePrecond)
	}
	manager := runtime.Transactions
	if manager == nil {
		manager = defaultServiceTransaction(store.Root, runtime, snap.Config.Rebuild, phpHealth{})
	}
	result, err := manager.Apply(cmd.Context(), transaction.Request{Files: map[string][]byte{"config.yaml": config, "generated/nixcp-module.nix": module}, CandidateModule: "generated/nixcp-module.nix", Affected: []string{"php"}})
	if err != nil {
		return transactionError(err)
	}
	warnings := php.CompatibilityWarnings(snap.Config.PHP.Installed, snap.Config.PHP.Extensions)
	return emitPHPResult(cmd, action, value, result.Changed, warnings, result.Phase)
}
func emitPHPResult(cmd *cobra.Command, action, value string, changed bool, warnings []php.Warning, phase string) error {
	data := map[string]any{"action": action, "value": value, "phase": phase}
	if commandJSON(cmd) {
		return emitJSON(cmd, output.Success("php."+action, changed, data, warnings))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "PHP %s %s: %s\n", action, value, phase)
	return nil
}
func useLocalPHP(cmd *cobra.Command, runtime Runtime, raw string) error {
	v, err := php.NormalizeVersion(raw)
	if err != nil {
		return apperrors.New("unsupported_php", err.Error(), "", apperrors.ExitCodePrecond)
	}
	_, snap, err := loadPHP(runtime)
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	if !containsString(snap.Config.PHP.Installed, v) {
		return apperrors.New("php_version_not_installed", "PHP "+v+" is not installed", "Run: ncp php install "+v, apperrors.ExitCodePrecond)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := writePHPMarker(filepath.Join(cwd, ".php-version"), v); err != nil {
		return apperrors.New("php_version_write_failed", err.Error(), "", apperrors.ExitCodeRuntime)
	}
	shell, _ := cmd.Flags().GetString("shell-emit")
	if shell != "" {
		code, err := shellActivation(shell, v)
		if err != nil {
			return apperrors.New("invalid_shell", err.Error(), "", apperrors.ExitCodeUsage)
		}
		fmt.Fprint(cmd.OutOrStdout(), code)
		return nil
	}
	return emitPHPResult(cmd, "use", v, true, nil, "local version written")
}
func writePHPMarker(path, v string) error {
	d := filepath.Dir(path)
	f, err := os.CreateTemp(d, ".php-version-")
	if err != nil {
		return err
	}
	n := f.Name()
	defer os.Remove(n)
	if _, err = f.WriteString(v + "\n"); err == nil {
		err = f.Chmod(0600)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(n, path)
	}
	return err
}
func runPHP(cmd *cobra.Command, runtime Runtime, args []string) error {
	_, snap, err := loadPHP(runtime)
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	cwd, _ := os.Getwd()
	v, err := php.Resolve(cwd, snap.Config.PHP, os.Getenv("NIXCP_PHP_VERSION"))
	if err != nil {
		return apperrors.New("no_active_php_version", err.Error(), "Run: ncp php use <version>", apperrors.ExitCodePrecond)
	}
	res, e := runtime.Runner.Run(cmd.Context(), &execx.Command{Name: php.Binary(v), Args: args, Dir: cwd, Env: phpEnv(os.Environ(), v)})
	if e != nil {
		return apperrors.New("php_execution_failed", strings.TrimSpace(res.Stderr), "PHP exit code: "+fmt.Sprint(res.ExitCode), apperrors.ExitCodeRuntime)
	}
	return nil
}
func phpEnv(env []string, v string) []string {
	out := append([]string(nil), env...)
	out = append(out, "NIXCP_PHP_VERSION="+v, "NIXCP_PHP_BIN="+filepath.Dir(php.Binary(v)))
	return out
}
func containsString(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

type phpHealth struct{}

func (phpHealth) Check(_ context.Context, _ []string) error { return nil }
