package command

import "github.com/spf13/cobra"

func newArtisanCommand() *cobra.Command {
	return &cobra.Command{Use: "artisan [args...]", Short: "Run artisan command", Args: cobra.ArbitraryArgs, RunE: unavailableRun("artisan execution")}
}
