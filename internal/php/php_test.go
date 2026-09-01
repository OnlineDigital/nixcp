package php

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nixcp/nixcp/internal/state"
)

func TestCatalogOnlyAcceptsExplicitVersionsAndExtensions(t *testing.T) {
	for _, version := range []string{"8.2", "8.3", "8.4", "8.5"} {
		if got, err := NormalizeVersion(version); err != nil || got != version {
			t.Fatalf("unexpected version result %q, %v", got, err)
		}
	}
	if _, err := NormalizeVersion("8.1"); err == nil {
		t.Fatal("8.1 must not be accepted")
	}
	for _, extension := range []string{"imagick", "redis", "xdebug"} {
		if got, err := NormalizeExtension(extension); err != nil || got != extension {
			t.Fatalf("unexpected extension result %q, %v", got, err)
		}
	}
	if _, err := NormalizeExtension("not-a-real-extension"); err == nil {
		t.Fatal("non-catalog extension must be rejected")
	}
	if _, ok := Compatible("8.5", "opcache"); ok {
		t.Fatal("PHP 8.5 must not request nixpkgs' removed opcache extension attribute")
	}
}
func TestResolvePrecedenceAndInstalledRequirement(t *testing.T) {
	d := t.TempDir()
	child := filepath.Join(d, "a", "b")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ".php-version"), []byte("8.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := state.PHPConfig{Installed: []string{"8.3", "8.4"}, GlobalDefault: "8.4"}
	if got, err := Resolve(child, cfg, "8.4"); err != nil || got != "8.3" {
		t.Fatalf("marker should win: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(d, ".php-version")); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(child, cfg, "8.3"); err != nil || got != "8.3" {
		t.Fatalf("env should win: %q %v", got, err)
	}
	if got, err := Resolve(child, cfg, ""); err != nil || got != "8.4" {
		t.Fatalf("default should win: %q %v", got, err)
	}
	cfg.Installed = []string{"8.3"}
	if _, err := Resolve(child, cfg, ""); err == nil {
		t.Fatal("uninstalled default must fail")
	}
}
func TestCompatibilityWarningsAreStructured(t *testing.T) {
	old := Catalog["8.3"]
	Catalog["8.3"] = Version{Version: "8.3", Nixpkgs: "php83", Extensions: map[string]string{}}
	defer func() { Catalog["8.3"] = old }()
	w := CompatibilityWarnings([]string{"8.3"}, []string{"redis"})
	if len(w) != 1 || w[0].Code != "php_extension_incompatible" {
		t.Fatalf("unexpected warnings: %#v", w)
	}
}
