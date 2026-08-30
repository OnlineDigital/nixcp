package shell

import (
	"strings"
	"testing"
)

func TestSupportedShellsAndProtocolRejection(t *testing.T) {
	for _, name := range []string{"bash", "zsh", "fish"} {
		if !Supported(name) {
			t.Fatalf("expected %s to be supported", name)
		}
		if _, err := Snippet(name); err != nil {
			t.Fatalf("snippet %s: %v", name, err)
		}
	}
	if Supported("sh") {
		t.Fatal("plain sh must not be advertised as supported")
	}
	if _, err := Activation("sh", "8.3"); err == nil {
		t.Fatal("expected unsupported activation to fail")
	}
}

func TestActivationQuotesVersionAndSelectsMatchingBin(t *testing.T) {
	code, err := Activation("bash", "8.3")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NIXCP_PHP_VERSION='8.3'", "NIXCP_PHP_BIN='/etc/nixcp/php/8.3/bin'"} {
		if !strings.Contains(code, want) {
			t.Fatalf("activation missing %q: %s", want, code)
		}
	}
}
