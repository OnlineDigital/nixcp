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
	c := state.ConfigSnapshot{SchemaVersion: 2, Owner: state.Owner{Username: "u", Group: "g", Home: "/tmp/u"}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{Installed: true, DesiredState: "running"}, MariaDB: state.ServiceConfig{DesiredState: "stopped"}, Valkey: state.ServiceConfig{DesiredState: "stopped"}}, PHP: state.PHPConfig{Installed: []string{"8.3"}}}
	s := state.Snapshot{Config: c, Sites: []state.SiteConfig{{SchemaVersion: 2, ID: "example-com", Enabled: true, Domain: "example.com", ProjectPath: path, DocumentRoot: path, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}}}}
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

func TestMariaDBAccountsSQL(t *testing.T) {
	const pw = "nixcp-fixture-password-123456789"
	sites := []state.SiteConfig{
		{SchemaVersion: 2, ID: "a", Enabled: true, Domain: "a.test", ProjectPath: "/srv/a", DocumentRoot: "/srv/a", PHP: "8.3", MariaDB: &state.MariaDBConfig{Database: "app", User: "app", Password: pw}, Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}},
		{SchemaVersion: 2, ID: "b", Enabled: true, Domain: "b.test", ProjectPath: "/srv/b", DocumentRoot: "/srv/b", PHP: "8.3", MariaDB: &state.MariaDBConfig{Database: "app", User: "app", Password: pw}, Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}},
		{SchemaVersion: 2, ID: "c", Enabled: true, Domain: "c.test", ProjectPath: "/srv/c", DocumentRoot: "/srv/c", PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}},
	}
	got := MariaDBAccountsSQL(state.ConfigSnapshot{}, sites)
	if !strings.Contains(got, "CREATE USER IF NOT EXISTS 'app'@'localhost' IDENTIFIED BY '"+pw+"'") {
		t.Fatalf("SQL missing CREATE USER with password: %s", got)
	}
	if !strings.Contains(got, "GRANT ALL ON `app`.* TO 'app'@'localhost'") {
		t.Fatalf("SQL missing GRANT: %s", got)
	}
	// Two sites sharing a database must produce one CREATE/ALTER/GRANT pair.
	if n := strings.Count(got, "CREATE USER"); n != 1 {
		t.Fatalf("expected exactly 1 CREATE USER for the deduped database, got %d: %s", n, got)
	}
	// No site with a database yields empty SQL.
	if empty := MariaDBAccountsSQL(state.ConfigSnapshot{}, []state.SiteConfig{sites[2]}); empty != "" {
		t.Fatalf("expected empty SQL when no site declares a database, got: %q", empty)
	}
}

func TestRenderMariaDBAccountsNeverLeaksPassword(t *testing.T) {
	const pw = "nixcpfixturesecret9876543210"
	c := state.ConfigSnapshot{SchemaVersion: 2, Owner: state.Owner{Username: "nixcp", Group: "nixcp", Home: "/home/nixcp"}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{Installed: true, DesiredState: "running"}, MariaDB: state.ServiceConfig{Installed: true, DesiredState: "running"}, Valkey: state.ServiceConfig{DesiredState: "stopped"}}, PHP: state.PHPConfig{Installed: []string{"8.3", "8.4"}}, MariaDBRegistry: state.MariaDBRegistry{Databases: []string{"app"}}}
	s := state.Snapshot{Config: c, Sites: []state.SiteConfig{{SchemaVersion: 2, ID: "example-test", Enabled: true, Domain: "example.test", ProjectPath: "/home", DocumentRoot: "/home", PHP: "8.4", MariaDB: &state.MariaDBConfig{Database: "app", User: "app", Password: pw}, Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}}}}
	b, e := (Renderer{}).Render(s)
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	if strings.Contains(text, pw) {
		t.Fatalf("rendered module leaked the MariaDB password")
	}
	if !strings.Contains(text, "/home/nixcp/.nixcp/secrets/mariadb/accounts.sql") {
		t.Fatalf("module must reference the private accounts.sql path: %s", text)
	}
	if !strings.Contains(text, "nixcp-mariadb-accounts sha256=") {
		t.Fatalf("module must carry a deterministic SQL digest to drive rotation: %s", text)
	}
}
