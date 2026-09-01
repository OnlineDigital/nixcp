package command

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/nixcp/nixcp/internal/database"
	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/nginxsnippet"
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
		maria = &state.MariaDBConfig{Database: db, User: db, Password: state.GenerateDatabasePassword()}
		if !containsString(snap.Config.MariaDBRegistry.Databases, db) {
			snap.Config.MariaDBRegistry.Databases = append(snap.Config.MariaDBRegistry.Databases, db)
		}
	}
	ids := map[string]struct{}{}
	for _, s := range snap.Sites {
		ids[s.ID] = struct{}{}
	}
	site := state.SiteConfig{SchemaVersion: 2, ID: state.GenerateStableSiteID(domain, ids), Enabled: true, Domain: domain, ProjectPath: project, DocumentRoot: root, PHP: phpVersion, MariaDB: maria, Nginx: state.NginxConfig{Handler: handler}}
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
	return nginxsnippet.Validate(s)
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
	// Keep the 0600 MariaDB grants file (which holds the per-site passwords) in
	// sync: written when a site declares a database, removed when none do.
	{
		secretFiles, secretDeletes := mariaDBSecretFiles(snap)
		for k, v := range secretFiles {
			files[k] = v
		}
		deletes = append(deletes, secretDeletes...)
	}
	manager := runtime.Transactions
	if manager == nil {
		dbCheck := runtime.DatabaseCheck
		dbCheck = credentialedDatabaseCheck(dbCheck, snap.Sites)
		manager = defaultServiceTransaction(store.Root, runtime, snap.Config.Rebuild, transaction.CompositeHealth{
			desiredHealth{systemd: runtime.Services, name: "nginx", running: true},
			siteTransactionHealth(runtime, snap),
			dbCheck,
		})
	}
	affected := []string{"nginx", "nginx-config"}
	if action == "link" {
		affected = append(affected, "site:"+site.ID)
		if site.MariaDB != nil {
			affected = append(affected, "database:"+site.MariaDB.Database)
		}
	}
	result, e := manager.Apply(cmd.Context(), transaction.Request{Files: files, Deletes: deletes, CandidateModule: "generated/nixcp-module.nix", Affected: affected})
	if e != nil {
		return transactionError(e)
	}
	data := map[string]any{"id": site.ID, "domain": site.Domain, "php": site.PHP, "documentRoot": site.DocumentRoot, "handler": site.Nginx.Handler.Type, "phase": result.Phase}
	if site.MariaDB != nil {
		data["mariadb"] = map[string]any{"database": site.MariaDB.Database, "user": site.MariaDB.User, "password": site.MariaDB.Password}
	}
	if commandJSON(cmd) {
		return emitJSON(cmd, output.Success(action, result.Changed, data, nil))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", ui.OKLine(action), site.Domain, result.Phase)
	if site.MariaDB != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "  database: %s (user: %s, password: %s)\n", site.MariaDB.Database, site.MariaDB.User, site.MariaDB.Password)
	}
	return nil
}

// credentialedDatabaseCheck decorates the runtime MariaDB checker with the
// site-specific credentials (when known) so the post-switch health phase
// verifies each declared database as its own dedicated account, proving the
// generated user and password actually work on the switched system. Injected
// test fakes and custom checkers are left untouched.
func credentialedDatabaseCheck(check database.Checker, sites []state.SiteConfig) database.Checker {
	creds := map[string]database.DBCredentials{}
	for _, s := range sites {
		if s.MariaDB == nil {
			continue
		}
		creds[s.MariaDB.Database] = database.DBCredentials{Database: s.MariaDB.Database, User: s.MariaDB.User, Password: s.MariaDB.Password}
	}
	if len(creds) == 0 {
		return check
	}
	if local, ok := check.(database.LocalChecker); ok {
		local.Credentials = creds
		return local
	}
	if localPtr, ok := check.(*database.LocalChecker); ok {
		local := *localPtr
		local.Credentials = creds
		return &local
	}
	return check
}

func siteTransactionHealth(runtime Runtime, snap state.Snapshot) sitepkg.TransactionHealth {
	sites := make(map[string]state.SiteConfig, len(snap.Sites))
	for _, s := range snap.Sites {
		sites[s.ID] = s
	}
	checker := runtime.SiteChecker
	if checker == nil {
		checker = sitepkg.RealChecker{}
	}
	config := runtime.NginxConfig
	if config == nil {
		config = sitepkg.NginxConfigVerifier{Runner: runtime.Runner}
	}
	return sitepkg.TransactionHealth{Config: config, Checker: checker, Sites: sites}
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
	listCmd := &cobra.Command{Use: "list", Short: "List linked sites", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return runSitesList(c, runtime) }}
	showCmd := &cobra.Command{Use: "show <domain-or-site-id>", Short: "Show one site's manifest and runtime details", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error { return runSitesShow(c, runtime, a[0]) }}
	cmd.AddCommand(listCmd, showCmd)
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
		handler := s.Nginx.Handler.Type
		if handler == "template" && s.Nginx.Handler.Name != "" {
			handler = "template:" + s.Nginx.Handler.Name
		}
		line := fmt.Sprintf("%s %s enabled=%t php=%s root=%s handler=%s", s.ID, s.Domain, s.Enabled, s.PHP, s.DocumentRoot, handler)
		if s.MariaDB != nil {
			line += " db=" + s.MariaDB.Database
		}
		fmt.Fprintln(cmd.OutOrStdout(), line)
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
			if s.MariaDB != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  database: %s (user: %s, password: %s)\n", s.MariaDB.Database, s.MariaDB.User, s.MariaDB.Password)
			}
			return nil
		}
	}
	return apperrors.New("site_not_found", "site was not found", "Run: ncp sites list", apperrors.ExitCodePrecond)
}
