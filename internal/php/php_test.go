package php

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nixcp/nixcp/internal/state"
)

func TestCatalogOnlyAcceptsExplicitVersionsAndExtensions(t *testing.T) {
	if _, err := NormalizeVersion("8.2"); err == nil {
		t.Fatal("8.2 must not be accepted")
	}
	if v, err := NormalizeVersion("8.3"); err != nil || v != "8.3" {
		t.Fatalf("unexpected version result %q, %v", v, err)
	}
	if _, err := NormalizeExtension("imagick"); err == nil {
		t.Fatal("non-catalog extension must be rejected")
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
