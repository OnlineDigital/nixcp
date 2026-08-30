package nix

import (
	"github.com/nixcp/nixcp/internal/state"
	"strings"
	"testing"
)

func TestNixStringEscapesAndContainsInterpolation(t *testing.T) {
	got := nixString("quote \" slash \\ interpolation ${pkgs.x}\n")
	want := "\"quote \\\" slash \\\\ interpolation \\${pkgs.x}\\n\""
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderIsDeterministicAndHTTPOnly(t *testing.T) {
	path := t.TempDir()
	c := state.ConfigSnapshot{SchemaVersion: 1, Owner: state.Owner{Username: "u", Group: "g", Home: "/tmp/u"}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{Installed: true, DesiredState: "running"}, MariaDB: state.ServiceConfig{DesiredState: "stopped"}, Redis: state.ServiceConfig{DesiredState: "stopped"}}, PHP: state.PHPConfig{Installed: []string{"8.3"}}}
	s := state.Snapshot{Config: c, Sites: []state.SiteConfig{{SchemaVersion: 1, ID: "example-com", Enabled: true, Domain: "example.com", ProjectPath: path, DocumentRoot: path, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}}}}
	r := Renderer{}
	a, e := r.Render(s)
	if e != nil {
		t.Fatal(e)
	}
	b, e := r.Render(s)
	if e != nil || string(a) != string(b) {
		t.Fatalf("not deterministic: %v", e)
	}
	text := string(a)
	if !strings.Contains(text, Marker) || !strings.Contains(text, "port = 80") || strings.Contains(strings.ToLower(text), "ssl") {
		t.Fatalf("unexpected module: %s", text)
	}
}
