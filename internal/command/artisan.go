package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newArtisanCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artisan [args...]",
		Short: "Run artisan command",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("artisan", false, map[string]any{"args": args}, nil)
			return emitJSON(cmd, payload)
		},
	}
	return cmd
}
