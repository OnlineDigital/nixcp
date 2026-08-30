package command

import (
	"fmt"
	"strings"

	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newShellCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "shell", Short: "Shell helper setup"}
	cmd.AddCommand(&cobra.Command{Use: "init [bash|zsh|fish]", Short: "Print shell function snippet", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		snippet, err := shellSnippet(a[0])
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
func isSupportedShell(s string) bool { return s == "bash" || s == "zsh" || s == "fish" }
func shellActivation(shell, v string) (string, error) {
	if !isSupportedShell(shell) {
		return "", fmt.Errorf("unsupported shell")
	}
	bin := "/etc/nixcp/php/" + v + "/bin"
	if shell == "fish" {
		return "set -l old $NIXCP_PHP_BIN\nset -gx PATH " + fishQuote(bin) + " (string match -v -- $old $PATH)\nset -gx NIXCP_PHP_VERSION " + fishQuote(v) + "\nset -gx NIXCP_PHP_BIN " + fishQuote(bin) + "\n", nil
	}
	// Strip any previous NixCP bin from PATH (whatever its position), then
	// prepend the new one: switching versions or re-sourcing must leave
	// exactly one php bin in PATH, never conflicting duplicates. The strip
	// only runs when NIXCP_PHP_BIN is set: an empty pattern would otherwise
	// corrupt PATH (matching everywhere).
	return "if [ -n \"$NIXCP_PHP_BIN\" ]; then PATH=\"${PATH//\"$NIXCP_PHP_BIN\":/}\"; PATH=\"${PATH//:\"$NIXCP_PHP_BIN\"/}\"; fi; NIXCP_PHP_VERSION=" + shQuote(v) + "; NIXCP_PHP_BIN=" + shQuote(bin) + "; PATH=\"$NIXCP_PHP_BIN:$PATH\"; export NIXCP_PHP_VERSION NIXCP_PHP_BIN PATH\n", nil
}
func shellSnippet(shell string) (string, error) {
	if !isSupportedShell(shell) {
		return "", fmt.Errorf("unsupported shell")
	}
	if shell == "fish" {
		return "function ncp\n  if test (count $argv) -eq 3; and test $argv[1] = php; and test $argv[2] = use\n    set -l code (command ncp $argv --shell-emit=fish | string collect)\n    or return $status\n    source (printf '%s\n' $code | psub)\n  else\n    command ncp $argv\n  end\nend\n", nil
	}
	return "ncp() {\n  if [ \"$#\" -eq 3 ] && [ \"$1\" = php ] && [ \"$2\" = use ]; then\n    local code\n    code=\"$(command ncp \"$@\" --shell-emit=" + shell + ")\" || return $?\n    eval \"$code\"\n  else\n    command ncp \"$@\"\n  fi\n}\n", nil
}
func shQuote(s string) string   { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func fishQuote(s string) string { return shQuote(s) }
