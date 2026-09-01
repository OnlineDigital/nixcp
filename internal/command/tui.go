package command

import (
	"fmt"

	"github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/tui"
	"github.com/nixcp/nixcp/internal/ui"
	"github.com/spf13/cobra"
)

// newTUICommand exposes the interactive panel as `ncp tui`. The panel itself
// lives in internal/tui; this command wires it to the runtime and enforces
// the same rules as the rest of the CLI (non-root, TTY required).
func newTUICommand(runtime Runtime, opts ...RuntimeOption) *cobra.Command {
	return &cobra.Command{
		Use:     "tui",
		Short:   "Launch the interactive panel",
		Long:    "Launch the NixCP interactive tabbed panel (status, sites, PHP, services, activity). Requires an interactive terminal; every mutation reuses the exact CLI pipelines with locked NixOS transactions.",
		Example: "  ncp tui",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !ui.StdinIsTTY() || !ui.StdoutIsTTY() {
				return errors.New("tui_requires_tty", "the interactive panel requires an interactive terminal", "Run ncp tui from a real terminal; scripts should use the subcommands", errors.ExitCodeUsage)
			}
			if err := tui.Run(NewTUIBackend(runtime, opts...)); err != nil {
				return errors.New("tui_failed", fmt.Sprintf("interactive panel failed: %v", err), "", errors.ExitCodeRuntime)
			}
			return nil
		},
	}
}
