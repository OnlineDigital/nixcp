package command

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/database"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/service"
	sitepkg "github.com/nixcp/nixcp/internal/site"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
)

func TestLinkThenUnlinkUsesTransactionalSiteState(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &execx.FakeRunner{Handle: func(cmd *execx.Command) (execx.Result, error) {
		if cmd.Name == "systemctl" {
			return execx.Result{ExitCode: 0}, nil
		}
		if cmd.Name == "ss" {
			return execx.Result{ExitCode: 0}, nil
		}
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) { rt.Services = testSystemd{}; rt.Transactions = testTransaction(home) })
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := testSiteConfig(home)
	if err = store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err = store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--json", "link", "example.test", "--php", "8.3", "--template", "laravel", "--path", project})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("link exit %d: out=%s err=%s", code, out, stderr)
	}
	snap, err = store.Load()
	if err != nil || len(snap.Sites) != 1 {
		t.Fatalf("sites: %v %#v", err, snap.Sites)
	}
	app.Root.SetArgs([]string{"unlink", "example.test"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("unlink exit %d", code)
	}
	snap, err = store.Load()
	if err != nil || len(snap.Sites) != 0 {
		t.Fatalf("sites after unlink: %v %#v", err, snap.Sites)
	}
}

func TestLinkRunsPostLinkHookWithSiteEnv(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	var hookCmd *execx.Command
	runner := &execx.FakeRunner{Handle: func(cmd *execx.Command) (execx.Result, error) {
		if cmd.Name == "sh" && len(cmd.Args) == 2 && cmd.Args[0] == "-c" {
			hookCmd = cmd
			return execx.Result{ExitCode: 0, Stdout: "registered " + os.Getenv("VHOST")}, nil
		}
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) { rt.Services = testSystemd{}; rt.Transactions = testTransaction(home) })
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	if err = store.Initialize(testSiteConfig(home)); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	snap.Config.Services.MariaDB = state.ServiceConfig{Installed: true, DesiredState: "running"}
	snap.Config.Hooks.PostLink = "notify-proxy $VHOST"
	if err = store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--json", "link", "example.test", "--php", "8.3", "--template", "laravel", "--mariadb", "app", "--path", project})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("link exit %d: out=%s err=%s", code, out, stderr)
	}
	if hookCmd == nil {
		t.Fatal("postLink hook was not executed")
	}
	env := map[string]string{}
	for _, kv := range hookCmd.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for key, want := range map[string]string{
		"VHOST":       "example.test",
		"PHP_VERSION": "8.3",
		"SITE_DIR":    project,
		"DB_NAME":     "app",
	} {
		if env[key] != want {
			t.Errorf("hook env %s=%q, want %q", key, env[key], want)
		}
	}
	if hookCmd.Args[1] != "notify-proxy $VHOST" {
		t.Errorf("hook command = %q", hookCmd.Args[1])
	}
	if !strings.Contains(out.String(), "registered") {
		t.Errorf("hook stdout not printed verbatim: %q", out.String())
	}
	// The site must remain linked regardless of hook behavior.
	snap, err = store.Load()
	if err != nil || len(snap.Sites) != 1 {
		t.Fatalf("site missing after hook: %v %#v", err, snap.Sites)
	}
}

func TestLinkPostLinkHookFailureDoesNotFailLink(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &execx.FakeRunner{Handle: func(cmd *execx.Command) (execx.Result, error) {
		if cmd.Name == "sh" {
			return execx.Result{ExitCode: 3, Stderr: "proxy unreachable"}, &execx.ProcessExitError{Cmd: []string{"sh", "-c", cmd.Args[1]}, ExitCode: 3}
		}
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) { rt.Services = testSystemd{}; rt.Transactions = testTransaction(home) })
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	if err = store.Initialize(testSiteConfig(home)); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	snap.Config.Hooks.PostLink = "notify-proxy $VHOST"
	if err = store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--json", "link", "example.test", "--php", "8.3", "--template", "laravel", "--path", project})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("link must succeed despite hook failure; exit %d: out=%s err=%s", code, out, stderr)
	}
	if !strings.Contains(out.String(), "postLink hook failed") {
		t.Errorf("hook failure not surfaced: %s", out)
	}
	if !strings.Contains(stderr.String(), "proxy unreachable") {
		t.Errorf("hook stderr not printed verbatim: %q", stderr.String())
	}
	snap, err = store.Load()
	if err != nil || len(snap.Sites) != 1 {
		t.Fatalf("site missing after failed hook: %v %#v", err, snap.Sites)
	}
}

func TestLinkWithMariaDBWritesPrivateSecretFile(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &execx.FakeRunner{Handle: func(cmd *execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) { rt.Services = testSystemd{}; rt.Transactions = testTransaction(home) })
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	if err = store.Initialize(testSiteConfig(home)); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	snap.Config.Services.MariaDB = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err = store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	app.Root.SetArgs([]string{"--json", "link", "example.test", "--php", "8.3", "--template", "laravel", "--mariadb", "app", "--path", project})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("link exit %d: out=%s err=%s", code, out, stderr)
	}

	secretPath := filepath.Join(home, ".nixcp", "secrets", "mariadb", "accounts.sql")
	b, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("private accounts.sql not written: %v", err)
	}
	sql := string(b)
	if !strings.Contains(sql, "CREATE USER IF NOT EXISTS 'app'@'localhost'") {
		t.Fatalf("accounts.sql missing CREATE USER: %s", sql)
	}

	// The generated module must reference the private file by path and never
	// embed the password.
	snap, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(home, ".nixcp", "generated", "nixcp-module.nix"))
	if err != nil {
		t.Fatalf("module not written: %v", err)
	}
	if !strings.Contains(string(module), "secrets/mariadb/accounts.sql") {
		t.Fatalf("module must reference the private accounts.sql path")
	}
	if pw := snap.Sites[0].MariaDB.Password; strings.Contains(string(module), pw) {
		t.Fatalf("module leaked the site MariaDB password %q", pw)
	}

	// Unlinking must remove the stale private grants file.
	app.Root.SetArgs([]string{"unlink", "example.test"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("unlink exit %d", code)
	}
	if _, err := os.Stat(secretPath); !os.IsNotExist(err) {
		t.Fatalf("expected accounts.sql removed after unlink, stat err=%v", err)
	}
}
func TestLinkRejectsUnsafeSnippet(t *testing.T) {
	if err := validateSnippet("server { listen 443; }"); err == nil {
		t.Fatal("expected unsafe snippet rejection")
	}
}

type recordingNginxConfig struct{ calls int }

func (c *recordingNginxConfig) Verify(context.Context) error { c.calls++; return nil }

type healthySiteProbe struct{ calls int }

func (p *healthySiteProbe) CheckSite(_ context.Context, domain, id string, enabled bool) sitepkg.HealthStatus {
	p.calls++
	return sitepkg.HealthStatus{Domain: domain, SiteID: id, DesiredOn: enabled, SocketOK: true, HTTPOK: true, HTTPStatus: 200}
}

type recordingDatabaseHealth struct{ affected []string }

func (h *recordingDatabaseHealth) Check(_ context.Context, affected []string) error {
	h.affected = append([]string(nil), affected...)
	return nil
}

type appErrorProbe struct{ calls int }

func (p *appErrorProbe) CheckSite(_ context.Context, domain, id string, enabled bool) sitepkg.HealthStatus {
	p.calls++
	return sitepkg.HealthStatus{Domain: domain, SiteID: id, DesiredOn: enabled, SocketOK: true, HTTPOK: false, HTTPStatus: 500, ProblemCode: "site_not_reachable"}
}

type socketMissingProbe struct{ calls int }

func (p *socketMissingProbe) CheckSite(_ context.Context, domain, id string, enabled bool) sitepkg.HealthStatus {
	p.calls++
	return sitepkg.HealthStatus{Domain: domain, SiteID: id, DesiredOn: enabled, SocketOK: false, HTTPOK: false, HTTPStatus: 0, ProblemCode: "php_fpm_socket_missing"}
}

func TestLinkSucceedsWithWarningWhenApplicationReturnsError(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	probe := &appErrorProbe{}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{Handle: func(command *execx.Command) (execx.Result, error) {
		if command.Name == "readlink" {
			return execx.Result{Stdout: "/nix/store/test-system"}, nil
		}
		return execx.Result{}, nil
	}}), WithServices(testSystemd{}), WithNginxConfigVerifier(&recordingNginxConfig{}), WithSiteChecker(probe), WithDatabaseChecker(&testHealth{}))
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := testSiteConfig(home)
	cfg.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	cfg.Services.MariaDB = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--json", "link", "broken.example", "--php", "8.3", "--path", project, "--mariadb", "app"})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("link exit %d despite app-only 500 (socket ok): out=%s err=%s", code, out, stderr)
	}
	if probe.calls != 1 {
		t.Fatalf("expected exactly one site probe, got %d", probe.calls)
	}
	envelope := envelopeFromJSON(t, out.String())
	if !envelope.Ok {
		t.Fatalf("expected success envelope")
	}
	if warnings, ok := envelope.Warnings.([]any); !ok || len(warnings) == 0 {
		t.Fatalf("expected at least one warning, got %#v", envelope.Warnings)
	} else if !strings.Contains(fmt.Sprint(warnings[0]), "broken.example") {
		t.Fatalf("warning does not mention the site: %#v", warnings[0])
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range snap.Sites {
		if s.Domain == "broken.example" && s.MariaDB != nil && s.MariaDB.Database == "app" {
			found = true
		}
	}
	if !found {
		t.Fatalf("site+database must survive when only the application errors, sites=%#v", snap.Sites)
	}
}

func TestLinkStillFailsWhenPHPSocketMissing(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	probe := &socketMissingProbe{}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{Handle: func(command *execx.Command) (execx.Result, error) {
		if command.Name == "readlink" {
			return execx.Result{Stdout: "/nix/store/test-system"}, nil
		}
		return execx.Result{}, nil
	}}), WithServices(testSystemd{}), WithNginxConfigVerifier(&recordingNginxConfig{}), WithSiteChecker(probe))
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := testSiteConfig(home)
	cfg.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"link", "socket.example", "--php", "8.3", "--path", project})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code == 0 {
		t.Fatal("link must fail when the PHP-FPM socket is missing")
	}
	if probe.calls != 1 {
		t.Fatalf("expected exactly one site probe, got %d", probe.calls)
	}
	if !strings.Contains(stderr.String(), "service_transaction_failed") {
		t.Fatalf("expected service_transaction_failed, err=%s", stderr.String())
	}
}

func TestLinkDefaultTransactionRunsSiteAndDeclaredDatabaseHealth(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	config := &recordingNginxConfig{}
	probe := &healthySiteProbe{}
	database := &recordingDatabaseHealth{}
	runner := &execx.FakeRunner{Handle: func(command *execx.Command) (execx.Result, error) {
		if command.Name == "readlink" {
			return execx.Result{Stdout: "/nix/store/test-system"}, nil
		}
		return execx.Result{}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), WithServices(testSystemd{}), WithNginxConfigVerifier(config), WithSiteChecker(probe), WithDatabaseChecker(database))
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := testSiteConfig(home)
	cfg.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	cfg.Services.MariaDB = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--json", "link", "database.example", "--php", "8.3", "--path", project, "--mariadb", "app"})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("link exit %d: out=%s err=%s", code, out, stderr)
	}
	if config.calls != 1 || probe.calls != 1 {
		t.Fatalf("nginx checks=%d site probes=%d", config.calls, probe.calls)
	}
	if !containsString(database.affected, "database:app") || !containsString(database.affected, "site:"+state.GenerateStableSiteID("database.example", nil)) {
		t.Fatalf("affected resources=%q", database.affected)
	}
}

func TestCredentialedDatabaseCheckDecoratesLocalChecker(t *testing.T) {
	sites := []state.SiteConfig{
		{ID: "a", MariaDB: &state.MariaDBConfig{Database: "app", User: "app", Password: "pw1234567890abcdef"}},
		{ID: "b"},
	}
	check := database.LocalChecker{Runner: &execx.FakeRunner{}}
	decorated := credentialedDatabaseCheck(check, sites)
	local, ok := decorated.(database.LocalChecker)
	if !ok {
		t.Fatalf("expected LocalChecker to be preserved, got %T", decorated)
	}
	if len(local.Credentials) != 1 {
		t.Fatalf("expected exactly 1 credential, got %d", len(local.Credentials))
	}
	cred := local.Credentials["app"]
	if cred.User != "app" || cred.Password != "pw1234567890abcdef" {
		t.Fatalf("bad credential: %#v", cred)
	}
}

func TestCredentialedDatabaseCheckLeavesForeignCheckerUntouched(t *testing.T) {
	foreign := testHealth{}
	if got := credentialedDatabaseCheck(foreign, []state.SiteConfig{{ID: "a", MariaDB: &state.MariaDBConfig{Database: "app", User: "app", Password: "pw1234567890abcdef"}}}); got != foreign {
		t.Fatalf("foreign checker must be returned unchanged, got %T", got)
	}
}

func TestSitesListShowsEnabledHandlerAndDatabase(t *testing.T) {
	u, _ := user.Current()
	home := t.TempDir()
	store := state.NewStore(home)
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	cfg.Services.MariaDB = state.ServiceConfig{Installed: true, DesiredState: "running"}
	cfg.MariaDBRegistry.Databases = []string{"app"}
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3", "8.4"}, GlobalDefault: "8.3"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Sites = []state.SiteConfig{
		{SchemaVersion: 2, ID: "a", Enabled: true, Domain: "a.example", ProjectPath: home, DocumentRoot: home, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "template", Name: "laravel"}}},
		{SchemaVersion: 2, ID: "b", Enabled: false, Domain: "b.example", ProjectPath: home, DocumentRoot: home, PHP: "8.4", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}, MariaDB: &state.MariaDBConfig{Database: "app", User: "app", Password: "nixcpfixturepass123456"}},
	}
	if err := store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	rt := defaultRuntime()
	rt.Runner = &execx.FakeRunner{}
	rt.StateHome = home
	root, err := NewRootCommand(context.Background(), func(r *Runtime) { *r = rt })
	if err != nil {
		t.Fatal(err)
	}
	b := &bytes.Buffer{}
	root.SetOut(b)
	root.SetArgs([]string{"sites", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"a.example", "enabled=true", "handler=template:laravel", "b.example", "enabled=false", "handler=generic", "db=app"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sites list output missing %q:\n%s", want, out)
		}
	}
}

type testRebuilder struct{}

func (testRebuilder) CurrentGeneration(context.Context) (string, error) { return "old", nil }
func (testRebuilder) Build(context.Context, string) error               { return nil }
func (testRebuilder) Switch(context.Context) error                      { return nil }
func (testRebuilder) Rollback(context.Context, string) error            { return nil }

type testHealth struct{}

func (testHealth) Check(context.Context, []string) error { return nil }
func testTransaction(home string) *transaction.Manager {
	return &transaction.Manager{Root: state.NewStore(home).Root, Locker: transaction.FlockLocker{Path: filepath.Join(state.NewStore(home).Root, "lock")}, Rebuilder: testRebuilder{}, Health: testHealth{}}
}

type testSystemd struct{}

func (testSystemd) Status(context.Context, service.Name) (service.Actual, error) {
	return service.Actual{Active: true, Enabled: true, Health: "healthy"}, nil
}
func (testSystemd) Restart(context.Context, service.Name) error { return nil }

func testSiteConfig(home string) state.ConfigSnapshot {
	return state.ConfigSnapshot{SchemaVersion: 2, Owner: state.Owner{Username: "u", UID: os.Getuid(), Group: "g", GID: os.Getgid(), Home: home}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{DesiredState: "stopped"}, MariaDB: state.ServiceConfig{DesiredState: "stopped"}, Valkey: state.ServiceConfig{DesiredState: "stopped"}}, PHP: state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}}
}

func TestUnlinkRunsPostUnlinkHookWithSiteEnv(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	var hookCmd *execx.Command
	runner := &execx.FakeRunner{Handle: func(cmd *execx.Command) (execx.Result, error) {
		if cmd.Name == "sh" && len(cmd.Args) == 2 && cmd.Args[0] == "-c" {
			hookCmd = cmd
			return execx.Result{ExitCode: 0, Stdout: "deregistered example.test"}, nil
		}
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) { rt.Services = testSystemd{}; rt.Transactions = testTransaction(home) })
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	if err = store.Initialize(testSiteConfig(home)); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	snap.Config.Hooks.PostUnlink = "notify-proxy remove $VHOST"
	snap.Sites = append(snap.Sites, state.SiteConfig{SchemaVersion: 2, ID: "example-test", Enabled: true, Domain: "example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}})
	if err = store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"unlink", "example.test", "--yes"})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("unlink exit %d: out=%s err=%s", code, out, stderr)
	}
	if hookCmd == nil {
		t.Fatal("postUnlink hook was not executed")
	}
	env := map[string]string{}
	for _, kv := range hookCmd.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for key, want := range map[string]string{
		"VHOST":       "example.test",
		"PHP_VERSION": "8.3",
		"SITE_DIR":    project,
	} {
		if env[key] != want {
			t.Errorf("hook env %s=%q, want %q", key, env[key], want)
		}
	}
	if _, hasDB := env["DB_NAME"]; hasDB {
		t.Errorf("DB_NAME must not be set for a site without a database")
	}
	if hookCmd.Args[1] != "notify-proxy remove $VHOST" {
		t.Errorf("hook command = %q", hookCmd.Args[1])
	}
	if !strings.Contains(out.String(), "deregistered example.test") {
		t.Errorf("hook stdout not printed verbatim: %q", out.String())
	}
	snap, err = store.Load()
	if err != nil || len(snap.Sites) != 0 {
		t.Fatalf("site still present after unlink: %v %#v", err, snap.Sites)
	}
}

func TestUnlinkPostUnlinkHookFailureDoesNotFailUnlink(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	runner := &execx.FakeRunner{Handle: func(cmd *execx.Command) (execx.Result, error) {
		if cmd.Name == "sh" {
			return execx.Result{ExitCode: 2, Stderr: "proxy timeout"}, &execx.ProcessExitError{Cmd: []string{"sh", "-c", cmd.Args[1]}, ExitCode: 2}
		}
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) { rt.Services = testSystemd{}; rt.Transactions = testTransaction(home) })
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	if err = store.Initialize(testSiteConfig(home)); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	snap.Config.Hooks.PostUnlink = "notify-proxy remove $VHOST"
	snap.Sites = append(snap.Sites, state.SiteConfig{SchemaVersion: 2, ID: "example-test", Enabled: true, Domain: "example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}})
	if err = store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--json", "unlink", "example.test", "--yes"})
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app.Root.SetOut(out)
	app.Root.SetErr(stderr)
	if code := app.Execute(); code != 0 {
		t.Fatalf("unlink must succeed despite hook failure; exit %d: out=%s err=%s", code, out, stderr)
	}
	if !strings.Contains(out.String(), "postUnlink hook failed") {
		t.Errorf("hook failure not surfaced: %s", out)
	}
	if !strings.Contains(stderr.String(), "proxy timeout") {
		t.Errorf("hook stderr not printed verbatim: %q", stderr.String())
	}
	snap, err = store.Load()
	if err != nil || len(snap.Sites) != 0 {
		t.Fatalf("site still present after unlink: %v %#v", err, snap.Sites)
	}
}
