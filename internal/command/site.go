package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newLinkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Create site manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("link", true, map[string]any{"domain": args[0]}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			if cmd.Flags().Changed("template") && cmd.Flags().Changed("config") {
				return cmd.Help()
			}
			return nil
		},
	}
	cmd.Flags().String("php", "", "PHP version")
	cmd.Flags().String("mariadb", "", "MariaDB name")
	cmd.Flags().String("template", "", "Template")
	cmd.Flags().String("config", "", "Custom config path")
	cmd.Flags().String("path", "", "Project path")
	cmd.Flags().String("root", "", "Document root")
	return cmd
}

func newUnlinkCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <domain-or-site-id>",
		Short: "Remove site manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("unlink", true, map[string]any{"site": args[0]}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			return nil
		},
	}
}

func newSitesCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "sites", Short: "List and show site entries"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List sites",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("sites.list", false, map[string]any{"items": []string{}}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "show <domain-or-site-id>",
		Short: "Show site details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("sites.show", false, map[string]any{"site": args[0], "manifest": map[string]any{}}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			return nil
		},
	})
	return cmd
}
