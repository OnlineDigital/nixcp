package command

import (
	"fmt"

	"github.com/nixcp/nixcp/internal/output"
	shellpkg "github.com/nixcp/nixcp/internal/shell"
	"github.com/spf13/cobra"
)

func newShellCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "shell", Short: "Shell helper setup"}
	cmd.AddCommand(&cobra.Command{Use: "init [bash|zsh|fish]", Short: "Print shell function snippet", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		snippet, err := shellpkg.Startup(a[0])
		if err != nil {
			return err
		}
		if commandJSON(c) {
			return emitJSON(c, output.Success("shell.init", false, map[string]any{"shell": a[0]}, nil))
		}
		_, _ = fmt.Fprint(c.OutOrStdout(), snippet)
		return nil
	}})
	return cmd
}
