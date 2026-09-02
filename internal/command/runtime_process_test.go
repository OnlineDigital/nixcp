package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
)

func runtimeTest(t *testing.T) (Runtime, *execx.FakeRunner, string) {
	t.Helper()
	home := t.TempDir()
	runner := &execx.FakeRunner{Handle: func(c *execx.Command) (execx.Result, error) {
		if c.Name == "crontab" && len(c.Args) == 1 && c.Args[0] == "-l" {
			return execx.Result{ExitCode: 1}, &execx.ProcessExitError{ExitCode: 1}
		}
		return execx.Result{}, nil
	}}
	rt := defaultRuntime()
	rt.StateHome = home
	rt.Runner = runner
	return rt, runner, home
}

func TestRuntimeServiceSlugUsesLinkedSiteID(t *testing.T) {
	rt, _, home := runtimeTest(t)
	project := t.TempDir()
	store := state.NewStore(home)
	config := testSiteConfig(home)
	config.Services.Nginx.Installed = true
	snap := state.Snapshot{Config: config, Sites: []state.SiteConfig{{SchemaVersion: 2, ID: "deals-p-ohost-cloud", Enabled: true, Domain: "deals.p.ohost.cloud", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}}}}
	if err := store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if got := runtimeServiceSlug(rt, project); got != "deals-p-ohost-cloud" {
		t.Fatalf("slug = %q", got)
	}
}

func TestEnableQueueViteAndPulseForwardFlags(t *testing.T) {
	for _, target := range []struct{ name, want string }{
		{"queue", `"queue:work" "--tries=3"`},
		{"horizon", `"horizon" "--balance=auto"`},
		{"vite", `"--host"`},
		{"pulse", `"pulse:check" "--no-interaction"`},
	} {
		t.Run(target.name, func(t *testing.T) {
			rt, _, home := runtimeTest(t)
			project := t.TempDir()
			if target.name == "vite" {
				config := testSiteConfig(home)
				config.Services.Nginx.Installed = true
				config.Services.Nginx.DesiredState = "running"
				snap := state.Snapshot{Config: config, Sites: []state.SiteConfig{{SchemaVersion: 2, ID: "vite-example", Enabled: true, Domain: "vite.example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}}}}
				if err := state.NewStore(home).Initialize(config); err != nil {
					t.Fatal(err)
				}
				if err := state.NewStore(home).WriteSnapshot(snap); err != nil {
					t.Fatal(err)
				}
				rt.Transactions = testTransaction(home)
			}
			old, _ := os.Getwd()
			defer os.Chdir(old)
			if err := os.Chdir(project); err != nil {
				t.Fatal(err)
			}
			flag := "--tries=3"
			if target.name == "horizon" {
				flag = "--balance=auto"
			}
			if target.name == "vite" {
				flag = "--host"
			}
			if target.name == "pulse" {
				flag = "--no-interaction"
			}
			if _, err := execute(t, rt, "enable", target.name, flag); err != nil {
				t.Fatal(err)
			}
			slug := runtimeProjectSlug(project)
			if target.name == "vite" {
				slug = "vite-example"
			}
			body, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", runtimeTarget(target.name).unit(slug)))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), target.want) {
				t.Fatalf("unit did not preserve flag:\n%s", body)
			}
		})
	}
}

func TestEnableVitePersistsPortAndNginxProxy(t *testing.T) {
	rt, _, home := runtimeTest(t)
	project := t.TempDir()
	config := testSiteConfig(home)
	config.Services.Nginx.Installed = true
	config.Services.Nginx.DesiredState = "running"
	snap := state.Snapshot{Config: config, Sites: []state.SiteConfig{{SchemaVersion: 2, ID: "vite-example", Enabled: true, Domain: "vite.example.test", ProjectPath: project, DocumentRoot: project, PHP: "8.3", Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "custom", Path: filepath.Join(project, "nginx.conf"), Content: "try_files $uri $uri/ /index.php?$query_string;"}}}}}
	if err := os.WriteFile(filepath.Join(project, "nginx.conf"), []byte("try_files $uri $uri/ /index.php?$query_string;"), 0644); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	if err := store.Initialize(config); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	rt.Transactions = testTransaction(home)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, rt, "enable", "vite"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	port := updated.Sites[0].Vite.Port
	if port < 30000 || port >= 50000 {
		t.Fatalf("unexpected Vite port %d", port)
	}
	unit, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", runtimeVite.unit("vite-example")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "Environment=PORT="+fmt.Sprint(port)) || !strings.Contains(string(unit), "\"--port="+fmt.Sprint(port)+"\"") {
		t.Fatalf("unit does not use persisted Vite port:\n%s", unit)
	}
	module, err := os.ReadFile(filepath.Join(store.Root, "generated", "nixcp-module.nix"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"locations.\"~ ^/(@vite|resources)/\"", "proxy_pass http://127.0.0.1:" + fmt.Sprint(port), "proxy_set_header Upgrade $http_upgrade", "try_files $uri $uri/ /index.php?$query_string"} {
		if !strings.Contains(string(module), want) {
			t.Fatalf("generated module missing %q:\n%s", want, module)
		}
	}
}

func TestEnableReverbWritesUserUnitAndForwardsFlags(t *testing.T) {
	rt, runner, home := runtimeTest(t)
	project := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, rt, "enable", "reverb", "--host=0.0.0.0", "--port", "6001"); err != nil {
		t.Fatal(err)
	}
	unitName := runtimeReverb.unit(runtimeProjectSlug(project))
	unit, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", unitName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	wantExec := "ExecStart=" + systemdArgs([]string{runtimeNCPBinary(), "php", "artisan", "reverb:start", "--host=0.0.0.0", "--port", "6001"})
	if !strings.Contains(text, "WorkingDirectory="+systemdPath(project)) || !strings.Contains(text, "Environment=PATH="+systemdPath(runtimePath())) || !strings.Contains(text, wantExec) {
		t.Fatalf("bad unit:\n%s", text)
	}
	if len(runner.Runs) != 2 || strings.Join(runner.Runs[0].Args, " ") != "--user daemon-reload" || strings.Join(runner.Runs[1].Args, " ") != "--user enable --now "+unitName {
		t.Fatalf("systemctl runs: %#v", runner.Runs)
	}
}

func TestDisableQueueStopsDeletesAndReloads(t *testing.T) {
	rt, runner, home := runtimeTest(t)
	project := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	unitName := runtimeQueue.unit(runtimeProjectSlug(project))
	path := filepath.Join(home, ".config", "systemd", "user", unitName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, rt, "disable", "queue"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unit remains: %v", err)
	}
	if len(runner.Runs) != 2 || strings.Join(runner.Runs[0].Args, " ") != "--user disable --now "+unitName || strings.Join(runner.Runs[1].Args, " ") != "--user daemon-reload" {
		t.Fatalf("runs: %#v", runner.Runs)
	}
}

func TestRestartRoutesUserAndSystemTargets(t *testing.T) {
	rt, runner, _ := runtimeTest(t)
	project := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, rt, "restart", "octane"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(runner.Runs[0].Args, " ") != "--user restart "+runtimeOctane.unit(runtimeProjectSlug(project)) {
		t.Fatalf("user restart: %#v", runner.Runs[0])
	}
	runner.Runs = nil
	if _, err := execute(t, rt, "restart", "php"); err != nil {
		t.Fatal(err)
	}
	if runner.Runs[0].Name != "sudo" || strings.Join(runner.Runs[0].Args, " ") != "-- systemctl restart phpfpm.service" {
		t.Fatalf("system restart: %#v", runner.Runs[0])
	}
}

func TestEnableScheduleInstallsManagedCronEntry(t *testing.T) {
	rt, runner, _ := runtimeTest(t)
	var installed string
	runner.Handle = func(c *execx.Command) (execx.Result, error) {
		if c.Name == "crontab" && len(c.Args) == 1 && c.Args[0] == "-l" {
			return execx.Result{ExitCode: 1}, &execx.ProcessExitError{ExitCode: 1}
		}
		if c.Name == "crontab" && len(c.Args) == 1 {
			body, err := os.ReadFile(c.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			installed = string(body)
		}
		return execx.Result{}, nil
	}
	project := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, rt, "enable", "schedule"); err != nil {
		t.Fatal(err)
	}
	if len(runner.Runs) != 2 || runner.Runs[1].Name != "crontab" {
		t.Fatalf("runs: %#v", runner.Runs)
	}
	want := "* * * * * PATH=" + shellQuote(runtimePath()) + "; export PATH; cd '" + project + "' && " + shellQuote(runtimeNCPBinary()) + " php artisan schedule:run >/dev/null 2>&1"
	if !strings.Contains(installed, want) {
		t.Fatalf("bad cron: %s", installed)
	}
}

func TestHumanArtisanLiveFailureSuppressesWrapper(t *testing.T) {
	rt, runner, home := runtimeTest(t)
	_ = home
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "artisan"), []byte("<?php"), 0644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	_ = os.Chdir(project)
	// use state from the normal PHP test helper instead of recreating it
	_, _, configuredHome := phpTestApp(t)
	rt.StateHome = configuredHome
	runner.Handle = func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 1}, &execx.ProcessExitError{ExitCode: 1}
	}
	app, err := New(context.Background(), func(r *Runtime) { *r = rt })
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	app.Root.SetErr(&stderr)
	app.Root.SetArgs([]string{"artisan", "migrate"})
	if code := app.Execute(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stderr.String(), "artisan_execution_failed") {
		t.Fatalf("live wrapper leaked: %q", stderr.String())
	}
	if !runner.Runs[0].Interactive {
		t.Fatal("human artisan did not attach terminal")
	}
}
