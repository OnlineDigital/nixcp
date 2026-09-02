package command

import (
	"fmt"
	"os/user"
	"sort"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/spf13/cobra"
)

// defaultAliases is persisted during installation. Keeping the target argv in
// config.yaml makes aliases inspectable and leaves their appended argv exactly
// untouched by the alias layer.
func defaultAliases() map[string][]string {
	return map[string][]string{
		"a":      {"artisan"},
		"am":     {"artisan", "migrate"},
		"tinker": {"artisan", "tinker"},
		"ci":     {"composer", "install"},
		"c":      {"composer", "run"},
		"pint":   {"php", "./vendor/bin/pint"},
	}
}

func configuredAliases(runtime Runtime) map[string][]string {
	u, err := user.Current()
	if err != nil {
		return defaultAliases()
	}
	snap, err := state.NewStore(runtime.StateHomeOrDefault(u.HomeDir)).Load()
	if err != nil || len(snap.Config.Aliases) == 0 {
		return defaultAliases()
	}
	return snap.Config.Aliases
}

func newConfiguredAliasCommand(runtime Runtime, name string, target []string) *cobra.Command {
	return &cobra.Command{
		Use:                name + " [args...]",
		Short:              "Alias for ncp " + strings.Join(target, " "),
		Long:               "Configured alias for `ncp " + strings.Join(target, " ") + "`. All appended arguments and flags are forwarded unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := append(append([]string{}, target...), passthroughArgs(cmd, args)...)
			if len(argv) == 0 {
				return apperrors.New("invalid_alias", "alias has no target", "Run ncp install to restore default aliases", apperrors.ExitCodePrecond)
			}
			switch argv[0] {
			case "artisan":
				return runArtisan(cmd, runtime, argv[1:])
			case "composer":
				return runComposer(cmd, runtime, argv[1:])
			case "php":
				return runPHP(cmd, runtime, argv[1:])
			default:
				return apperrors.New("unsupported_alias_target", "alias "+name+" targets unsupported command "+argv[0], "Aliases may target artisan, composer, or php", apperrors.ExitCodeUsage)
			}
		},
	}
}

func newAliasCommand(runtime Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "alias", Short: "Inspect configured command aliases"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List aliases and their final commands", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		u, userErr := user.Current()
		if userErr != nil {
			return apperrors.New("user_lookup_failed", userErr.Error(), "", apperrors.ExitCodeRuntime)
		}
		snap, err := state.NewStore(runtime.StateHomeOrDefault(u.HomeDir)).Load()
		if err != nil {
			return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
		}
		names := make([]string, 0, len(snap.Config.Aliases))
		for name := range snap.Config.Aliases {
			names = append(names, name)
		}
		sort.Strings(names)
		if commandJSON(cmd) {
			items := make([]map[string]string, 0, len(names))
			for _, name := range names {
				items = append(items, map[string]string{"alias": name, "command": "ncp " + strings.Join(snap.Config.Aliases[name], " ")})
			}
			return emitJSON(cmd, map[string]any{"ok": true, "command": "alias list", "changed": false, "data": items})
		}
		for _, name := range names {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\tncp %s\n", name, strings.Join(snap.Config.Aliases[name], " "))
		}
		return nil
	}})
	return cmd
}
