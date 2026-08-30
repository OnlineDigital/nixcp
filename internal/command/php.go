package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/php"
	"github.com/nixcp/nixcp/internal/securefs"
	shellpkg "github.com/nixcp/nixcp/internal/shell"
	sitepkg "github.com/nixcp/nixcp/internal/site"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
	"github.com/spf13/cobra"
)

func newPHPCommand(runtime Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "php", Short: "PHP operations", Args: cobra.ArbitraryArgs, DisableFlagParsing: true, RunE: func(c *cobra.Command, a []string) error { return dispatchPHP(c, runtime, passthroughArgs(c, a)) }}
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
			// Shell-emit is the internal wrapper protocol. Route through the
			// same validation and .php-version write as plain `use`, but print
			// ONLY evaluable shell code on stdout (the wrapper evals it
			// verbatim after checking the exit code).
			return useLocalPHPWithShell(cmd, runtime, args[1], strings.TrimPrefix(args[2], "--shell-emit="))
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
	shell, _ := cmd.Flags().GetString("shell-emit")
	return useLocalPHPWithShell(cmd, runtime, raw, shell)
}

// useLocalPHPWithShell validates the version, writes .php-version, and when
// shell is non-empty emits ONLY the evaluable shell activation code (the
// ncp() wrapper protocol); otherwise it emits the normal command result.
func useLocalPHPWithShell(cmd *cobra.Command, runtime Runtime, raw, shell string) error {
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
	project, err := safePHPProjectDir(cwd)
	if err != nil {
		return apperrors.New("php_version_write_failed", err.Error(), "Use a project outside unsafe world-writable directories", apperrors.ExitCodeRuntime)
	}
	if err := writePHPMarker(filepath.Join(project, ".php-version"), v); err != nil {
		return apperrors.New("php_version_write_failed", err.Error(), "", apperrors.ExitCodeRuntime)
	}
	if shell != "" {
		code, err := shellpkg.Activation(shell, v)
		if err != nil {
			return apperrors.New("invalid_shell", err.Error(), "", apperrors.ExitCodeUsage)
		}
		fmt.Fprint(cmd.OutOrStdout(), code)
		return nil
	}
	return emitPHPResult(cmd, "use", v, true, nil, "local version written")
}
func writePHPMarker(path, v string) error {
	return securefs.WithPrivateUmask(func() error { return writePHPMarkerPrivate(path, v) })
}
func writePHPMarkerPrivate(path, v string) error {
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

// safePHPProjectDir applies the same canonical-path and sticky-directory
// policy used for linked sites before creating a project-local marker.
func safePHPProjectDir(cwd string) (string, error) {
	project, err := sitepkg.CanonicalizePath(cwd)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(project)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0555 != 0555 {
		return "", fmt.Errorf("current directory must be readable and traversable")
	}
	if err := sitepkg.RefuseWorldWritable(project); err != nil {
		return "", fmt.Errorf("current directory must not be beneath a world-writable directory")
	}
	return project, nil
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
	interactive := len(args) > 0 && (args[0] == "-a" || args[0] == "--interactive")
	c := &execx.Command{Name: php.Binary(v), Args: args, Dir: cwd, Env: phpEnv(os.Environ(), v), Interactive: interactive}
	res, e := runtime.Runner.Run(cmd.Context(), c)
	if e != nil || res.ExitCode != 0 {
		return processFailure("php_execution_failed", "PHP", append([]string{php.Binary(v)}, args...), res, e, apperrors.ExitCodeRuntime)
	}
	return nil
}

// processFailure wraps a child-process failure as a stable AppError while
// propagating the child's own exit code (and signal, when applicable) so
// `ncp php`/`ncp artisan` behave like the wrapped tool in scripts.
func processFailure(code, label string, argv []string, res execx.Result, runErr error, fallback apperrors.ExitCode) error {
	pe := apperrors.ProcessExit{ExitCode: res.ExitCode, Command: strings.Join(argv, " "), Stderr: strings.TrimSpace(res.Stderr)}
	var pex *execx.ProcessExitError
	if errors.As(runErr, &pex) {
		pe.ExitCode = pex.ExitCode
		pe.Stderr = strings.TrimSpace(pex.Stderr)
		pe.Signal = pex.Signal
	}
	if pe.ExitCode == 0 {
		// The child reported success but the runner errored; keep it a failure.
		pe.ExitCode = 1
	}
	hint := fmt.Sprintf("%s exit code: %d", label, pe.ExitCode)
	if pe.Signal != "" {
		hint = fmt.Sprintf("%s terminated by signal %s", label, pe.Signal)
	}
	return apperrors.New(code, pe.Stderr, hint, fallback).WithProcessExit(pe)
}

// phpEnv returns a copy of env with exactly one NIXCP_PHP_VERSION and one
// NIXCP_PHP_BIN entry (stale values from earlier activations are replaced,
// not duplicated).
func phpEnv(env []string, v string) []string {
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		if strings.HasPrefix(kv, "NIXCP_PHP_VERSION=") || strings.HasPrefix(kv, "NIXCP_PHP_BIN=") {
			continue
		}
		out = append(out, kv)
	}
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
