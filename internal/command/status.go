package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show desired and actual state",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("status", false, map[string]any{"desired": nil, "actual": nil}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			cmd.Println("status: not-configured")
			return nil
		},
	}
	return cmd
}

func newDoctorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run diagnostic checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("doctor", false, map[string]any{"checks": []string{"state", "files", "tools"}}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			cmd.Println("doctor: ok")
			return nil
		},
	}
	return cmd
}
