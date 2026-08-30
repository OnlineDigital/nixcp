package nix

import (
	"strings"
	"testing"
)

func FuzzNixString(f *testing.F) {
	for _, seed := range []string{"", "plain", `quote " slash \\ ${pkgs.x}`, "line\n\r\ttab", "emoji ✓"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if strings.ContainsRune(input, '\x00') {
			t.Skip()
		}
		got := nixString(input)
		if len(got) < 2 || got[0] != '"' || got[len(got)-1] != '"' {
			t.Fatalf("not a quoted Nix string: %q", got)
		}
		for i := 0; i+1 < len(got); i++ {
			if got[i] == '$' && got[i+1] == '{' && (i == 0 || got[i-1] != '\\') {
				t.Fatalf("unescaped interpolation in %q", got)
			}
		}
		if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
			t.Fatalf("literal newline escaped incorrectly: %q", got)
		}
	})
}
