package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newInstallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Initialize nixcp local state",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("install", false, map[string]any{"flags": map[string]any{}}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			return nil
		},
	}
	cmd.Flags().String("flake", "", "NixOS flake reference for import snippet")
	cmd.Flags().Bool("impure", false, "Allow impure evaluation when required")
	cmd.Flags().Bool("confirm-import", false, "Require import confirmation")
	return cmd
}
