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
		// When NIXCP_PHP_BIN is unset, passing the empty $old expansion to
		// string match makes Fish treat the first PATH entry as its pattern,
		// leaving no input and dropping the entire existing PATH. Only filter
		// when there is an old NixCP path to remove.
		return "set -l old $NIXCP_PHP_BIN\nset -l clean_path $PATH\nif test -n \"$old\"\n  set clean_path (string match -v -- \"$old\" $PATH)\nend\nset -gx PATH " + quote(bin) + " $clean_path\nset -gx NIXCP_PHP_VERSION " + quote(version) + "\nset -gx NIXCP_PHP_BIN " + quote(bin) + "\n", nil
	}

	// Strip any previous NixCP bin from PATH before prepending the new one.
	// Rebuilding from IFS-split entries also handles a PATH containing only
	// the old bin and avoids introducing empty entries.
	pathSplit := "$PATH"
	if name == "zsh" {
		// zsh does not word-split an unquoted $PATH by default, so the loop
		// would iterate the whole string once; expand the colon-separated
		// list explicitly instead.
		pathSplit = "${(s.:.)PATH}"
	}
	return "if [ -n \"$NIXCP_PHP_BIN\" ]; then _nixcp_path=\"\"; IFS=\":\"; for _nixcp_p in " + pathSplit + "; do [ -n \"$_nixcp_p\" ] && [ \"$_nixcp_p\" != \"$NIXCP_PHP_BIN\" ] && _nixcp_path=\"${_nixcp_path:+$_nixcp_path:}$_nixcp_p\"; done; unset IFS _nixcp_p; PATH=\"$_nixcp_path\"; unset _nixcp_path; fi; NIXCP_PHP_VERSION=" + quote(version) + "; NIXCP_PHP_BIN=" + quote(bin) + "; PATH=\"$NIXCP_PHP_BIN${PATH:+:$PATH}\"; export NIXCP_PHP_VERSION NIXCP_PHP_BIN PATH\n", nil
}

// Snippet returns the wrapper function users source from shell startup files.
// It intercepts `ncp php use <version>` and routes it through the internal
// --shell-emit activation protocol, sourcing the resulting env (and PATH
// rewrite) into the current shell instead of a subshell.
func Snippet(name string) (string, error) {
	if !Supported(name) {
		return "", fmt.Errorf("unsupported shell")
	}
	if name == "fish" {
		return "function ncp\n  if test (count $argv) -eq 3; and test $argv[1] = php; and test $argv[2] = use\n    set -l code (command ncp $argv --shell-emit=fish | string collect)\n    or return $status\n    source (printf '%s\n' $code | psub)\n  else\n    command ncp $argv\n  end\nend\n", nil
	}
	return "ncp() {\n  if [ \"$#\" -eq 3 ] && [ \"$1\" = php ] && [ \"$2\" = use ]; then\n    local code\n    code=\"$(command ncp \"$@\" --shell-emit=" + name + ")\" || return $?\n    eval \"$code\"\n  else\n    command ncp \"$@\"\n  fi\n}\n", nil
}

// Bootstrap returns the code that captures the configured default PHP version
// for a fresh interactive session. It runs only when no active version has
// been chosen yet (NIXCP_PHP_VERSION unset), asks `ncp php session` for the
// global default, and evals it. On a non-NixOS host (or when nothing is
// configured) `ncp php session` prints nothing and exits 0, so this degrades
// to a silent no-op.
func Bootstrap(name string) (string, error) {
	if !Supported(name) {
		return "", fmt.Errorf("unsupported shell")
	}
	if name == "fish" {
		return "if test -z \"$NIXCP_PHP_VERSION\"\n  set -l nixcp_code (command ncp php session --shell-emit=fish | string collect)\n  if test -n \"$nixcp_code\"\n    source (printf '%s\n' $nixcp_code | psub)\n  end\nend\n", nil
	}
	return "if [ -z \"${NIXCP_PHP_VERSION:-}\" ]; then\n  _nixcp_code=\"$(command ncp php session --shell-emit=" + name + " 2>/dev/null)\" || true\n  if [ -n \"$_nixcp_code\" ]; then\n    eval \"$_nixcp_code\"\n  fi\n  unset _nixcp_code\nfi\n", nil
}

// Startup returns the complete shell startup snippet: the ncp() wrapper plus
// the new-session default-capture bootstrap. This is what `ncp shell init`
// prints and what `ncp install` writes into ~/.nixcp/shell/<shell>.
func Startup(name string) (string, error) {
	if !Supported(name) {
		return "", fmt.Errorf("unsupported shell")
	}
	snippet, err := Snippet(name)
	if err != nil {
		return "", err
	}
	bootstrap, err := Bootstrap(name)
	if err != nil {
		return "", err
	}
	return snippet + bootstrap, nil
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
