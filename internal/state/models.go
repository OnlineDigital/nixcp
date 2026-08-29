// Package state owns the strict YAML source of truth stored under ~/.nixcp.
package state

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const supportedSchemaVersion = 1

// ConfigSnapshot is config.yaml. Empty strings encode YAML null for nullable fields.
type ConfigSnapshot struct {
	SchemaVersion int           `yaml:"schemaVersion"`
	Owner         Owner         `yaml:"owner"`
	Platform      Platform      `yaml:"platform"`
	Rebuild       RebuildConfig `yaml:"rebuild"`
	Services      ServiceStates `yaml:"services"`
	PHP           PHPConfig     `yaml:"php"`
}

type Owner struct {
	Username string `yaml:"username"`
	UID      int    `yaml:"uid"`
	Group    string `yaml:"group"`
	GID      int    `yaml:"gid"`
	Home     string `yaml:"home"`
}
type Platform struct {
	System string `yaml:"system"`
}
type RebuildConfig struct {
	Mode            string `yaml:"mode"`
	Target          string `yaml:"target,omitempty"`
	Impure          bool   `yaml:"impure"`
	ImportConfirmed bool   `yaml:"importConfirmed"`
}
type ServiceStates struct {
	Nginx   ServiceConfig `yaml:"nginx"`
	MariaDB ServiceConfig `yaml:"mariadb"`
	Redis   ServiceConfig `yaml:"redis"`
}
type ServiceConfig struct {
	Installed    bool   `yaml:"installed"`
	DesiredState string `yaml:"desiredState"`
}
type PHPConfig struct {
	Installed     []string `yaml:"installed"`
	Extensions    []string `yaml:"extensions"`
	GlobalDefault string   `yaml:"globalDefault,omitempty"`
}

type SiteConfig struct {
	SchemaVersion int            `yaml:"schemaVersion"`
	ID            string         `yaml:"id"`
	Enabled       bool           `yaml:"enabled"`
	Domain        string         `yaml:"domain"`
	ProjectPath   string         `yaml:"projectPath"`
	DocumentRoot  string         `yaml:"documentRoot"`
	PHP           string         `yaml:"php"`
	MariaDB       *MariaDBConfig `yaml:"mariadb,omitempty"`
	Nginx         NginxConfig    `yaml:"nginx"`
}
type MariaDBConfig struct {
	Database string `yaml:"database"`
}
type NginxConfig struct {
	Handler HandlerConfig `yaml:"handler"`
}
type HandlerConfig struct {
	Type    string `yaml:"type"`
	Name    string `yaml:"name,omitempty"`
	Path    string `yaml:"path,omitempty"`
	Content string `yaml:"-"`
}

// Snapshot is the complete, validated desired state.
type Snapshot struct {
	Config ConfigSnapshot
	Sites  []SiteConfig
}

func (cfg *ConfigSnapshot) Canonicalize() {
	cfg.Owner.Username = strings.TrimSpace(cfg.Owner.Username)
	cfg.Owner.Group = strings.TrimSpace(cfg.Owner.Group)
	cfg.Owner.Home = filepath.Clean(cfg.Owner.Home)
	cfg.Platform.System = strings.TrimSpace(cfg.Platform.System)
	if cfg.Platform.System == "" {
		cfg.Platform.System = "x86_64-linux"
	}
	cfg.Rebuild.Mode = strings.ToLower(strings.TrimSpace(cfg.Rebuild.Mode))
	if cfg.Rebuild.Mode == "" {
		cfg.Rebuild.Mode = rebuildModeTraditional
	}
	cfg.Rebuild.Target = strings.TrimSpace(cfg.Rebuild.Target)
	for _, s := range []*ServiceConfig{&cfg.Services.Nginx, &cfg.Services.MariaDB, &cfg.Services.Redis} {
		s.DesiredState = strings.ToLower(strings.TrimSpace(s.DesiredState))
	}
	cfg.PHP.GlobalDefault = strings.TrimSpace(cfg.PHP.GlobalDefault)
	for i := range cfg.PHP.Installed {
		cfg.PHP.Installed[i] = strings.TrimSpace(cfg.PHP.Installed[i])
	}
	for i := range cfg.PHP.Extensions {
		cfg.PHP.Extensions[i] = strings.ToLower(strings.TrimSpace(cfg.PHP.Extensions[i]))
	}
	sort.Strings(cfg.PHP.Installed)
	sort.Strings(cfg.PHP.Extensions)
}
func (site *SiteConfig) Canonicalize() {
	site.ID = strings.TrimSpace(strings.ToLower(site.ID))
	site.Domain = strings.TrimSpace(strings.ToLower(site.Domain))
	site.Domain = strings.TrimSuffix(site.Domain, ".")
	site.ProjectPath = filepath.Clean(site.ProjectPath)
	site.DocumentRoot = filepath.Clean(site.DocumentRoot)
	site.PHP = strings.TrimSpace(site.PHP)
	site.Nginx.Handler.Type = strings.ToLower(strings.TrimSpace(site.Nginx.Handler.Type))
	site.Nginx.Handler.Name = strings.ToLower(strings.TrimSpace(site.Nginx.Handler.Name))
	site.Nginx.Handler.Path = filepath.Clean(site.Nginx.Handler.Path)
	if site.MariaDB != nil {
		site.MariaDB.Database = strings.TrimSpace(site.MariaDB.Database)
	}
}

func ValidateConfig(cfg ConfigSnapshot) error {
	if cfg.SchemaVersion != supportedSchemaVersion {
		return newStateError("unsupported_schema", "unsupported schemaVersion", nil)
	}
	if cfg.Owner.Username == "" || cfg.Owner.Group == "" || cfg.Owner.Home == "" || !filepath.IsAbs(cfg.Owner.Home) {
		return newStateError("invalid_owner", "owner must contain username, group, and absolute home", nil)
	}
	if cfg.Platform.System != "x86_64-linux" {
		return newStateError("invalid_platform", "platform.system must be x86_64-linux", nil)
	}
	if !IsValidRebuildMode(cfg.Rebuild.Mode) {
		return newStateError("invalid_rebuild_mode", "rebuild mode must be traditional or flake", nil)
	}
	if cfg.Rebuild.Mode == "flake" && cfg.Rebuild.Target == "" {
		return newStateError("invalid_rebuild_target", "flake rebuild mode requires target", nil)
	}
	if cfg.Rebuild.Mode == "traditional" && cfg.Rebuild.Target != "" {
		return newStateError("invalid_rebuild_target", "traditional rebuild mode cannot set target", nil)
	}
	for name, svc := range map[string]ServiceConfig{"nginx": cfg.Services.Nginx, "mariadb": cfg.Services.MariaDB, "redis": cfg.Services.Redis} {
		if !IsValidServiceState(svc.DesiredState) {
			return newStateError("invalid_service_state", "invalid "+name+" desiredState", nil)
		}
		if !svc.Installed && svc.DesiredState != "stopped" {
			return newStateError("invalid_service_state", "uninstalled "+name+" cannot be running", nil)
		}
	}
	if err := validateUnique(cfg.PHP.Installed, "php version", func(s string) (string, error) { return NormalizePHPVersion(s) }); err != nil {
		return err
	}
	if err := validateUnique(cfg.PHP.Extensions, "extension", func(s string) (string, error) {
		if !IsValidExtName(s) {
			return "", fmt.Errorf("invalid extension")
		}
		return s, nil
	}); err != nil {
		return err
	}
	if cfg.PHP.GlobalDefault != "" && !contains(cfg.PHP.Installed, cfg.PHP.GlobalDefault) {
		return newStateError("invalid_global_default", "globalDefault must be installed", nil)
	}
	return nil
}
func ValidateSite(site SiteConfig) error {
	if site.SchemaVersion != supportedSchemaVersion {
		return newStateError("unsupported_schema", "unsupported schemaVersion", nil)
	}
	if site.ID == "" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`).MatchString(site.ID) {
		return newStateError("invalid_site_id", "invalid site id", nil)
	}
	if _, err := NormalizeDomain(site.Domain); err != nil {
		return newStateError("invalid_domain", err.Error(), nil)
	}
	if _, err := NormalizePHPVersion(site.PHP); err != nil {
		return err
	}
	if !filepath.IsAbs(site.ProjectPath) || !filepath.IsAbs(site.DocumentRoot) {
		return newStateError("invalid_path", "site paths must be absolute", nil)
	}
	if site.Nginx.Handler.Type == "template" && !IsTemplateHandler(site.Nginx.Handler.Name) {
		return newStateError("invalid_handler", "unknown template handler", nil)
	}
	if site.Nginx.Handler.Type == "custom" && (!filepath.IsAbs(site.Nginx.Handler.Path) || site.Nginx.Handler.Path == ".") {
		return newStateError("invalid_handler", "custom handler requires absolute path", nil)
	}
	if site.Nginx.Handler.Type != "template" && site.Nginx.Handler.Type != "custom" && site.Nginx.Handler.Type != "generic" {
		return newStateError("invalid_handler", "handler type must be template, custom, or generic", nil)
	}
	if site.MariaDB != nil && !isValidMariaDBName(site.MariaDB.Database) {
		return newStateError("invalid_mariadb", "invalid database name", nil)
	}
	return nil
}
func (s Snapshot) Validate() error {
	s.Config.Canonicalize()
	if err := ValidateConfig(s.Config); err != nil {
		return err
	}
	ids, domains := map[string]bool{}, map[string]bool{}
	for _, site := range s.Sites {
		site.Canonicalize()
		if err := ValidateSite(site); err != nil {
			return err
		}
		if ids[site.ID] || domains[site.Domain] {
			return newStateError("duplicate_site", "duplicate site id or domain", nil)
		}
		ids[site.ID] = true
		domains[site.Domain] = true
		if site.Enabled && !s.Config.Services.Nginx.Installed {
			return newStateError("nginx_not_installed", "enabled site requires nginx", nil)
		}
		if !contains(s.Config.PHP.Installed, site.PHP) {
			return newStateError("php_version_not_installed", "site PHP version is not installed", nil)
		}
		if site.MariaDB != nil && !s.Config.Services.MariaDB.Installed {
			return newStateError("mariadb_not_installed", "site database requires mariadb", nil)
		}
	}
	return nil
}
func NormalizeAndValidateConfig(raw []byte) (ConfigSnapshot, error) {
	var cfg ConfigSnapshot
	if err := decodeStrict(raw, &cfg); err != nil {
		return cfg, err
	}
	cfg.Canonicalize()
	return cfg, ValidateConfig(cfg)
}
func NormalizeAndValidateSite(raw []byte) (SiteConfig, error) {
	var site SiteConfig
	if err := decodeStrict(raw, &site); err != nil {
		return site, err
	}
	site.Canonicalize()
	return site, ValidateSite(site)
}
func ValidateSnapshots(candidates ...ConfigSnapshot) error {
	for _, c := range candidates {
		if err := ValidateConfig(c); err != nil {
			return err
		}
	}
	return nil
}
func NormalizePHPVersion(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(raw) {
		return "", newStateError("invalid_php", "php version must be major.minor", nil)
	}
	return raw, nil
}
func isValidMariaDBName(n string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]{0,63}$`).MatchString(n)
}
func validateUnique(values []string, kind string, normal func(string) (string, error)) error {
	seen := map[string]bool{}
	for _, v := range values {
		n, e := normal(v)
		if e != nil {
			return newStateError("invalid_"+strings.ReplaceAll(kind, " ", "_"), e.Error(), e)
		}
		if seen[n] {
			return newStateError("duplicate_entry", "duplicate "+kind, nil)
		}
		seen[n] = true
	}
	return nil
}
func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
