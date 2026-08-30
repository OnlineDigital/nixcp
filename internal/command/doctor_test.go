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
	for _, want := range []string{"state", "module", "artifacts", "import", "toolchain"} {
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
