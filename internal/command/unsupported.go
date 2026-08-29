package command

import (
	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/spf13/cobra"
)

// unavailable is deliberately explicit: a command must never claim it changed a
// NixOS system unless it has passed through the transaction/rebuild protocol.
func unavailable(feature string) error {
	return apperrors.New("feature_not_implemented", feature+" is not implemented in this build", "This command is unavailable until its safe transaction backend is installed", apperrors.ExitCodePrecond)
}
func unavailableRun(feature string) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, _ []string) error { return unavailable(feature) }
}
