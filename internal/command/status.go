package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/spf13/cobra"
	"os/user"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show desired state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		u, err := user.Current()
		if err != nil {
			return err
		}
		snap, err := state.NewStore(u.HomeDir).Load()
		if err != nil {
			if commandJSON(cmd) {
				return emitJSON(cmd, output.Success("status", false, map[string]any{"desired": nil, "actual": nil, "configured": false}, nil))
			}
			cmd.Println("status: not-configured")
			return nil
		}
		data := map[string]any{"desired": snap, "actual": nil, "configured": true}
		if commandJSON(cmd) {
			return emitJSON(cmd, output.Success("status", false, data, nil))
		}
		cmd.Printf("status: configured (%d sites)\n", len(snap.Sites))
		return nil
	}}
}
func newDoctorCommand() *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Run diagnostic checks", Args: cobra.NoArgs, RunE: unavailableRun("doctor")}
}
