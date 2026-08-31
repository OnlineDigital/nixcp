package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSiteRejectsDocumentRootOutsideProject(t *testing.T) {
	project, outside := t.TempDir(), t.TempDir()
	site := SiteConfig{SchemaVersion: 2, ID: "example", Domain: "example.test", ProjectPath: project, DocumentRoot: outside, PHP: "8.3", Nginx: NginxConfig{Handler: HandlerConfig{Type: "generic"}}}
	if err := ValidateSite(site); err == nil || !strings.Contains(err.Error(), "documentRoot") {
		t.Fatalf("expected root containment rejection, got %v", err)
	}
}

func TestValidateSiteRejectsOversizedCustomSnippet(t *testing.T) {
	project := t.TempDir()
	snippet := filepath.Join(project, "snippet.conf")
	if err := os.WriteFile(snippet, make([]byte, maxCustomSnippetBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	site := SiteConfig{SchemaVersion: 2, ID: "example", Domain: "example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: NginxConfig{Handler: HandlerConfig{Type: "custom", Path: snippet}}}
	if err := ValidateSite(site); err == nil {
		t.Fatal("expected oversized snippet rejection")
	}
}

func TestValidateSiteRejectsUnsafeCustomSnippetContent(t *testing.T) {
	project := t.TempDir()
	snippet := filepath.Join(project, "snippet.conf")
	if err := os.WriteFile(snippet, []byte("include /tmp/evil.conf;"), 0600); err != nil {
		t.Fatal(err)
	}
	site := SiteConfig{SchemaVersion: 2, ID: "example", Domain: "example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: NginxConfig{Handler: HandlerConfig{Type: "custom", Path: snippet}}}
	if err := ValidateSite(site); err == nil {
		t.Fatal("expected unsafe custom snippet rejection")
	}
}

func TestValidateSiteRejectsUnsafeLoadedCustomSnippetContent(t *testing.T) {
	project := t.TempDir()
	snippet := filepath.Join(project, "snippet.conf")
	if err := os.WriteFile(snippet, []byte("try_files $uri =404;"), 0600); err != nil {
		t.Fatal(err)
	}
	site := SiteConfig{SchemaVersion: 2, ID: "example", Domain: "example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: NginxConfig{Handler: HandlerConfig{Type: "custom", Path: snippet, Content: "server { listen 80; }"}}}
	if err := ValidateSite(site); err == nil {
		t.Fatal("expected unsafe in-memory custom snippet rejection")
	}
}

func TestStrictYAMLRejectsExcessiveNesting(t *testing.T) {
	raw := []byte("schemaVersion:")
	for range 40 {
		raw = append(raw, []byte("\n  x:")...)
	}
	raw = append(raw, []byte("  1\n")...)
	if _, err := NormalizeAndValidateConfig(raw); err == nil {
		t.Fatal("expected deeply nested YAML rejection")
	}
}
