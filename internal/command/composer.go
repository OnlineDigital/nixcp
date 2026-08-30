package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/php"
	"github.com/spf13/cobra"
)

// composerScript is installed by the generated NixOS module. An absolute,
// system-owned path means a project-local executable or an untrusted PATH
// entry can never be selected in its place.
const composerScript = "/etc/nixcp/composer/bin/composer"

// newComposerCommand runs Composer from the current project without a shell.
// DisableFlagParsing is deliberate: Composer owns its argv, while the root
// pre-run extracts and removes NixCP's global flags from the raw tail.
func newComposerCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:                "composer [args...]",
		Short:              "Run Composer with resolved PHP",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposer(cmd, runtime, passthroughArgs(cmd, args))
		},
	}
}

func runComposer(cmd *cobra.Command, runtime Runtime, args []string) error {
	_, snap, err := loadPHP(runtime)
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	v, err := php.Resolve(cwd, snap.Config.PHP, os.Getenv("NIXCP_PHP_VERSION"))
	if err != nil {
		return apperrors.New("no_active_php_version", err.Error(), "Run: ncp php use <version>", apperrors.ExitCodePrecond)
	}

	argv := append([]string{composerScript}, args...)
	res, runErr := runtime.Runner.Run(cmd.Context(), &execx.Command{
		// Invoke the Composer PHP script through the exact resolved PHP
		// binary, rather than trusting the package shebang's PHP version.
		Name: php.Binary(v),
		Args: argv,
		Dir:  cwd,
		Env:  composerEnv(os.Environ(), v),
	})
	if runErr != nil || res.ExitCode != 0 {
		if !commandJSON(cmd) && res.Stdout != "" {
			fmt.Fprint(cmd.OutOrStdout(), res.Stdout)
		}
		if !commandJSON(cmd) && strings.TrimSpace(res.Stderr) != "" {
			fmt.Fprint(cmd.ErrOrStderr(), res.Stderr)
		}
		return processFailure("composer_execution_failed", "Composer", append([]string{php.Binary(v)}, argv...), res, runErr, apperrors.ExitCodeRuntime)
	}

	if commandJSON(cmd) {
		return emitJSON(cmd, output.Success("composer", false, map[string]any{
			"phpVersion": v,
			"stdout":     res.Stdout,
			"stderr":     res.Stderr,
		}, nil))
	}
	if res.Stdout != "" {
		fmt.Fprint(cmd.OutOrStdout(), res.Stdout)
	}
	if res.Stderr != "" {
		fmt.Fprint(cmd.ErrOrStderr(), res.Stderr)
	}
	return nil
}

// composerEnv uses the same normalized NixCP variables as PHP and ensures the
// resolved PHP is first on PATH. Composer's launcher and any subprocesses it
// starts therefore use the project-selected PHP, without trusting a project
// executable or invoking a shell.
func composerEnv(env []string, v string) []string {
	out := phpEnv(env, v)
	bin := filepath.Dir(php.Binary(v))
	for i, kv := range out {
		if strings.HasPrefix(kv, "PATH=") {
			out[i] = "PATH=" + bin + ":" + strings.TrimPrefix(kv, "PATH=")
			return out
		}
	}
	return append(out, "PATH="+bin)
}
