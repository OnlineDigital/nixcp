package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real-shell tests for the binary–shell protocol: the wrapper snippet and
// the activation code are EXECUTED by actual interpreters (bash/fish), not
// string-compared against themselves. A broken quoting, eval, or PATH
// rewrite fails here even when the emitted bytes look plausible.

func skipWithoutShell(t *testing.T, shell string) {
	t.Helper()
	if _, err := exec.LookPath(shell); err != nil {
		t.Skipf("%s not available on this host", shell)
	}
}

// TestShellActivationExecutesInBash runs the emitted activation code in a
// real bash and asserts the exported environment the protocol promises.
func TestShellActivationExecutesInBash(t *testing.T) {
	skipWithoutShell(t, "bash")
	code, err := shellActivation("bash", "8.3")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.TrimRight(code, "\n") + `; printf '%s\n%s\n%s\n' "$NIXCP_PHP_VERSION" "$NIXCP_PHP_BIN" "$PATH"`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash rejected activation code: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "8.3" || lines[1] != "/etc/nixcp/php/8.3/bin" {
		t.Fatalf("env not exported as promised: version=%q bin=%q", lines[0], lines[1])
	}
	if !strings.HasPrefix(lines[2], "/etc/nixcp/php/8.3/bin:") {
		t.Fatalf("PATH not prepended, got %q", lines[2])
	}
	if strings.Count(lines[2], "/etc/nixcp/php/8.3/bin") != 1 {
		t.Fatalf("activation duplicated the PATH entry: %q", lines[2])
	}
}

// TestShellActivationIsIdempotentInBash: sourcing the activation twice (or
// switching versions) must not stack duplicate PATH entries. The empty-
// NIXCP_PHP_BIN first activation is the dangerous case: a naive strip
// pattern would corrupt PATH, so this runs against a realistic multi-
// element PATH.
func TestShellActivationIsIdempotentInBash(t *testing.T) {
	skipWithoutShell(t, "bash")
	code83, err := shellActivation("bash", "8.3")
	if err != nil {
		t.Fatal(err)
	}
	code84, err := shellActivation("bash", "8.4")
	if err != nil {
		t.Fatal(err)
	}
	sysPath := os.Getenv("PATH")
	if sysPath == "" {
		sysPath = "/usr/bin:/bin"
	}
	script := strings.TrimRight(code83+code84+code84, "\n") + `; printf '%s\n' "$PATH"`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{"PATH=" + sysPath}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash rejected switch/double activation: %v\n%s", err, out)
	}
	path := strings.TrimSpace(string(out))
	if n := strings.Count(path, "/etc/nixcp/php/8.3/bin"); n != 0 {
		t.Fatalf("switch must remove the old bin from PATH, found %d: %q", n, path)
	}
	if n := strings.Count(path, "/etc/nixcp/php/8.4/bin"); n != 1 {
		t.Fatalf("double activation must keep exactly one PATH entry, found %d: %q", n, path)
	}
	if strings.Contains(path, "::") || strings.HasPrefix(path, ":") || strings.HasSuffix(path, ":") {
		t.Fatalf("activation corrupted PATH (empty elements): %q", path)
	}
	if !strings.HasPrefix(path, "/etc/nixcp/php/8.4/bin:") {
		t.Fatalf("new bin must lead PATH, got %q", path)
	}
}

// TestShellActivationSingleEntryPATH: regression for the review-reported
// case where PATH consists solely of the old NIXCP bin — positional
// substitution patterns miss it and reactivation duplicates the entry.
func TestShellActivationSingleEntryPATH(t *testing.T) {
	skipWithoutShell(t, "bash")
	code84, err := shellActivation("bash", "8.4")
	if err != nil {
		t.Fatal(err)
	}
	// Reactivation (and version switch) while PATH is solely the old bin:
	// the old entry must be fully removed, not duplicated. NIXCP_PHP_BIN
	// is set as any prior activation leaves it.
	script := strings.TrimRight(code84, "\n") + `; printf '%s\n' "$PATH"`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{"PATH=/etc/nixcp/php/8.3/bin", "NIXCP_PHP_BIN=/etc/nixcp/php/8.3/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash rejected reactivation: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if n := strings.Count(got, "/etc/nixcp/php/8.3/bin"); n != 0 {
		t.Fatalf("old bin must be gone after switch, found %d: %q", n, got)
	}
	if got != "/etc/nixcp/php/8.4/bin" {
		t.Fatalf("expected exactly the new bin, got %q", got)
	}
}

// TestShellActivationExecutesInFish runs the fish activation and asserts
// the global exported variables.
func TestShellActivationExecutesInFish(t *testing.T) {
	skipWithoutShell(t, "fish")
	code, err := shellActivation("fish", "8.4")
	if err != nil {
		t.Fatal(err)
	}
	script := code + `; printf '%s\n%s\n%s\n' $NIXCP_PHP_VERSION $NIXCP_PHP_BIN $PATH`
	cmd := exec.Command("fish", "-c", script)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish rejected activation code: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 output lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "8.4" || lines[1] != "/etc/nixcp/php/8.4/bin" {
		t.Fatalf("fish env not exported as promised: version=%q bin=%q", lines[0], lines[1])
	}
	if !strings.HasPrefix(lines[2], "/etc/nixcp/php/8.4/bin") {
		t.Fatalf("fish PATH not prepended, got %q", lines[2])
	}
}

// TestShellSnippetExecutesAndDelegatesInBash installs the real wrapper
// function in a real bash and verifies: (1) a 3-arg `ncp php use X` call
// delegates to the --shell-emit protocol, sourcing the activation and
// exporting the variables in the same shell; (2) any other invocation
// falls through to `command ncp`.
func TestShellSnippetExecutesAndDelegatesInBash(t *testing.T) {
	skipWithoutShell(t, "bash")
	snippet, err := shellSnippet("bash")
	if err != nil {
		t.Fatal(err)
	}
	// Build a fake `ncp` on PATH that records argv and emits activation.
	bin := t.TempDir()
	fake := filepath.Join(bin, "ncp")
	emit := "NIXCP_PHP_VERSION='9.9'; NIXCP_PHP_BIN='/etc/nixcp/php/9.9/bin'; PATH=\"$NIXCP_PHP_BIN:$PATH\"; export NIXCP_PHP_VERSION NIXCP_PHP_BIN PATH"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = php ] && [ \"$2\" = use ] && [ \"$3\" = 9.9 ] && [ -z \"$SHELL_EMIT_PROBE\" ]; then printf '%s\\n' '"+emit+"'; exit 0; fi\necho \"delegated:$*\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	script := snippet + `
ncp php use 9.9
printf 'after-wrapper:%s|%s\n' "$NIXCP_PHP_VERSION" "$NIXCP_PHP_BIN"
ncp status
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash rejected wrapper snippet: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "after-wrapper:9.9|/etc/nixcp/php/9.9/bin") {
		t.Fatalf("wrapper did not source activation into the calling shell:\n%s", got)
	}
	if !strings.Contains(got, "delegated:status") {
		t.Fatalf("non-use invocation did not delegate to command ncp:\n%s", got)
	}
}

// TestShellSnippetExecutesInFish mirrors the bash wrapper test for fish.
func TestShellSnippetExecutesInFish(t *testing.T) {
	skipWithoutShell(t, "fish")
	snippet, err := shellSnippet("fish")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fake := filepath.Join(bin, "ncp")
	emit := "set -gx NIXCP_PHP_VERSION '7.7'\nset -gx NIXCP_PHP_BIN '/etc/nixcp/php/7.7/bin'\nset -gx PATH $NIXCP_PHP_BIN $PATH"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = php ] && [ \"$2\" = use ] && [ \"$3\" = 7.7 ]; then printf '%s\\n' '"+emit+"'; exit 0; fi\necho \"delegated:$*\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	script := snippet + `
ncp php use 7.7
printf 'after-wrapper:%s|%s\n' $NIXCP_PHP_VERSION $NIXCP_PHP_BIN
ncp status
`
	cmd := exec.Command("fish", "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish rejected wrapper snippet: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "after-wrapper:7.7|/etc/nixcp/php/7.7/bin") {
		t.Fatalf("fish wrapper did not source activation into the calling shell:\n%s", got)
	}
	if !strings.Contains(got, "delegated:status") {
		t.Fatalf("fish non-use invocation did not delegate to command ncp:\n%s", got)
	}
}
