package command

import (
	"context"
	"time"

	"github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

// ApplicationRoot wraps the root command.
type ApplicationRoot struct {
	Root *cobra.Command
}

// NewRootCommand constructs all stage-1 CLI commands and flags.
func NewRootCommand(_ context.Context) (*cobra.Command, error) {
	root := &cobra.Command{
		Use:           "ncp",
		Short:         "NixCP CLI",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			jsonOut, err := cmd.Flags().GetBool("json")
			if err != nil {
				return err
			}
			if jsonOut {
				_ = cmd.Flags().Set("no-input", "true")
			}
			return nil
		},
	}

	root.PersistentFlags().Bool("json", false, "Emit a single JSON object")
	root.PersistentFlags().Bool("no-input", false, "Disable interactive prompts")
	root.PersistentFlags().Bool("yes", false, "Skip confirmation prompts")
	root.PersistentFlags().Duration("timeout", 30*time.Second, "Operation timeout")

	root.AddCommand(newInstallCommand())
	root.AddCommand(newStatusCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newServiceCommand())
	root.AddCommand(newServiceAliasCommand("nginx"))
	root.AddCommand(newServiceAliasCommand("mariadb"))
	root.AddCommand(newServiceAliasCommand("redis"))
	root.AddCommand(newPHPCommand())
	root.AddCommand(newArtisanCommand())
	root.AddCommand(newLinkCommand())
	root.AddCommand(newUnlinkCommand())
	root.AddCommand(newSitesCommand())
	root.AddCommand(newShellCommand())
	root.AddCommand(newVersionCommand())

	return root, nil
}

func New(ctx context.Context) (*ApplicationRoot, error) {
	root, err := NewRootCommand(ctx)
	if err != nil {
		return nil, err
	}
	return &ApplicationRoot{Root: root}, nil
}

func (a *ApplicationRoot) Execute() int {
	err := a.Root.Execute()
	if err == nil {
		return int(errors.ExitCodeSuccess)
	}
	appErr := errors.Normalize(err)
	if jsonOut, _ := a.Root.PersistentFlags().GetBool("json"); jsonOut {
		payload := output.Error("", "ncp_error", appErr.Message, "", nil)
		_ = output.WriteJSON(a.Root.ErrOrStderr(), payload)
	}
	return int(appErr.ExitCode())
}
