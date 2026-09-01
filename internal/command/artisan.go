package command

import (
	"os"
	"path/filepath"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/php"
	"github.com/spf13/cobra"
)

func newArtisanCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "artisan [args...]",
		Short:   "Run artisan with resolved PHP",
		Long:    "Run the project's ./artisan with the NixCP-resolved PHP version. Any arguments and flags are forwarded to artisan unchanged.",
		Example: "  ncp artisan migrate\n  ncp artisan tinker\n  ncp artisan migrate:fresh --seed --force",
		// Artisan owns its argv (options like --seed or --force are artisan
		// flags, not NixCP flags), so parse nothing here; the root pre-run
		// extracts NixCP's own global flags from the raw tail and the RunE
		// re-reads the cleaned argv via passthroughArgs.
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runArtisan(cmd, runtime, passthroughArgs(cmd, args))
		},
	}
}

// runArtisan is the artisan execution path shared by `ncp artisan` and the
// `a`/`am`/`tinker` aliases: resolve PHP, verify ./artisan, forward argv.
func runArtisan(cmd *cobra.Command, runtime Runtime, args []string) error {
	_, snap, err := loadPHP(runtime)
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Join(cwd, "artisan"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0444 == 0 {
		return apperrors.New("artisan_not_found", "./artisan must be a readable regular file", "Run this command from a Laravel project", apperrors.ExitCodePrecond)
	}
	v, err := php.Resolve(cwd, snap.Config.PHP, os.Getenv("NIXCP_PHP_VERSION"))
	if err != nil {
		return apperrors.New("no_active_php_version", err.Error(), "Run: ncp php use <version>", apperrors.ExitCodePrecond)
	}
	// tinker is artisan's interactive REPL: attach the real TTY so the
	// user gets a prompt instead of a closed stdin.
	interactive := len(args) > 0 && args[0] == "tinker"
	argv := append([]string{"./artisan"}, args...)
	res, err := runtime.Runner.Run(cmd.Context(), &execx.Command{Name: php.Binary(v), Args: argv, Dir: cwd, Env: phpEnv(os.Environ(), v), Interactive: interactive})
	if err != nil || res.ExitCode != 0 {
		return processFailure("artisan_execution_failed", "Artisan", argv, res, err, apperrors.ExitCodeRuntime)
	}
	return nil
}
