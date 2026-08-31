package command

import (
	"os/user"
	"strings"

	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/ui"
	"github.com/spf13/cobra"
)

// serviceStatus is the stable desired-versus-actual shape used by status and
// doctor. A nil Actual and Drift means the systemd probe was unavailable; it
// is deliberately distinct from an inactive service.
type serviceStatus struct {
	Desired state.ServiceConfig `json:"desired"`
	Actual  *service.Actual     `json:"actual"`
	Drift   *bool               `json:"drift"`
	Error   string              `json:"error,omitempty"`
}

func desiredServiceConfig(c state.ConfigSnapshot, name service.Name) state.ServiceConfig {
	switch name {
	case service.Nginx:
		return c.Services.Nginx
	case service.MariaDB:
		return c.Services.MariaDB
	default:
		return c.Services.Redis
	}
}

func serviceHasDrift(desired state.ServiceConfig, actual service.Actual) bool {
	running := desired.Installed && desired.DesiredState == "running"
	return actual.Active != running || actual.Enabled != running
}

func collectServiceStatus(cmd *cobra.Command, runtime Runtime, config state.ConfigSnapshot) map[string]serviceStatus {
	result := make(map[string]serviceStatus, 3)
	for _, name := range []service.Name{service.Nginx, service.MariaDB, service.Redis} {
		desired := desiredServiceConfig(config, name)
		entry := serviceStatus{Desired: desired}
		if runtime.Services == nil {
			entry.Error = "systemd adapter unavailable"
			result[string(name)] = entry
			continue
		}
		actual, err := runtime.Services.Status(cmd.Context(), name)
		if err != nil {
			entry.Error = err.Error()
			result[string(name)] = entry
			continue
		}
		drift := serviceHasDrift(desired, actual)
		entry.Actual = &actual
		entry.Drift = &drift
		result[string(name)] = entry
	}
	return result
}

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
		services := collectServiceStatus(cmd, runtime, snap.Config)
		actual := map[string]any{}
		drift := []string{}
		for _, name := range []service.Name{service.Nginx, service.MariaDB, service.Redis} {
			entry := services[string(name)]
			actual[string(name)] = entry.Actual
			if entry.Drift != nil && *entry.Drift {
				drift = append(drift, string(name))
			}
		}
		data := map[string]any{
			"desired":    snap,
			"actual":     actual,
			"services":   services,
			"drift":      drift,
			"configured": true,
		}
		if commandJSON(cmd) {
			return emitJSON(cmd, output.Success("status", false, data, nil))
		}
		cmd.Printf("%s: configured (%d sites, php %s)\n", ui.Heading("status"), len(snap.Sites), snap.Config.PHP.GlobalDefault)
		for _, name := range []service.Name{service.Nginx, service.MariaDB, service.Redis} {
			entry := services[string(name)]
			if entry.Actual == nil {
				cmd.Printf("%s: desired=%s installed=%t actual=unknown drift=unknown (%s)\n", name, entry.Desired.DesiredState, entry.Desired.Installed, entry.Error)
				continue
			}
			cmd.Printf("%s: desired=%s installed=%t actual=%t enabled=%t health=%s drift=%t\n", name, entry.Desired.DesiredState, entry.Desired.Installed, entry.Actual.Active, entry.Actual.Enabled, entry.Actual.Health, *entry.Drift)
		}
		if len(drift) > 0 {
			cmd.Printf("%s: %s\n", ui.Heading("drift"), strings.Join(drift, ", "))
		}
		return nil
	}}
}
