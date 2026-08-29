package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("version", false, map[string]any{"version": "0.0.0"}, nil)
			return emitJSON(cmd, payload)
		},
	}
}
