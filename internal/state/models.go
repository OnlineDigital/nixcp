// Package state owns the strict YAML source of truth stored under ~/.nixcp.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nixcp/nixcp/internal/nginxsnippet"
)

// supportedSchemaVersion is the only state schema accepted by this binary.
const supportedSchemaVersion = 2

var phpVersionPattern = regexp.MustCompile(`^(?:8\.[0-9]+)$`)
var siteIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
var databasePasswordPattern = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

// ConfigSnapshot is config.yaml. Empty strings encode YAML null for nullable fields.
type ConfigSnapshot struct {
	SchemaVersion   int                 `yaml:"schemaVersion"`
	Owner           Owner               `yaml:"owner"`
	Platform        Platform            `yaml:"platform"`
	Rebuild         RebuildConfig       `yaml:"rebuild"`
	Services        ServiceStates       `yaml:"services"`
	PHP             PHPConfig           `yaml:"php"`
	MariaDBRegistry MariaDBRegistry     `yaml:"mariadbRegistry,omitempty"`
	Aliases         map[string][]string `yaml:"aliases,omitempty"`
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
	Valkey  ServiceConfig `yaml:"valkey"`
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
type MariaDBRegistry struct {
	Databases []string `yaml:"databases,omitempty"`
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
	// User is the dedicated MariaDB login for this site's database. NixCP sets
	// it to the database name (one database → one user, per plan 06), so two
	// sites never share a MariaDB account.
	User string `yaml:"user,omitempty"`
	// Password is a generated, owner-only random secret kept in the private
	// site YAML (0600). It is never written into the world-readable Nix store:
	// the generated module provisions accounts from a private 0600
	// ~/.nixcp/secrets/mariadb/accounts.sql file that the oneshot unit reads via
	// stdin.
	Password string `yaml:"password,omitempty"`
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

// MarshalYAML preserves explicit nulls for nullable schema fields instead of
// silently omitting them. This makes freshly generated config.yaml match the
// published schema and keeps canonical bytes stable.
func (cfg ConfigSnapshot) MarshalYAML() (any, error) {
	type rebuildYAML struct {
		Mode            string  `yaml:"mode"`
		Target          *string `yaml:"target"`
		Impure          bool    `yaml:"impure"`
		ImportConfirmed bool    `yaml:"importConfirmed"`
	}
	type phpYAML struct {
		Installed     []string `yaml:"installed"`
		Extensions    []string `yaml:"extensions"`
		GlobalDefault *string  `yaml:"globalDefault"`
	}
	type configYAML struct {
		SchemaVersion   int                 `yaml:"schemaVersion"`
		Owner           Owner               `yaml:"owner"`
		Platform        Platform            `yaml:"platform"`
		Rebuild         rebuildYAML         `yaml:"rebuild"`
		Services        ServiceStates       `yaml:"services"`
		PHP             phpYAML             `yaml:"php"`
		MariaDBRegistry MariaDBRegistry     `yaml:"mariadbRegistry,omitempty"`
		Aliases         map[string][]string `yaml:"aliases,omitempty"`
	}
	var target, globalDefault *string
	if cfg.Rebuild.Target != "" {
		value := cfg.Rebuild.Target
		target = &value
	}
	if cfg.PHP.GlobalDefault != "" {
		value := cfg.PHP.GlobalDefault
		globalDefault = &value
	}
	return configYAML{cfg.SchemaVersion, cfg.Owner, cfg.Platform, rebuildYAML{cfg.Rebuild.Mode, target, cfg.Rebuild.Impure, cfg.Rebuild.ImportConfirmed}, cfg.Services, phpYAML{cfg.PHP.Installed, cfg.PHP.Extensions, globalDefault}, cfg.MariaDBRegistry, cfg.Aliases}, nil
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
	if len(cfg.Aliases) == 0 {
		cfg.Aliases = nil
	}
	for _, s := range []*ServiceConfig{&cfg.Services.Nginx, &cfg.Services.MariaDB, &cfg.Services.Valkey} {
		s.DesiredState = strings.ToLower(strings.TrimSpace(s.DesiredState))
	}
	cfg.PHP.GlobalDefault = strings.TrimSpace(cfg.PHP.GlobalDefault)
	for i := range cfg.PHP.Installed {
		cfg.PHP.Installed[i] = strings.TrimSpace(cfg.PHP.Installed[i])
	}
	for i := range cfg.PHP.Extensions {
		cfg.PHP.Extensions[i] = strings.ToLower(strings.TrimSpace(cfg.PHP.Extensions[i]))
	}
	for i := range cfg.MariaDBRegistry.Databases {
		cfg.MariaDBRegistry.Databases[i] = strings.TrimSpace(cfg.MariaDBRegistry.Databases[i])
	}
	sort.Strings(cfg.PHP.Installed)
	sort.Strings(cfg.PHP.Extensions)
	sort.Strings(cfg.MariaDBRegistry.Databases)
}
func (site *SiteConfig) Canonicalize() {
	site.ID = strings.TrimSpace(strings.ToLower(site.ID))
	site.Domain = strings.TrimSpace(strings.ToLower(site.Domain))
	site.Domain = strings.TrimSuffix(site.Domain, ".")
	if normalized, err := NormalizeDomain(site.Domain); err == nil {
		site.Domain = normalized
	}
	site.ProjectPath = filepath.Clean(site.ProjectPath)
	site.DocumentRoot = filepath.Clean(site.DocumentRoot)
	site.PHP = strings.TrimSpace(site.PHP)
	site.Nginx.Handler.Type = strings.ToLower(strings.TrimSpace(site.Nginx.Handler.Type))
	site.Nginx.Handler.Name = strings.ToLower(strings.TrimSpace(site.Nginx.Handler.Name))
	site.Nginx.Handler.Path = strings.TrimSpace(site.Nginx.Handler.Path)
	if site.Nginx.Handler.Path != "" {
		site.Nginx.Handler.Path = filepath.Clean(site.Nginx.Handler.Path)
	}
	if site.MariaDB != nil {
		site.MariaDB.Database = strings.TrimSpace(site.MariaDB.Database)
		if site.MariaDB.User == "" {
			// One database ⇒ one user (the database name), so a site's MariaDB
			// account is never shared across sites.
			site.MariaDB.User = site.MariaDB.Database
		} else {
			site.MariaDB.User = strings.TrimSpace(site.MariaDB.User)
		}
		if site.MariaDB.Password == "" {
			site.MariaDB.Password = GenerateDatabasePassword()
		}
	}
}

func ValidateConfig(cfg ConfigSnapshot) error {
	if cfg.SchemaVersion != supportedSchemaVersion {
		return newStateError("unsupported_schema", "schemaVersion must be 2", nil)
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
	if strings.ContainsAny(cfg.Rebuild.Target, "\x00\r\n") {
		return newStateError("invalid_rebuild_target", "rebuild target contains unsafe characters", nil)
	}
	for name, svc := range map[string]ServiceConfig{"nginx": cfg.Services.Nginx, "mariadb": cfg.Services.MariaDB, "valkey": cfg.Services.Valkey} {
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
	if err := validateUnique(cfg.MariaDBRegistry.Databases, "mariadb database", func(s string) (string, error) {
		if !isValidMariaDBName(s) {
			return "", fmt.Errorf("invalid database")
		}
		return s, nil
	}); err != nil {
		return err
	}
	return nil
}
func ValidateSite(site SiteConfig) error {
	if site.SchemaVersion != supportedSchemaVersion {
		return newStateError("unsupported_schema", "schemaVersion must be 2", nil)
	}
	if site.ID == "" || !siteIDPattern.MatchString(site.ID) {
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
	if site.ProjectPath == "/" || site.DocumentRoot == "/" {
		return newStateError("invalid_path", "site paths cannot be the filesystem root", nil)
	}
	if err := validateProjectDirectory(site.ProjectPath, "projectPath"); err != nil {
		return err
	}
	if err := validateProjectDirectory(site.DocumentRoot, "documentRoot"); err != nil {
		return err
	}
	if rel, err := filepath.Rel(site.ProjectPath, site.DocumentRoot); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return newStateError("invalid_path", "documentRoot must be within projectPath", err)
	}
	if site.Nginx.Handler.Type == "template" && !IsTemplateHandler(site.Nginx.Handler.Name) {
		return newStateError("invalid_handler", "unknown template handler", nil)
	}
	if site.Nginx.Handler.Type == "generic" && (site.Nginx.Handler.Name != "" || site.Nginx.Handler.Path != "") {
		return newStateError("invalid_handler", "generic handler cannot name a template or path", nil)
	}
	if site.Nginx.Handler.Type == "custom" && (!filepath.IsAbs(site.Nginx.Handler.Path) || site.Nginx.Handler.Path == ".") {
		return newStateError("invalid_handler", "custom handler requires absolute path", nil)
	}
	if site.Nginx.Handler.Type == "custom" {
		info, err := os.Lstat(site.Nginx.Handler.Path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0444 == 0 || info.Size() > maxCustomSnippetBytes {
			return newStateError("invalid_handler", "custom handler must be a readable regular file within the size limit", err)
		}
		content, err := readRegularNoFollow(site.Nginx.Handler.Path, maxCustomSnippetBytes)
		if err != nil {
			return newStateError("invalid_handler", "cannot read custom handler", err)
		}
		if err := nginxsnippet.Validate(string(content)); err != nil {
			return newStateError("invalid_handler", "custom handler is not a permitted location snippet", err)
		}
		if site.Nginx.Handler.Content != "" {
			if err := nginxsnippet.Validate(site.Nginx.Handler.Content); err != nil {
				return newStateError("invalid_handler", "custom handler content is not a permitted location snippet", err)
			}
		}
	}
	if site.Nginx.Handler.Type != "template" && site.Nginx.Handler.Type != "custom" && site.Nginx.Handler.Type != "generic" {
		return newStateError("invalid_handler", "handler type must be template or custom", nil)
	}
	if site.MariaDB != nil {
		if !isValidMariaDBName(site.MariaDB.Database) {
			return newStateError("invalid_mariadb", "invalid database name", nil)
		}
		if site.MariaDB.User == "" || !isValidMariaDBName(site.MariaDB.User) {
			return newStateError("invalid_mariadb", "invalid MariaDB user name", nil)
		}
		if !databasePasswordPattern.MatchString(site.MariaDB.Password) {
			return newStateError("invalid_mariadb", "MariaDB password must be 16-64 alphanumeric characters", nil)
		}
	}
	return nil
}
func (s Snapshot) Validate() error {
	s.Config.Canonicalize()
	if err := ValidateConfig(s.Config); err != nil {
		return err
	}
	ids, domains := map[string]bool{}, map[string]bool{}
	databases := map[string]bool{}
	for _, site := range s.Sites {
		site.Canonicalize()
		if err := ValidateSite(site); err != nil {
			return err
		}
		if ids[site.ID] || domains[strings.ToLower(site.Domain)] {
			return newStateError("duplicate_site", "duplicate site id or domain", nil)
		}
		ids[site.ID] = true
		domains[strings.ToLower(site.Domain)] = true
		if site.Enabled && !s.Config.Services.Nginx.Installed {
			return newStateError("nginx_not_installed", "enabled site requires nginx", nil)
		}
		if !contains(s.Config.PHP.Installed, site.PHP) {
			return newStateError("php_version_not_installed", "site PHP version is not installed", nil)
		}
		if site.MariaDB != nil && !s.Config.Services.MariaDB.Installed {
			return newStateError("mariadb_not_installed", "site database requires mariadb", nil)
		}
		if site.MariaDB != nil {
			if databases[site.MariaDB.Database] {
				return newStateError("database_in_use", "MariaDB database is already used by another site", nil)
			}
			databases[site.MariaDB.Database] = true
		}
		if site.MariaDB != nil && !contains(s.Config.MariaDBRegistry.Databases, site.MariaDB.Database) {
			return newStateError("invalid_mariadb", "site database must be recorded in the MariaDB registry", nil)
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
	if cfg.SchemaVersion != supportedSchemaVersion {
		return cfg, newStateError("unsupported_schema", "schemaVersion must be 2", nil)
	}
	// Declarative structural layer (go-playground/validator) runs after
	// canonicalization and before semantic rules: it guards the structural
	// contract of the current schema on every parsed config.yaml.
	if err := applyStructural("config", structuralConfig(cfg)); err != nil {
		return cfg, err
	}
	return cfg, ValidateConfig(cfg)
}
func NormalizeAndValidateSite(raw []byte) (SiteConfig, error) {
	var site SiteConfig
	if err := decodeStrict(raw, &site); err != nil {
		return site, err
	}
	site.Canonicalize()
	if site.SchemaVersion != supportedSchemaVersion {
		return site, newStateError("unsupported_schema", "schemaVersion must be 2", nil)
	}
	// Same declarative structural layer for every parsed sites/*.yaml.
	if err := applyStructural("site", structuralSite(site)); err != nil {
		return site, err
	}
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
	if !phpVersionPattern.MatchString(raw) {
		return "", newStateError("invalid_php", "php version must be major.minor", nil)
	}
	if !IsSupportedPHPVersion(raw) {
		return "", newStateError("unsupported_php_version", "PHP version is not in the supported nixpkgs catalog", nil)
	}
	return raw, nil
}

const maxCustomSnippetBytes int64 = 64 << 10

func validateProjectDirectory(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return newStateError("invalid_path", name+" must be an existing non-symlink directory", err)
	}
	if info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0444 == 0 {
		return newStateError("invalid_path", name+" must be readable and traversable", nil)
	}
	// A project beneath a writable ancestor can be replaced after validation.
	// v1 deliberately refuses that topology instead of mutating project modes.
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		parent, statErr := os.Lstat(current)
		if statErr != nil || !parent.IsDir() {
			return newStateError("invalid_path", name+" has an unsafe ancestor", statErr)
		}
		if parent.Mode().Perm()&0002 != 0 && parent.Mode()&os.ModeSticky == 0 {
			return newStateError("invalid_path", name+" cannot be under a world-writable directory", nil)
		}
		if current == "/" {
			break
		}
	}
	return nil
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

// GenerateDatabasePassword returns a fresh random alphanumeric secret for a
// site's dedicated MariaDB account. Exported so the CLI can generate one at
// link time; alphanumeric-only keeps it safe to embed in SQL single-quoted
// literals, Nix strings and YAML plain scalars without any escaping.
func GenerateDatabasePassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read must not fail on a supported platform; a predictable
		// password would defeat the whole point.
		panic(fmt.Sprintf("nixcp: cannot generate MariaDB password: %v", err))
	}
	return hex.EncodeToString(b)
}
