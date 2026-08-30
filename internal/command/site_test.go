package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

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
	return state.ConfigSnapshot{SchemaVersion: 2, Owner: state.Owner{Username: "u", UID: os.Getuid(), Group: "g", GID: os.Getgid(), Home: home}, Platform: state.Platform{System: "x86_64-linux"}, Rebuild: state.RebuildConfig{Mode: "traditional"}, Services: state.ServiceStates{Nginx: state.ServiceConfig{DesiredState: "stopped"}, MariaDB: state.ServiceConfig{DesiredState: "stopped"}, Redis: state.ServiceConfig{DesiredState: "stopped"}}, PHP: state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}}
}
