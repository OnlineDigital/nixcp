package state

import (
	"strings"
	"testing"
)

// The declarative structural layer (go-playground/validator) runs on every
// parsed config.yaml / sites/*.yaml inside NormalizeAndValidate{Config,Site}.
// These tests pin its contract: canonicalized documents must satisfy the
// structural tags, failures surface as stable invalid_config/invalid_site
// StateError codes, and the hand-written semantic rules keep working.

func validConfigYAML(home string) string {
	return `schemaVersion: 1
owner:
  username: alice
  uid: 1000
  group: users
  gid: 100
  home: ` + home + `
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
  redis:
    installed: false
    desiredState: stopped
php:
  installed: ["8.3"]
  extensions: []
  globalDefault: null
mariadbRegistry:
  databases: []
`
}

func TestStructuralConfigAcceptsCanonicalDocument(t *testing.T) {
	cfg, err := NormalizeAndValidateConfig([]byte(validConfigYAML(t.TempDir())))
	if err != nil {
		t.Fatalf("expected structural validation to pass, got %v", err)
	}
	if cfg.Owner.Username != "alice" {
		t.Fatalf("unexpected owner %q", cfg.Owner.Username)
	}
}

func TestStructuralConfigRejectsEmptyOwnerAfterCanonicalize(t *testing.T) {
	doc := validConfigYAML(t.TempDir())
	doc = strings.Replace(doc, "username: alice", "username: \"\"", 1)
	_, err := NormalizeAndValidateConfig([]byte(doc))
	if err == nil {
		t.Fatalf("expected structural rejection of empty username")
	}
	if !strings.Contains(err.Error(), "invalid_config") {
		t.Fatalf("expected invalid_config error code, got %v", err)
	}
}

func TestStructuralConfigRejectsUnknownRebuildMode(t *testing.T) {
	doc := validConfigYAML(t.TempDir())
	doc = strings.Replace(doc, "mode: traditional", "mode: swap", 1)
	_, err := NormalizeAndValidateConfig([]byte(doc))
	if err == nil {
		t.Fatalf("expected structural rejection of rebuild mode")
	}
	if !strings.Contains(err.Error(), "invalid_config") {
		t.Fatalf("expected invalid_config error code, got %v", err)
	}
}

func TestStructuralConfigRejectsRelativeHome(t *testing.T) {
	doc := validConfigYAML(t.TempDir())
	doc = strings.Replace(doc, "home: ", "home: relative/path # ", 1)
	_, err := NormalizeAndValidateConfig([]byte(doc))
	if err == nil {
		t.Fatalf("expected structural rejection of relative home")
	}
	if !strings.Contains(err.Error(), "invalid_config") {
		t.Fatalf("expected invalid_config error code, got %v", err)
	}
}

func TestStructuralConfigAcceptsRootHome(t *testing.T) {
	// A persisted config may legitimately carry owner.home = "/" (the
	// root account); structural validation must not reject it, or an
	// existing installation would read back as unconfigured.
	doc := validConfigYAML(t.TempDir())
	// Replace whatever home path the fixture carries with the root home.
	lines := strings.Split(doc, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "  home: ") {
			lines[i] = "  home: /"
			break
		}
	}
	doc = strings.Join(lines, "\n")
	if !strings.Contains(doc, "\n  home: /\n") {
		t.Fatal("fixture did not carry a home line to replace")
	}
	cfg, err := NormalizeAndValidateConfig([]byte(doc))
	if err != nil {
		t.Fatalf("root home must remain loadable: %v", err)
	}
	if cfg.Owner.Home != "/" {
		t.Fatalf("expected canonical home /, got %q", cfg.Owner.Home)
	}
}

func TestStructuralSiteRejectsEmptyPHPAndBadHandlerType(t *testing.T) {
	base := func(handler string) string {
		return `schemaVersion: 1
id: app-example-com
enabled: true
domain: app.example.com
projectPath: ` + t.TempDir() + `
documentRoot: ` + t.TempDir() + `
php: "8.3"
nginx:
  handler:
` + handler
	}
	// Schema-declared PHP version that canonicalization cannot invent.
	missingPHP := strings.Replace(base("    type: generic\n"), `php: "8.3"`, `php: ""`, 1)
	if _, err := NormalizeAndValidateSite([]byte(missingPHP)); err == nil {
		t.Fatalf("expected structural rejection of empty php")
	}

	unknownHandler := base("    type: proxypass\n")
	_, err := NormalizeAndValidateSite([]byte(unknownHandler))
	if err == nil {
		t.Fatalf("expected structural rejection of unknown handler type")
	}
	if !strings.Contains(err.Error(), "invalid_site") {
		t.Fatalf("expected invalid_site error code, got %v", err)
	}
}

func TestStructuralSiteAcceptsAllHandlerTypes(t *testing.T) {
	tmp := t.TempDir()
	for _, handler := range []string{
		"    type: generic\n",
		"    type: template\n    name: laravel\n",
	} {
		doc := `schemaVersion: 1
id: app-example-com
enabled: true
domain: app.example.com
projectPath: ` + tmp + `
documentRoot: ` + tmp + `
php: "8.3"
nginx:
  handler:
` + handler
		if _, err := NormalizeAndValidateSite([]byte(doc)); err != nil {
			t.Fatalf("expected handler %q to pass structural validation, got %v", handler, err)
		}
	}
}

func TestSemanticRulesStillRunAfterStructuralLayer(t *testing.T) {
	// Structure is fine; PHP version is not in the allowlist — the
	// hand-written semantic layer must still reject it with its own code.
	doc := `schemaVersion: 1
id: app-example-com
enabled: true
domain: app.example.com
projectPath: ` + t.TempDir() + `
documentRoot: ` + t.TempDir() + `
php: "7.4"
nginx:
  handler:
    type: generic
`
	_, err := NormalizeAndValidateSite([]byte(doc))
	if err == nil {
		t.Fatalf("expected semantic rejection of unsupported PHP version")
	}
	if !strings.Contains(err.Error(), "php") {
		t.Fatalf("expected php-related semantic error, got %v", err)
	}
}
