package command

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/output"
	sitepkg "github.com/nixcp/nixcp/internal/site"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
	"github.com/nixcp/nixcp/internal/ui"
	"github.com/spf13/cobra"
)

func newLinkCommand(runtime Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "link <domain>", Short: "Create an HTTP site", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error { return runLink(c, runtime, a[0]) }}
	cmd.Flags().String("php", "", "PHP version")
	cmd.Flags().String("mariadb", "", "MariaDB database")
	cmd.Flags().String("template", "", "Template: laravel or wordpress")
	cmd.Flags().String("config", "", "Custom nginx location snippet")
	cmd.Flags().String("path", "", "Project path (default current directory)")
	cmd.Flags().String("root", "", "Document root relative to project or absolute")
	return cmd
}
func siteStore(runtime Runtime) (*state.Store, error) {
	u, e := user.Current()
	if e != nil {
		return nil, e
	}
	home := u.HomeDir
	if runtime.StateHome != "" {
		home = runtime.StateHome
	}
	return state.NewStore(home), nil
}
func runLink(cmd *cobra.Command, runtime Runtime, rawDomain string) error {
	store, err := siteStore(runtime)
	if err != nil {
		return err
	}
	snap, err := store.Load()
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	domain, err := state.NormalizeDomain(rawDomain)
	if err != nil {
		return apperrors.New("invalid_domain", err.Error(), "Provide a valid HTTP hostname", apperrors.ExitCodePrecond)
	}
	for _, s := range snap.Sites {
		if s.Domain == domain {
			return apperrors.New("site_exists", "a site already uses "+domain, "Use ncp sites show "+domain, apperrors.ExitCodeLock)
		}
	}
	phpRaw, _ := cmd.Flags().GetString("php")
	if phpRaw == "" {
		phpRaw = snap.Config.PHP.GlobalDefault
	}
	phpVersion, err := state.NormalizePHPVersion(phpRaw)
	if err != nil {
		return apperrors.New("invalid_php", err.Error(), "Specify --php=8.3 or --php=8.4", apperrors.ExitCodePrecond)
	}
	if !containsString(snap.Config.PHP.Installed, phpVersion) {
		return apperrors.New("php_version_not_installed", "PHP "+phpVersion+" is not installed", "Run: ncp php install "+phpVersion, apperrors.ExitCodePrecond)
	}
	if !snap.Config.Services.Nginx.Installed || snap.Config.Services.Nginx.DesiredState != "running" {
		return apperrors.New("nginx_not_running", "nginx must be installed and running", "Run: ncp service nginx install", apperrors.ExitCodePrecond)
	}
	tmpl, _ := cmd.Flags().GetString("template")
	config, _ := cmd.Flags().GetString("config")
	if tmpl != "" && config != "" {
		return apperrors.New("mutually_exclusive_flags", "--template and --config cannot be used together", "Choose one handler", apperrors.ExitCodeUsage)
	}
	// On a real TTY, a missing handler choice is a genuine choice: offer
	// generic + templates. Non-interactive callers keep the generic default
	// so scripted output stays byte-stable.
	if tmpl == "" && config == "" && commandUIMode(cmd).Interactive() {
		choice, e := ui.Select("HTTP handler", "How should nginx route this site?", []ui.SelectOption{
			{Value: "generic", Label: "Generic", Desc: "FastCGI to PHP-FPM only"},
			{Value: "laravel", Label: "Laravel", Desc: "Template for Laravel public/ structure"},
			{Value: "wordpress", Label: "WordPress", Desc: "Template for WordPress"},
		})
		if e != nil {
			return apperrors.New("handler_selection_failed", e.Error(), "", apperrors.ExitCodeUsage)
		}
		tmpl = choice
	}
	handler := state.HandlerConfig{Type: "generic"}
	if tmpl != "" {
		tmpl = strings.ToLower(strings.TrimSpace(tmpl))
		if !state.IsTemplateHandler(tmpl) {
			return apperrors.New("invalid_template", "template must be laravel or wordpress", "Use --template=laravel or --template=wordpress", apperrors.ExitCodePrecond)
		}
		handler = state.HandlerConfig{Type: "template", Name: tmpl}
	}
	if config != "" {
		p, e := canonicalFile(config)
		if e != nil {
			return apperrors.New("invalid_handler", e.Error(), "Custom config must be a readable regular file", apperrors.ExitCodePrecond)
		}
		b, e := os.ReadFile(p)
		if e != nil || int64(len(b)) > 64<<10 || strings.ContainsRune(string(b), 0) {
			return apperrors.New("invalid_handler", "custom config cannot be read safely", "Use a regular text file within the 64 KiB limit", apperrors.ExitCodePrecond)
		}
		if e := validateSnippet(string(b)); e != nil {
			return apperrors.New("invalid_handler", e.Error(), "Remove directives outside NixCP's location boundary", apperrors.ExitCodePrecond)
		}
		handler = state.HandlerConfig{Type: "custom", Path: p, Content: string(b)}
	}
	projectRaw, _ := cmd.Flags().GetString("path")
	if projectRaw == "" {
		projectRaw, _ = os.Getwd()
	}
	project, e := canonicalDir(projectRaw)
	if e != nil {
		return apperrors.New("invalid_path", e.Error(), "Provide an existing readable project directory", apperrors.ExitCodePrecond)
	}
	rootRaw, _ := cmd.Flags().GetString("root")
	if rootRaw == "" && handler.Type == "template" && handler.Name == "laravel" {
		rootRaw = "public"
	}
	root := project
	if rootRaw != "" {
		if !filepath.IsAbs(rootRaw) {
			rootRaw = filepath.Join(project, rootRaw)
		}
		root, e = canonicalDir(rootRaw)
		if e != nil {
			return apperrors.New("invalid_path", e.Error(), "Document root must be an existing directory", apperrors.ExitCodePrecond)
		}
	}
	db, _ := cmd.Flags().GetString("mariadb")
	var maria *state.MariaDBConfig
	if db != "" {
		if !snap.Config.Services.MariaDB.Installed || snap.Config.Services.MariaDB.DesiredState != "running" {
			return apperrors.New("mariadb_not_running", "MariaDB must be installed and running", "Run: ncp service mariadb install", apperrors.ExitCodePrecond)
		}
		maria = &state.MariaDBConfig{Database: db}
		if !containsString(snap.Config.MariaDBRegistry.Databases, db) {
			snap.Config.MariaDBRegistry.Databases = append(snap.Config.MariaDBRegistry.Databases, db)
		}
	}
	ids := map[string]struct{}{}
	for _, s := range snap.Sites {
		ids[s.ID] = struct{}{}
	}
	site := state.SiteConfig{SchemaVersion: 1, ID: state.GenerateStableSiteID(domain, ids), Enabled: true, Domain: domain, ProjectPath: project, DocumentRoot: root, PHP: phpVersion, MariaDB: maria, Nginx: state.NginxConfig{Handler: handler}}
	snap.Sites = append(snap.Sites, site)
	snap.Canonicalize()
	if err := snap.Validate(); err != nil {
		return apperrors.New("invalid_site", err.Error(), "Correct the site input", apperrors.ExitCodePrecond)
	}
	return applySite(cmd, runtime, store, snap, nil, "link", site)
}

// canonicalDir resolves the physical path and applies the v1 site-path
// safety rules: existing directory, readable/traversable, and no
// world-writable component on the way to / (the shared rules live in the
// internal/site package so status/doctor can reuse them).
func canonicalDir(p string) (string, error) {
	if _, err := sitepkg.CanonicalizePath(p); err != nil {
		return "", fmt.Errorf("path must be an existing readable and traversable directory")
	}
	a, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	a, e = filepath.EvalSymlinks(a)
	if e != nil {
		return "", e
	}
	i, e := os.Lstat(a)
	if e != nil || !i.IsDir() || i.Mode().Perm()&0555 != 0555 || a == "/" {
		return "", fmt.Errorf("path must be an existing readable and traversable directory")
	}
	if err := sitepkg.RefuseWorldWritable(a); err != nil {
		return "", fmt.Errorf("path must not be beneath a world-writable directory")
	}
	return filepath.Clean(a), nil
}
func canonicalFile(p string) (string, error) {
	a, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	i, e := os.Lstat(a)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0444 == 0 {
		return "", fmt.Errorf("path must be a readable regular non-symlink file")
	}
	return filepath.Clean(a), nil
}
func validateSnippet(s string) error {
	forbidden := []string{"server", "http", "events", "listen", "server_name", "root", "include", "upstream", "ssl", "certificate", "acme", "fastcgi_pass"}
	for n, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.Split(line, "#")[0])
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		d := strings.ToLower(f[0])
		for _, x := range forbidden {
			if d == x || strings.HasPrefix(d, x+"_") {
				return fmt.Errorf("line %d: directive %s is forbidden", n+1, d)
			}
		}
		if strings.Contains(line, "{") || strings.Contains(line, "}") {
			return fmt.Errorf("line %d: blocks are forbidden", n+1)
		}
	}
	return nil
}
func applySite(cmd *cobra.Command, runtime Runtime, store *state.Store, snap state.Snapshot, deletes []string, action string, site state.SiteConfig) error {
	module, e := runtime.Renderer.Render(snap)
	if e != nil {
		return apperrors.New("render_failed", e.Error(), "", apperrors.ExitCodeBuild)
	}
	cfg, e := state.MarshalConfig(snap.Config)
	if e != nil {
		return e
	}
	files := map[string][]byte{"config.yaml": cfg, "generated/nixcp-module.nix": module}
	for _, s := range snap.Sites {
		b, x := state.MarshalSite(s)
		if x != nil {
			return x
		}
		files[filepath.Join("sites", s.ID+".yaml")] = b
	}
	manager := runtime.Transactions
	if manager == nil {
		manager = defaultServiceTransaction(store.Root, runtime, snap.Config.Rebuild, desiredHealth{systemd: runtime.Services, name: "nginx", running: true})
	}
	result, e := manager.Apply(cmd.Context(), transaction.Request{Files: files, Deletes: deletes, CandidateModule: "generated/nixcp-module.nix", Affected: []string{"nginx"}})
	if e != nil {
		return transactionError(e)
	}
	data := map[string]any{"id": site.ID, "domain": site.Domain, "php": site.PHP, "documentRoot": site.DocumentRoot, "handler": site.Nginx.Handler.Type, "phase": result.Phase}
	if site.MariaDB != nil {
		data["mariadb"] = site.MariaDB.Database
	}
	if commandJSON(cmd) {
		return emitJSON(cmd, output.Success(action, result.Changed, data, nil))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", ui.OKLine(action), site.Domain, result.Phase)
	return nil
}
func newUnlinkCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{Use: "unlink <domain-or-site-id>", Short: "Remove a site", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error { return runUnlink(c, runtime, a[0]) }}
}
func runUnlink(cmd *cobra.Command, runtime Runtime, key string) error {
	store, e := siteStore(runtime)
	if e != nil {
		return e
	}
	snap, e := store.Load()
	if e != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	normalized, _ := state.NormalizeDomain(key)
	var found *state.SiteConfig
	for i := range snap.Sites {
		if snap.Sites[i].ID == key || snap.Sites[i].Domain == normalized {
			if found != nil {
				return apperrors.New("ambiguous_site", "site target is ambiguous", "Use the site ID", apperrors.ExitCodePrecond)
			}
			found = &snap.Sites[i]
		}
	}
	if found == nil {
		return apperrors.New("site_not_found", "site was not found", "Run: ncp sites list", apperrors.ExitCodePrecond)
	}
	// Destructive op: confirm only on a TTY without --yes/--json/--no-input;
	// scripts keep today's non-interactive semantics.
	if commandUIMode(cmd).Confirmable() {
		ok, e := ui.Confirm("Unlink site " + found.Domain + " (" + found.ID + ")?")
		if e != nil || !ok {
			return apperrors.New("aborted_by_user", "aborted by user", "", apperrors.ExitCodeUsage)
		}
	}
	remaining := []state.SiteConfig{}
	for _, s := range snap.Sites {
		if s.ID != found.ID {
			remaining = append(remaining, s)
		}
	}
	snap.Sites = remaining
	return applySite(cmd, runtime, store, snap, []string{filepath.Join("sites", found.ID+".yaml")}, "unlink", *found)
}
func newSitesCommand(runtime Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "sites", Short: "List and show site entries"}
	cmd.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return runSitesList(c, runtime) }}, &cobra.Command{Use: "show <domain-or-site-id>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error { return runSitesShow(c, runtime, a[0]) }})
	return cmd
}
func runSitesList(cmd *cobra.Command, runtime Runtime) error {
	store, e := siteStore(runtime)
	if e != nil {
		return e
	}
	snap, e := store.Load()
	if e != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	if commandJSON(cmd) {
		return emitJSON(cmd, output.Success("sites.list", false, map[string]any{"sites": snap.Sites}, nil))
	}
	for _, s := range snap.Sites {
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s php=%s root=%s\n", s.ID, s.Domain, s.PHP, s.DocumentRoot)
	}
	return nil
}
func runSitesShow(cmd *cobra.Command, runtime Runtime, key string) error {
	store, e := siteStore(runtime)
	if e != nil {
		return e
	}
	snap, e := store.Load()
	if e != nil {
		return e
	}
	d, _ := state.NormalizeDomain(key)
	for _, s := range snap.Sites {
		if s.ID == key || s.Domain == d {
			// Read-only drift probe: socket + HTTP reachability as desired.
			checker := sitepkg.RealChecker{}
			health := checker.CheckSite(cmd.Context(), s.Domain, s.ID, s.Enabled)
			data := map[string]any{"site": s, "pool": "nixcp-" + s.ID, "socket": sitepkg.SocketPath(s.ID), "health": health}
			if commandJSON(cmd) {
				return emitJSON(cmd, output.Success("sites.show", false, data, nil))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", s.ID, s.Domain)
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", health.Describe())
			return nil
		}
	}
	return apperrors.New("site_not_found", "site was not found", "Run: ncp sites list", apperrors.ExitCodePrecond)
}
