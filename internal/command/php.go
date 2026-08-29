package command

import "github.com/spf13/cobra"

func newPHPCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "php", Short: "PHP operations", Args: cobra.ArbitraryArgs, RunE: unavailableRun("php execution")}
	cmd.AddCommand(&cobra.Command{Use: "install <version>", Short: "Install PHP version", Args: cobra.ExactArgs(1), RunE: unavailableRun("php install")})
	ext := &cobra.Command{Use: "ext", Short: "PHP extensions"}
	ext.AddCommand(&cobra.Command{Use: "install <name>", Short: "Install PHP extension", Args: cobra.ExactArgs(1), RunE: unavailableRun("php extension install")})
	cmd.AddCommand(ext)
	use := &cobra.Command{Use: "use <version>", Short: "Set active PHP version", Args: cobra.ExactArgs(1), RunE: unavailableRun("php use")}
	use.Flags().Bool("global", false, "Set global default")
	cmd.AddCommand(use)
	return cmd
}
