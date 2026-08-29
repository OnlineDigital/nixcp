package command

import (
	"fmt"

	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newPHPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "php",
		Short: "PHP operations",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("php", false, map[string]any{"args": args}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			return nil
		},
	}

	install := &cobra.Command{
		Use:   "install <version>",
		Short: "Install PHP version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("php.install", true, map[string]any{"version": args[0]}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			cmd.Println(fmt.Sprintf("php %s installed", args[0]))
			return nil
		},
	}

	ext := &cobra.Command{Use: "ext", Short: "PHP extensions"}
	ext.AddCommand(&cobra.Command{
		Use:   "install <name>",
		Short: "Install PHP extension",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("php.ext.install", true, map[string]any{"name": args[0]}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			cmd.Println(fmt.Sprintf("php ext %s installed", args[0]))
			return nil
		},
	})

	use := &cobra.Command{
		Use:   "use <version>",
		Short: "Set active PHP version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			global, _ := cmd.Flags().GetBool("global")
			payload := output.Success("php.use", false, map[string]any{"version": args[0], "global": global}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			cmd.Println(fmt.Sprintf("php use %s", args[0]))
			return nil
		},
	}
	use.Flags().Bool("global", false, "Set global default")

	cmd.AddCommand(install)
	cmd.AddCommand(ext)
	cmd.AddCommand(use)
	return cmd
}
