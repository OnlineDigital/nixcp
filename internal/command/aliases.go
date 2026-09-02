package command

import (
	"github.com/spf13/cobra"
)

// runAsAlias executes the target RunE with the alias's injected prefix
// plus whatever the user appended. Alias commands keep DisableFlagParsing
// so every extra argument or flag the user typed is forwarded verbatim to
// the wrapped tool; the root pre-run has already stripped NixCP's own global
// flags (see parseGlobalFlags), so read the cleaned argv back the same way
// the passthrough commands do (passthroughArgs).
func runAsAlias(target func(*cobra.Command, []string) error, prefix []string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		clean := passthroughArgs(cmd, args)
		return target(cmd, append(append([]string{}, prefix...), clean...))
	}
}

// newArtisanAliasCommand builds a pass-through alias for `ncp artisan`.
// name is the alias users type (e.g. "a"); prefix is the argv injected in
// front of whatever the user appends (e.g. ["migrate"] for `ncp am`).
func newArtisanAliasCommand(runtime Runtime, name, short string, prefix []string) *cobra.Command {
	return &cobra.Command{
		Use:                name + " [args...]",
		Short:              short,
		Long:               "Shortcut for `ncp artisan" + joinAliasTail(prefix) + "`. Any extra arguments or flags are forwarded to artisan unchanged.",
		Example:            aliasExample(name, "artisan", prefix),
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runAsAlias(func(c *cobra.Command, a []string) error { return runArtisan(c, runtime, a) }, prefix),
	}
}

// newComposerAliasCommand builds a pass-through alias for `ncp composer`.
// newPHPVendorAliasCommand builds a direct PHP proxy shortcut for a fixed
// project-local executable under vendor/bin (for example Laravel Pint).
func newPHPVendorAliasCommand(runtime Runtime, name, executable, short string) *cobra.Command {
	return &cobra.Command{
		Use:                name + " [args...]",
		Short:              short,
		Long:               "Shortcut for `ncp php " + executable + "`. Any extra arguments or flags are forwarded unchanged.",
		Example:            "  ncp " + name + " --parallel",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runAsAlias(func(c *cobra.Command, a []string) error { return runPHP(c, runtime, a) }, []string{executable}),
	}
}

func newComposerAliasCommand(runtime Runtime, name, short string, prefix []string) *cobra.Command {
	return &cobra.Command{
		Use:                name + " [args...]",
		Short:              short,
		Long:               "Shortcut for `ncp composer" + joinAliasTail(prefix) + "`. Any extra arguments or flags are forwarded to Composer unchanged.",
		Example:            aliasExample(name, "composer", prefix),
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: runAsAlias(func(c *cobra.Command, a []string) error {
			return runComposer(c, runtime, a)
		}, prefix),
	}
}

// joinAliasTail renders the injected prefix for help strings.
func joinAliasTail(prefix []string) string {
	tail := ""
	for _, p := range prefix {
		tail += " " + p
	}
	return tail
}

func aliasExample(alias, target string, prefix []string) string {
	_ = target
	// Show the shortcut plus a representative appended argument, making it
	// explicit that extra user argv is part of the contract.
	if len(prefix) == 0 {
		return "  ncp " + alias + " migrate --force\n  ncp " + alias + " make:model Post --migration"
	}
	// For fixed-prefix shortcuts, show plain usage plus an appended flag.
	// Skip the prefix when it equals the alias itself (tinker), which would
	// otherwise read as a doubled `ncp tinker tinker`.
	exampleTail := joinAliasTail(prefix)
	if prefix[0] == alias {
		exampleTail = ""
	}
	return "  ncp " + alias + exampleTail + "\n  ncp " + alias + exampleTail + " --extra-flag"
}
