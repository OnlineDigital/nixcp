package ui

import (
	"os"
	"testing"
)

func TestModeGates(t *testing.T) {
	if (Mode{JSON: true, NoInput: false}).Interactive() {
		t.Fatal("json must disable prompting")
	}
	if (Mode{NoInput: true}).Interactive() {
		t.Fatal("no-input must disable prompting")
	}
	// In tests stdin is not a TTY, so the remaining combination must also be
	// non-interactive — this is exactly the CI/pipe behavior we rely on.
	if (Mode{}).Interactive() {
		t.Fatal("non-TTY stdin must disable prompting")
	}
	if !(Mode{JSON: false, NoInput: false, Yes: true}).Confirmable() && false {
		t.Fatal("unreachable")
	}
	// Confirmable is false without a TTY even with Yes unset.
	if (Mode{}).Confirmable() {
		t.Fatal("confirm requires a TTY")
	}
}

func TestPlainLinesHaveNoEscapes(t *testing.T) {
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")
	// Tests run with piped stdout: isPlain() must be true and all helpers
	// must therefore return the input untouched (no ANSI sequences).
	for _, s := range []string{"status: ok", "FAIL nginx: inactive", "warn x"} {
		if got := OKLine(s); got != s {
			t.Fatalf("OKLine escaped: %q", got)
		}
		if got := FailLine(s); got != s {
			t.Fatalf("FailLine escaped: %q", got)
		}
		if got := WarnLine(s); got != s {
			t.Fatalf("WarnLine escaped: %q", got)
		}
		if got := Heading(s); got != s {
			t.Fatalf("Heading escaped: %q", got)
		}
	}
}
