// Package php defines the supported nixpkgs-only PHP catalog and active-version resolver.
package php

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nixcp/nixcp/internal/state"
)

type Version struct {
	Version, Nixpkgs string
	Extensions       map[string]string
}

// Catalog is deliberately explicit: NixCP never guesses attributes or fetches PHP externally.
var Catalog = map[string]Version{
	"8.3": {Version: "8.3", Nixpkgs: "php83", Extensions: map[string]string{"curl": "curl", "intl": "intl", "mbstring": "mbstring", "opcache": "opcache", "pdo_mysql": "pdo_mysql", "redis": "redis"}},
	"8.4": {Version: "8.4", Nixpkgs: "php84", Extensions: map[string]string{"curl": "curl", "intl": "intl", "mbstring": "mbstring", "opcache": "opcache", "pdo_mysql": "pdo_mysql", "redis": "redis"}},
}

func NormalizeVersion(raw string) (string, error) {
	v, err := state.NormalizePHPVersion(raw)
	if err != nil {
		return "", err
	}
	if _, ok := Catalog[v]; !ok {
		return "", fmt.Errorf("unsupported PHP version %s; supported versions are 8.3, 8.4", v)
	}
	return v, nil
}
func NormalizeExtension(raw string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(raw))
	if !state.IsValidExtName(n) {
		return "", fmt.Errorf("invalid PHP extension")
	}
	for _, v := range Catalog {
		if _, ok := v.Extensions[n]; ok {
			return n, nil
		}
	}
	return "", fmt.Errorf("PHP extension %q is not in the nixpkgs catalog", n)
}
func Compatible(version, extension string) (string, bool) {
	v, ok := Catalog[version]
	if !ok {
		return "", false
	}
	a, ok := v.Extensions[extension]
	return a, ok
}
func Binary(version string) string { return "/etc/nixcp/php/" + version + "/bin/php" }

type Warning struct {
	Code       string `json:"code"`
	Extension  string `json:"extension"`
	PHPVersion string `json:"phpVersion"`
	Reason     string `json:"reason"`
}

func CompatibilityWarnings(installed, extensions []string) []Warning {
	var out []Warning
	for _, version := range installed {
		for _, ext := range extensions {
			if _, ok := Compatible(version, ext); !ok {
				out = append(out, Warning{"php_extension_incompatible", ext, version, "extension is unavailable for this nixpkgs PHP version"})
			}
		}
	}
	return out
}

// Resolve searches project markers before session and global configuration.
func Resolve(cwd string, cfg state.PHPConfig, session string) (string, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, ".php-version")
		if b, err := os.ReadFile(path); err == nil {
			v, e := parseMarker(string(b))
			if e != nil {
				return "", fmt.Errorf("invalid .php-version at %s: %w", path, e)
			}
			return installed(v, cfg)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot read .php-version at %s: %w", path, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	if session != "" {
		v, err := NormalizeVersion(session)
		if err != nil {
			return "", fmt.Errorf("invalid NIXCP_PHP_VERSION: %w", err)
		}
		return installed(v, cfg)
	}
	if cfg.GlobalDefault != "" {
		return installed(cfg.GlobalDefault, cfg)
	}
	return "", fmt.Errorf("no active PHP version")
}
func parseMarker(s string) (string, error) {
	if strings.TrimSpace(s) != s && strings.TrimSpace(s)+"\n" != s {
		return "", fmt.Errorf("marker must contain exactly one version")
	}
	if strings.Count(strings.TrimSpace(s), " ") > 0 || strings.Contains(strings.TrimSpace(s), "\n") {
		return "", fmt.Errorf("marker must contain exactly one version")
	}
	return NormalizeVersion(strings.TrimSpace(s))
}
func installed(v string, cfg state.PHPConfig) (string, error) {
	for _, x := range cfg.Installed {
		if x == v {
			return v, nil
		}
	}
	return "", fmt.Errorf("PHP %s is not installed", v)
}
