package command

import (
	"fmt"
	"strings"
	"time"

	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func commandJSON(cmd *cobra.Command) bool {
	jsonOut, _ := cmd.Flags().GetBool("json")
	return jsonOut
}

func emitJSON(cmd *cobra.Command, payload any) error {
	if !commandJSON(cmd) {
		return nil
	}
	return output.WriteJSON(cmd.OutOrStdout(), payload)
}

// parseGlobalFlags extracts the persistent flags from the raw argv of a
// pass-through command (DisableFlagParsing) and applies them to that
// command's flag set. Only the exact forms `--json`, `--no-input`, `--yes`,
// `--timeout <dur>` and `--timeout=<dur>` are recognized anywhere in argv;
// everything else — including values meant for the passthrough target — is
// left untouched.
func parseGlobalFlags(cmd *cobra.Command, args []string) error {
	fs := cmd.Flags()
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			if err := fs.Set("json", "true"); err != nil {
				return err
			}
		case args[i] == "--no-input":
			if err := fs.Set("no-input", "true"); err != nil {
				return err
			}
		case args[i] == "--yes":
			if err := fs.Set("yes", "true"); err != nil {
				return err
			}
		case args[i] == "--timeout":
			if i+1 >= len(args) {
				return fmt.Errorf("--timeout requires a duration argument")
			}
			if err := validateTimeout(args[i+1]); err != nil {
				return err
			}
			if err := fs.Set("timeout", args[i+1]); err != nil {
				return err
			}
			i++
		case strings.HasPrefix(args[i], "--timeout="):
			v := strings.TrimPrefix(args[i], "--timeout=")
			if err := validateTimeout(v); err != nil {
				return err
			}
			if err := fs.Set("timeout", v); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTimeout(raw string) error {
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fmt.Errorf("invalid --timeout value %q (use e.g. 120s)", raw)
	}
	return nil
}
