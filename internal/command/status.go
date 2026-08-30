package command

import (
	"os/user"

	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/ui"
	"github.com/spf13/cobra"
)

// newStatusCommand reports the desired/actual summary for services, PHP and
// sites. Read-only; a failed actual probe degrades to null, never errors.
func newStatusCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show desired state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		u, err := user.Current()
		if err != nil {
			return err
		}
		snap, err := state.NewStore(runtime.StateHomeOrDefault(u.HomeDir)).Load()
		if err != nil {
			if commandJSON(cmd) {
				return emitJSON(cmd, output.Success("status", false, map[string]any{"desired": nil, "actual": nil, "configured": false}, nil))
			}
			cmd.Println(ui.Heading("status") + ": not-configured")
			return nil
		}
		actual := map[string]any{}
		if runtime.Services != nil {
			for _, name := range []service.Name{service.Nginx, service.MariaDB, service.Redis} {
				if act, err := runtime.Services.Status(cmd.Context(), name); err == nil {
					actual[string(name)] = act
				} else {
					actual[string(name)] = nil
				}
			}
		}
		data := map[string]any{
			"desired":    snap,
			"actual":     actual,
			"configured": true,
		}
		if commandJSON(cmd) {
			return emitJSON(cmd, output.Success("status", false, data, nil))
		}
		cmd.Printf("%s: configured (%d sites, php %s)\n", ui.Heading("status"), len(snap.Sites), snap.Config.PHP.GlobalDefault)
		return nil
	}}
}
