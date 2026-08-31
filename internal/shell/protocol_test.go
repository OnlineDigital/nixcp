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

func TestZshActivationSplitsPathExplicitly(t *testing.T) {
	zsh, err := Activation("zsh", "8.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(zsh, "${(s.:.)PATH}") {
		t.Fatalf("zsh activation must split PATH explicitly via ${(s.:.)PATH}, got: %s", zsh)
	}
	if strings.Contains(zsh, "for _nixcp_p in $PATH") {
		t.Fatalf("zsh activation must not rely on unquoted $PATH word-splitting, got: %s", zsh)
	}
	bash, err := Activation("bash", "8.3")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bash, "${(s.:.)PATH}") {
		t.Fatalf("bash activation must keep the IFS loop idiom, got: %s", bash)
	}
}

func TestBootstrapEmitsDefaultCapture(t *testing.T) {
	for _, name := range []string{"bash", "zsh", "fish"} {
		b, err := Bootstrap(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b, "ncp php session --shell-emit="+name) {
			t.Fatalf("%s bootstrap must invoke the session subcommand, got: %s", name, b)
		}
		if !strings.Contains(b, "NIXCP_PHP_VERSION") {
			t.Fatalf("%s bootstrap must guard on NIXCP_PHP_VERSION, got: %s", name, b)
		}
	}
}

func TestStartupCombinesWrapperAndBootstrap(t *testing.T) {
	snippet, err := Snippet("bash")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := Bootstrap("bash")
	if err != nil {
		t.Fatal(err)
	}
	startup, err := Startup("bash")
	if err != nil {
		t.Fatal(err)
	}
	if startup != snippet+bootstrap {
		t.Fatalf("Startup must be Snippet + Bootstrap verbatim")
	}
	if !strings.Contains(startup, "ncp()") || !strings.Contains(startup, "php session") {
		t.Fatalf("Startup must include both the wrapper and the default capture, got: %s", startup)
	}
}
