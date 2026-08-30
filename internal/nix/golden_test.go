package nix

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nixcp/nixcp/internal/state"
)

// goldenProject is a fixed, always-existing path: Render validates that site
// paths exist on disk (os.Lstat via state.ValidateSite), so byte-stable
// fixtures cannot use t.TempDir() paths.
const goldenProject = "/home"

func TestRendererV1GoldenScenarios(t *testing.T) {
	base := state.ConfigSnapshot{
		SchemaVersion: 2,
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
		{"sites", "virtualHosts.\"example.test\"", withNginx(base), []state.SiteConfig{{SchemaVersion: 2, ID: "example-test", Enabled: true, Domain: "example.test", ProjectPath: goldenProject, DocumentRoot: goldenProject, PHP: "8.4", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}}}},
		{"escaping", `\${not-nix}`, withNginx(base), []state.SiteConfig{{SchemaVersion: 2, ID: "escape-test", Enabled: true, Domain: "escape.test", ProjectPath: goldenProject, DocumentRoot: goldenProject, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := state.Snapshot{Config: tc.config, Sites: tc.sites}
			got, err := (Renderer{}).Render(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			again, err := (Renderer{}).Render(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, again) {
				t.Fatalf("renderer v1 %s is not deterministic across renders", tc.name)
			}
			goldenPath := filepath.Join("testdata", "golden", tc.name+".nix")
			if update := os.Getenv("UPDATE_GOLDEN"); update != "" {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden (run UPDATE_GOLDEN=1 go test ./internal/nix/): %v", err)
			}
			if !bytes.Equal(want, got) {
				t.Fatalf("renderer output drifted from golden %s:\nwant:\n%s\ngot:\n%s", tc.name, want, got)
			}
			text := string(got)
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

// TestGoldenFilesAreCRLFFreeAndUTF8 guards the golden fixtures against editor
// and platform damage: a CR byte or invalid UTF-8 would make every byte-for-byte
// comparison environment-dependent instead of a stable contract.
func TestGoldenFilesAreCRLFFreeAndUTF8(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "golden", "*.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no golden fixtures found under testdata/golden")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.ContainsRune(data, '\r') {
			t.Errorf("%s contains CR bytes; golden fixtures must be LF-only", path)
		}
		if !utf8.Valid(data) {
			t.Errorf("%s is not valid UTF-8", path)
		}
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
