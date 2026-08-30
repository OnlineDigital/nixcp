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
	return &cobra.Command{Use: "artisan [args...]", Short: "Run artisan with resolved PHP", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
	}}
}
