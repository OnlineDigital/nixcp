// Package shell implements the small, argv-safe protocol between NixCP and
// supported interactive shells.
package shell

import (
	"fmt"
	"strings"
)

// Supported reports whether name is a shell for which NixCP can emit code.
func Supported(name string) bool {
	return name == "bash" || name == "zsh" || name == "fish"
}

// Activation returns shell code that selects the given PHP version without
// accumulating stale NixCP entries in PATH.
func Activation(name, version string) (string, error) {
	if !Supported(name) {
		return "", fmt.Errorf("unsupported shell")
	}
	bin := "/etc/nixcp/php/" + version + "/bin"
	if name == "fish" {
		return "set -l old $NIXCP_PHP_BIN\nset -gx PATH " + quote(bin) + " (string match -v -- $old $PATH)\nset -gx NIXCP_PHP_VERSION " + quote(version) + "\nset -gx NIXCP_PHP_BIN " + quote(bin) + "\n", nil
	}

	// Strip any previous NixCP bin from PATH before prepending the new one.
	// Rebuilding from IFS-split entries also handles a PATH containing only
	// the old bin and avoids introducing empty entries.
	return "if [ -n \"$NIXCP_PHP_BIN\" ]; then _nixcp_path=\"\"; IFS=\":\"; for _nixcp_p in $PATH; do [ -n \"$_nixcp_p\" ] && [ \"$_nixcp_p\" != \"$NIXCP_PHP_BIN\" ] && _nixcp_path=\"${_nixcp_path:+$_nixcp_path:}$_nixcp_p\"; done; unset IFS _nixcp_p; PATH=\"$_nixcp_path\"; unset _nixcp_path; fi; NIXCP_PHP_VERSION=" + quote(version) + "; NIXCP_PHP_BIN=" + quote(bin) + "; PATH=\"$NIXCP_PHP_BIN${PATH:+:$PATH}\"; export NIXCP_PHP_VERSION NIXCP_PHP_BIN PATH\n", nil
}

// Snippet returns the wrapper function users source from shell startup files.
func Snippet(name string) (string, error) {
	if !Supported(name) {
		return "", fmt.Errorf("unsupported shell")
	}
	if name == "fish" {
		return "function ncp\n  if test (count $argv) -eq 3; and test $argv[1] = php; and test $argv[2] = use\n    set -l code (command ncp $argv --shell-emit=fish | string collect)\n    or return $status\n    source (printf '%s\n' $code | psub)\n  else\n    command ncp $argv\n  end\nend\n", nil
	}
	return "ncp() {\n  if [ \"$#\" -eq 3 ] && [ \"$1\" = php ] && [ \"$2\" = use ]; then\n    local code\n    code=\"$(command ncp \"$@\" --shell-emit=" + name + ")\" || return $?\n    eval \"$code\"\n  else\n    command ncp \"$@\"\n  fi\n}\n", nil
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
