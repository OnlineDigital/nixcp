package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func commandJSON(cmd *cobra.Command) bool {
	jsonOut, _ := cmd.Flags().GetBool("json")
	return jsonOut
}

func emitJSON(cmd *cobra.Command, payload any) error {
	if !commandJSON(cmd) {
		return nil
	}
	return output.WriteJSON(cmd.OutOrStdout(), payload)
}
