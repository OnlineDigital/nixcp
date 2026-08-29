package command

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newShellCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "shell", Short: "Shell helper setup"}

	initCmd := &cobra.Command{
		Use:   "init [bash|zsh|fish]",
		Short: "Print shell function snippet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := args[0]
			if !isSupportedShell(shell) {
				return cmd.Help()
			}
			if commandJSON(cmd) {
				payload := map[string]any{"ok": true, "command": "shell.init", "changed": false, "data": map[string]any{"shell": shell}, "warnings": []string{}}
				return emitJSON(cmd, payload)
			}
			cmd.Print(fmt.Sprintf("function ncp() {} # %s", shell))
			cmd.Println()
			return nil
		},
	}
	cmd.AddCommand(initCmd)
	return cmd
}

func isSupportedShell(name string) bool {
	switch name {
	case "bash", "zsh", "fish":
		return true
	default:
		return false
	}
}
