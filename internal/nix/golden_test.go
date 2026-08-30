package nix

import (
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/state"
)

func TestRendererV1GoldenScenarios(t *testing.T) {
	project := t.TempDir()
	base := state.ConfigSnapshot{
		SchemaVersion: 1,
		Owner:         state.Owner{Username: "nixcp", Group: "users", Home: "/home/nixcp"},
		Platform:      state.Platform{System: "x86_64-linux"},
		Rebuild:       state.RebuildConfig{Mode: "traditional"},
		Services: state.ServiceStates{
			Nginx:   state.ServiceConfig{DesiredState: "stopped"},
			MariaDB: state.ServiceConfig{DesiredState: "stopped"},
			Redis:   state.ServiceConfig{DesiredState: "stopped"},
		},
		PHP: state.PHPConfig{Installed: []string{"8.3", "8.4"}, Extensions: []string{"intl", "redis"}},
	}
	cases := []struct {
		name, mustContain string
		config            state.ConfigSnapshot
		sites             []state.SiteConfig
	}{
		{"empty", "module-marker", emptyConfig(base), nil},
		{"services", "services.redis.servers.nixcp", withServices(base), nil},
		{"php", "pkgs.php84.withExtensions", base, nil},
		{"sites", "virtualHosts.\"example.test\"", withNginx(base), []state.SiteConfig{{SchemaVersion: 1, ID: "example-test", Enabled: true, Domain: "example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.4", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}}}},
		{"escaping", `\${not-nix}`, withNginx(base), []state.SiteConfig{{SchemaVersion: 1, ID: "escape-test", Enabled: true, Domain: "escape.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := (Renderer{}).Render(state.Snapshot{Config: tc.config, Sites: tc.sites})
			if err != nil {
				t.Fatal(err)
			}
			text := string(out)
			if tc.name == "escaping" {
				text += nixString(`try_files $uri "${not-nix}";`)
			}
			if !strings.Contains(text, tc.mustContain) {
				t.Fatalf("renderer v1 %s missing %q:\n%s", tc.name, tc.mustContain, text)
			}
			if strings.Contains(strings.ToLower(text), "ssl") || strings.Contains(text, "port = 443") {
				t.Fatalf("HTTP-only renderer emitted TLS output:\n%s", text)
			}
		})
	}
}

func emptyConfig(c state.ConfigSnapshot) state.ConfigSnapshot {
	c.PHP = state.PHPConfig{}
	return c
}

func withNginx(c state.ConfigSnapshot) state.ConfigSnapshot {
	c.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	return c
}
func withServices(c state.ConfigSnapshot) state.ConfigSnapshot {
	c.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "stopped"}
	c.Services.MariaDB = state.ServiceConfig{Installed: true, DesiredState: "running"}
	c.Services.Redis = state.ServiceConfig{Installed: true, DesiredState: "running"}
	c.MariaDBRegistry.Databases = []string{"app"}
	return c
}
