package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalWriteHasStableBytesAndQuotedPHP(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	cfg := validConfig(home)
	cfg.PHP.Installed = []string{"8.4", "8.3"}
	cfg.PHP.Extensions = []string{"redis", "intl"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(store.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "- \"8.3\"") || !strings.Contains(string(first), "- \"8.4\"") {
		t.Fatalf("PHP versions must remain YAML strings: %s", first)
	}
	if err := store.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(store.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical writes differ\n%s\n%s", first, second)
	}
}

func TestLoadRejectsSiteSymlinkAndUnexpectedEntries(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	if err := store.Initialize(validConfig(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store.ConfigPath(), filepath.Join(store.SitesPath(), "bad.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected symlink rejection")
	}
	if err := os.Remove(filepath.Join(store.SitesPath(), "bad.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.SitesPath(), "notes.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected unmanaged entry rejection")
	}
}

func TestStrictYAMLRejectsMultipleDocumentsAndNonMapping(t *testing.T) {
	for _, raw := range [][]byte{[]byte("---\n[]\n"), []byte("schemaVersion: 2\n---\nschemaVersion: 2\n")} {
		if _, err := NormalizeAndValidateConfig(raw); err == nil {
			t.Fatalf("expected strict document rejection: %q", raw)
		}
	}
}

func TestValidateRejectsUnsupportedHandlersAndPHPVersions(t *testing.T) {
	site := SiteConfig{SchemaVersion: 2, ID: "x", Domain: "example.com", ProjectPath: "/srv/x", DocumentRoot: "/srv/x", PHP: "7.4", Nginx: NginxConfig{Handler: HandlerConfig{Type: "generic"}}}
	if err := ValidateSite(site); err == nil {
		t.Fatal("expected unsupported handler/version rejection")
	}
}

func FuzzNormalizeDomain(f *testing.F) {
	for _, seed := range []string{"Example.COM.", "bücher.example", "https://example.com", "bad..example"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if domain, err := NormalizeDomain(raw); err == nil {
			if domain == "" || domain != strings.ToLower(domain) || strings.HasSuffix(domain, ".") {
				t.Fatalf("non-canonical domain %q", domain)
			}
		}
	})
}
