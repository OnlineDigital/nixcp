package command

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
)

func doctorTestApp(t *testing.T, configure bool) (*ApplicationRoot, *execx.FakeRunner, string) {
	t.Helper()
	home := t.TempDir()
	runner := &execx.FakeRunner{}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	if configure {
		u, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		store := state.NewStore(home)
		cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
		cfg.Owner.Home = home
		cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}
		if err := store.Initialize(cfg); err != nil {
			t.Fatal(err)
		}
	}
	return app, runner, home
}

func TestDoctorUnconfiguredIsReadOnlyAndHealthy(t *testing.T) {
	app, _, home := doctorTestApp(t, false)
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"doctor"})
	code := app.Execute()
	if code != 0 {
		t.Fatalf("unconfigured doctor must not fail hard, got %d", code)
	}
	if !strings.Contains(buf.String(), "not-configured") {
		t.Fatalf("expected not-configured note, got %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".nixcp")); !os.IsNotExist(err) {
		t.Fatal("doctor must not create state directories")
	}
}

func TestDoctorConfiguredReportsChecks(t *testing.T) {
	app, _, _ := doctorTestApp(t, true)
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"doctor"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("doctor on healthy configured state failed: %d\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"state", "module", "config", "artifacts", "import", "toolchain", "service.nginx"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected check %q in output:\n%s", want, out)
		}
	}
}

func TestDoctorJSONEnvelopeShape(t *testing.T) {
	app, _, _ := doctorTestApp(t, true)
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"--json", "doctor"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	s := buf.String()
	for _, want := range []string{`"command":"doctor"`, `"checks"`, `"configured":true`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
}

func TestDoctorToolchainWarnWhenRebuildMissing(t *testing.T) {
	app, runner, _ := doctorTestApp(t, true)
	runner.Handle = func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 127, Stderr: "not found"}, nil
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"doctor"})
	// warn must not fail the whole doctor (only [FAIL] does)
	if code := app.Execute(); code != 0 {
		t.Fatalf("warn should not fail doctor, got %d\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "toolchain") {
		t.Fatalf("expected toolchain check: %s", buf.String())
	}
}

func TestStatusReportsActualFromSystemd(t *testing.T) {
	home := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSystemd{actual: service.Actual{Active: true, Enabled: true, Health: "healthy"}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{}), WithServices(fs))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"--json", "status"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	s := buf.String()
	for _, want := range []string{`"configured":true`, `"actual"`, `"nginx"`, `"healthy"`, `"desired"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
}

func TestStatusReportsPerServiceDriftInHumanAndJSON(t *testing.T) {
	home := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{}), WithServices(&fakeSystemd{actual: service.Actual{Active: false, Enabled: false, Health: "inactive"}}))
	if err != nil {
		t.Fatal(err)
	}
	var human bytes.Buffer
	app.Root.SetOut(&human)
	app.Root.SetArgs([]string{"status"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d: %s", code, human.String())
	}
	if !strings.Contains(human.String(), "nginx: desired=running") || !strings.Contains(human.String(), "drift=true") {
		t.Fatalf("human status does not expose drift: %s", human.String())
	}

	app.Root.SetOut(&human)
	human.Reset()
	app.Root.SetArgs([]string{"--json", "status"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d: %s", code, human.String())
	}
	for _, want := range []string{`"services"`, `"drift":["nginx"]`, `"actual":{"active":false`, `"drift":true`} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("missing %q in %s", want, human.String())
		}
	}
}

func TestStatusMarksUnavailableSystemdAsUnknown(t *testing.T) {
	// Use an explicitly failing probe so this assertion does not depend on the
	// host's systemd.
	dir := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = dir
	if err := state.NewStore(dir).Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	failing, err := New(context.Background(), WithStateHome(dir), WithServices(&fakeSystemd{err: errBoom{}}))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	failing.Root.SetOut(&buf)
	failing.Root.SetArgs([]string{"--json", "status"})
	if code := failing.Execute(); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	if !strings.Contains(buf.String(), `"actual":null`) || !strings.Contains(buf.String(), `"drift":null`) {
		t.Fatalf("unavailable systemd must be unknown, got %s", buf.String())
	}
}

func TestStatusDegradesWhenSystemdUnavailable(t *testing.T) {
	home := t.TempDir()
	app, _, _ := doctorTestApp(t, false)
	_ = home
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{}), WithServices(&fakeSystemd{err: errBoom{}}))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"status"})
	// unconfigured: not-configured line, no crash
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(buf.String(), "not-configured") {
		t.Fatalf("got %q", buf.String())
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func TestDoctorJSONFailsOnUnhealthyHost(t *testing.T) {
	home := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	// Corrupt the generated module AFTER initialization (a directory where
	// the regular module file should be) so the module diagnostic records
	// a hard FAIL while the state store itself still loads.
	moduleDir := filepath.Join(home, ".nixcp", "generated")
	if err := os.RemoveAll(moduleDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(moduleDir, "nixcp-module.nix"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{}))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"doctor", "--json"})
	code := app.Execute()
	if code == 0 {
		t.Fatalf("doctor --json on unhealthy host must not exit 0, got %d\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), `"healthy":false`) {
		t.Fatalf("expected healthy:false in JSON output, got:\n%s", buf.String())
	}
}

func TestDoctorFailsOnConfiguredServiceDrift(t *testing.T) {
	home := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err := state.NewStore(home).Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{}), WithServices(&fakeSystemd{actual: service.Actual{Active: false, Enabled: false, Health: "inactive"}}))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"doctor"})
	if code := app.Execute(); code == 0 {
		t.Fatalf("service drift must fail doctor: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "service.nginx") || !strings.Contains(buf.String(), "desired=running") {
		t.Fatalf("missing actionable service diagnostic: %s", buf.String())
	}
}
