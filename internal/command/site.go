package command

import "github.com/spf13/cobra"

func newLinkCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "link <domain>", Short: "Create site manifest", Args: cobra.ExactArgs(1), RunE: unavailableRun("site link")}
	cmd.Flags().String("php", "", "PHP version")
	cmd.Flags().String("mariadb", "", "MariaDB name")
	cmd.Flags().String("template", "", "Template")
	cmd.Flags().String("config", "", "Custom config path")
	cmd.Flags().String("path", "", "Project path")
	cmd.Flags().String("root", "", "Document root")
	return cmd
}
func newUnlinkCommand() *cobra.Command {
	return &cobra.Command{Use: "unlink <domain-or-site-id>", Short: "Remove site manifest", Args: cobra.ExactArgs(1), RunE: unavailableRun("site unlink")}
}
func newSitesCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "sites", Short: "List and show site entries"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List sites", Args: cobra.NoArgs, RunE: unavailableRun("sites list")}, &cobra.Command{Use: "show <domain-or-site-id>", Short: "Show site details", Args: cobra.ExactArgs(1), RunE: unavailableRun("sites show")})
	return cmd
}
