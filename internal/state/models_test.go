package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDomainAcceptsAsciiAndRejectsBadChars(t *testing.T) {
	if got, err := NormalizeDomain("Example.COM."); err != nil {
		t.Fatalf("expected normalize ok, got err: %v", err)
	} else if got != "example.com" {
		t.Fatalf("expected normalized domain, got %q", got)
	}

	if _, err := NormalizeDomain("https://example.com"); err == nil {
		t.Fatalf("expected error for URL scheme")
	}
	if _, err := NormalizeDomain("exa mple.com"); err == nil {
		t.Fatalf("expected error for whitespace")
	}
}

func TestNormalizeDomainRejectsPathAndPort(t *testing.T) {
	for _, input := range []string{"example.com/path", "example.com:8080", "*.example.com", "bad..com"} {
		if _, err := NormalizeDomain(input); err == nil {
			t.Fatalf("expected error for domain %q", input)
		}
	}
}

func TestGenerateStableSiteIDDeterministicAndCollisionSafe(t *testing.T) {
	base := GenerateStableSiteID("example.com", nil)
	if base == "" {
		t.Fatalf("expected base id")
	}
	taken := map[string]struct{}{base: {}, "site": {}, "example-com": {}}
	other := GenerateStableSiteID("example.com", taken)
	if other == base {
		t.Fatalf("expected collision suffix")
	}
}

func TestCanonicalConfigSortsAndDedupes(t *testing.T) {
	cfg := ConfigSnapshot{
		SchemaVersion: 2,
		Owner:         Owner{Username: " alice ", Home: "/tmp/home", Group: "g", UID: 1, GID: 1},
		Platform:      Platform{System: ""},
		Rebuild:       RebuildConfig{Mode: ""},
		Services: ServiceStates{
			Nginx:   ServiceConfig{DesiredState: "RUNNING", Installed: true},
			MariaDB: ServiceConfig{DesiredState: "stopped", Installed: false},
			Valkey:  ServiceConfig{DesiredState: "STOPPED", Installed: false},
		},
		PHP: PHPConfig{Installed: []string{"8.1", "8.0", "8.1"}, Extensions: []string{"xsl", "xsl", "intl"}},
	}
	cfg.Canonicalize()
	if cfg.Owner.Username != "alice" {
		t.Fatalf("expected trimmed username, got %q", cfg.Owner.Username)
	}
	if cfg.Platform.System != "x86_64-linux" {
		t.Fatalf("expected default system, got %q", cfg.Platform.System)
	}
	if len(cfg.PHP.Installed) != 3 || cfg.PHP.Installed[0] != "8.0" || cfg.PHP.Installed[1] != "8.1" {
		t.Fatalf("unexpected sorted installed versions: %#v", cfg.PHP.Installed)
	}
	if len(cfg.PHP.Extensions) != 3 {
		t.Fatalf("canonicalization must preserve duplicates for validation: %#v", cfg.PHP.Extensions)
	}
}

func TestValidateConfigRejectsUnsupportedSchema(t *testing.T) {
	cfg := ConfigSnapshot{
		SchemaVersion: 3,
		Owner:         Owner{Username: "u", Home: "/tmp", Group: "g", UID: 1, GID: 1},
		Platform:      Platform{System: "x86_64-linux"},
		Rebuild:       RebuildConfig{Mode: "traditional"},
		Services: ServiceStates{
			Nginx:   ServiceConfig{DesiredState: "stopped"},
			MariaDB: ServiceConfig{DesiredState: "stopped"},
			Valkey:  ServiceConfig{DesiredState: "stopped"},
		},
		PHP: PHPConfig{},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatalf("expected validation failure")
	}
}

func TestValidateConfigRejectsGlobalDefaultAndExtensions(t *testing.T) {
	cfg := ConfigSnapshot{
		SchemaVersion: 2,
		Owner:         Owner{Username: "u", Home: "/tmp", Group: "g", UID: 1, GID: 1},
		Platform:      Platform{System: "x86_64-linux"},
		Rebuild:       RebuildConfig{Mode: "traditional"},
		Services: ServiceStates{
			Nginx:   ServiceConfig{Installed: true, DesiredState: "stopped"},
			MariaDB: ServiceConfig{Installed: false, DesiredState: "stopped"},
			Valkey:  ServiceConfig{Installed: false, DesiredState: "stopped"},
		},
		PHP: PHPConfig{Installed: []string{"8.1", "8.1"}, GlobalDefault: "8.2", Extensions: []string{"wrong-"}},
	}
	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatalf("expected duplicate/globaldefault/ext validation error")
	}
}

func TestValidateAndNormalizeSiteTemplateAndCustom(t *testing.T) {
	tmpDir := t.TempDir()
	custom := filepath.Join(tmpDir, "site.conf")
	if err := os.WriteFile(custom, []byte("# x"), 0644); err != nil {
		t.Fatal(err)
	}

	site := SiteConfig{SchemaVersion: 2, ID: "a", Domain: "example.com", ProjectPath: tmpDir, DocumentRoot: tmpDir, PHP: "8.3", Nginx: NginxConfig{Handler: HandlerConfig{Type: "template", Name: "wordpress"}}}
	if err := ValidateSite(site); err != nil {
		t.Fatalf("expected valid template handler site, got %v", err)
	}

	site2 := site
	site2.ID = "b"
	site2.Nginx.Handler = HandlerConfig{Type: "custom", Path: custom}
	if err := ValidateSite(site2); err != nil {
		t.Fatalf("expected valid custom handler site, got %v", err)
	}
}

func TestNormalizeAndValidateRoundTripConfig(t *testing.T) {
	raw := []byte(`
schemaVersion: 2
owner:
  username: alice
  uid: 1000
  group: users
  gid: 100
  home: /home/alice
platform:
  system: x86_64-linux
rebuild:
  mode: traditional
  target: null
  impure: false
  importConfirmed: false
services:
  nginx:
    installed: false
    desiredState: stopped
  mariadb:
    installed: false
    desiredState: stopped
  valkey:
    installed: false
    desiredState: stopped
php:
  installed: []
  extensions: []
`)
	cfg, err := NormalizeAndValidateConfig(raw)
	if err != nil {
		t.Fatalf("expected valid config round-trip, got %v", err)
	}
	if cfg.SchemaVersion != 2 {
		t.Fatalf("unexpected schema version: %d", cfg.SchemaVersion)
	}
}

func TestNormalizeDomainRejectsIPv6Literals(t *testing.T) {
	if _, err := NormalizeDomain("[::1]"); err == nil {
		t.Fatalf("expected ipv6 literal rejection")
	}
}
