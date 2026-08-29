package nix

import (
	"github.com/nixcp/nixcp/internal/state"
	"strings"
	"testing"
)

func TestRenderIsDeterministicAndHTTPOnly(t *testing.T) {
	c := state.ConfigSnapshot{SchemaVersion: 1, Owner: state.Owner{Username: "u", Group: "g", Home: "/tmp/u"}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{Installed: true, DesiredState: "running"}, MariaDB: state.ServiceConfig{DesiredState: "stopped"}, Redis: state.ServiceConfig{DesiredState: "stopped"}}, PHP: state.PHPConfig{Installed: []string{"8.3"}}}
	s := state.Snapshot{Config: c, Sites: []state.SiteConfig{{SchemaVersion: 1, ID: "example-com", Enabled: true, Domain: "example.com", ProjectPath: "/srv/p", DocumentRoot: "/srv/p/public", PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}}}}
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
