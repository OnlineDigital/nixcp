package command

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
)

// tuiBackendApp builds a runtime mirroring serviceRuntime with the pieces
// the TUI backend needs (state, fake systemd, fake rebuild transactions).
func tuiBackendApp(t *testing.T) (Runtime, *fakeSystemd, *fakeRebuild) {
	t.Helper()
	u, e := user.Current()
	if e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	cfg := initialConfig(u, 1000, 1000, "users", "", false)
	cfg.Owner.Home = home
	cfg.Owner.UID = os.Getuid()
	cfg.Owner.GID = os.Getgid()
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}
	s := state.NewStore(home)
	if e = s.Initialize(cfg); e != nil {
		t.Fatal(e)
	}
	sys := &fakeSystemd{actual: service.Actual{Active: true, Enabled: true, Health: "healthy"}}
	reb := &fakeRebuild{}
	rt := defaultRuntime()
	rt.Services = sys
	rt.Runner = &execx.FakeRunner{}
	rt.StateHome = home
	rt.Transactions = &transaction.Manager{Root: filepath.Join(home, ".nixcp"), Locker: transaction.FlockLocker{Path: filepath.Join(home, ".nixcp", "lock")}, Rebuilder: reb, Health: desiredHealth{systemd: sys, name: service.Nginx, running: true}}
	return rt, sys, reb
}

// backendFor wires a commandBackend over the test runtime.
func backendFor(rt Runtime) *commandBackend {
	return &commandBackend{runtime: rt, opts: []RuntimeOption{func(r *Runtime) { *r = rt }}}
}

// TestTUIBackendSnapshotReadsState verifies the read path maps config.yaml
// and sites into the TUI's shape.
func TestTUIBackendSnapshotReadsState(t *testing.T) {
	rt, _, _ := tuiBackendApp(t)
	b := backendFor(rt)
	snap, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Configured {
		t.Fatal("expected configured snapshot")
	}
	if len(snap.PHPInstalled) != 1 || snap.PHPInstalled[0] != "8.3" {
		t.Fatalf("php installed = %v", snap.PHPInstalled)
	}
	if snap.PHPDefault != "8.3" {
		t.Fatalf("php default = %q", snap.PHPDefault)
	}
	found := false
	for _, svc := range snap.Services {
		if svc.Name == "nginx" {
			found = true
		}
	}
	if !found {
		t.Fatalf("services missing nginx: %+v", snap.Services)
	}
	if snap.StateRoot == "" {
		t.Fatal("state root not reported")
	}
}

// TestTUIBackendServiceStatus reads actual state through the adapter.
func TestTUIBackendServiceStatus(t *testing.T) {
	rt, sys, _ := tuiBackendApp(t)
	b := backendFor(rt)
	sys.actual = service.Actual{Active: true, Enabled: true, Health: "healthy"}
	actual, err := b.ServiceStatus(context.Background(), "nginx")
	if err != nil {
		t.Fatal(err)
	}
	if !actual.Active {
		t.Fatal("expected active nginx")
	}
	// a failing adapter surfaces as an error
	sys.err = &fakeErr{}
	if _, err := b.ServiceStatus(context.Background(), "nginx"); err == nil {
		t.Fatal("expected adapter error to surface")
	}
}

// homePathRx matches the tempdir owner.home line so twin runs compare equal.
var homePathRx = regexp.MustCompile(`home: /tmp/[^\s]+`)

type fakeErr struct{}

func (fakeErr) Error() string { return "systemd down" }

// TestTUIBackendMutationRunsCLIPipeline verifies mutations launched by the
// TUI execute the real CLI pipeline in-process (preconditions + transaction).
func TestTUIBackendMutationRunsCLIPipeline(t *testing.T) {
	rt, _, reb := tuiBackendApp(t)
	// nginx must be installed before stop is allowed (CLI precondition)
	store := state.NewStore(rt.StateHome)
	snap, e := store.Load()
	if e != nil {
		t.Fatal(e)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if e = store.WriteSnapshot(snap); e != nil {
		t.Fatal(e)
	}
	b := backendFor(rt)
	res, err := b.ServiceAction(context.Background(), "nginx", "stop")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatalf("expected changed result, got %+v", res)
	}
	if len(reb.calls) == 0 {
		t.Fatal("mutation did not reach the transaction rebuild")
	}
}

// TestTUIBackendMutationRejectsInvalidInput verifies CLI precondition checks
// fire identically through the TUI backend (php version not installed).
func TestTUIBackendMutationRejectsInvalidInput(t *testing.T) {
	rt, _, _ := tuiBackendApp(t)
	b := backendFor(rt)
	_, err := b.PHPInstall(context.Background(), "7.4")
	if err == nil {
		t.Fatal("expected precondition failure for unsupported version")
	}
}

// TestTUIBackendNotConfiguredSnapshot verifies the unconfigured host maps
// to Configured=false without an error.
func TestTUIBackendNotConfiguredSnapshot(t *testing.T) {
	home := t.TempDir()
	rt := defaultRuntime()
	rt.StateHome = home
	rt.Runner = &execx.FakeRunner{}
	b := backendFor(rt)
	snap, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("unconfigured snapshot must not error: %v", err)
	}
	if snap.Configured {
		t.Fatal("expected unconfigured snapshot")
	}
}

// TestBareRootKeepsVersionWhenNotTTY pins the script-safety contract: bare
// ncp prints the version banner on non-TTY stdio.
func TestBareRootKeepsVersionWhenNotTTY(t *testing.T) {
	root, e := NewRootCommand(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{})
	if code := executeRoot(root); code != 0 {
		t.Fatalf("bare root exited %d", code)
	}
	if out.String() != "NixCP CLI 0.0.0\n" {
		t.Fatalf("unexpected banner %q", out.String())
	}
}

// TestTUICommandExists pins the tui command wiring.
func TestTUICommandExists(t *testing.T) {
	root, e := NewRootCommand(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	cmd, _, e := root.Find([]string{"tui"})
	if e != nil || cmd == nil {
		t.Fatalf("missing tui command (err=%v)", e)
	}
	if cmd.Short == "" {
		t.Fatal("tui command needs a synopsis")
	}
}

// TestTUIBackendYAMLEquivalenceWithCLI pins the acceptance criterion: a
// mutation launched through the TUI backend lands in exactly the same
// config.yaml bytes as the same mutation run through the CLI command.
func TestTUIBackendYAMLEquivalenceWithCLI(t *testing.T) {
	// run the same mutation twice in twin state homes: once via the TUI
	// backend, once via the parsed CLI root; compare config.yaml bytes.
	run := func(viaTUI bool) string {
		rt, _, _ := tuiBackendApp(t)
		store := state.NewStore(rt.StateHome)
		snap, e := store.Load()
		if e != nil {
			t.Fatal(e)
		}
		snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
		if e = store.WriteSnapshot(snap); e != nil {
			t.Fatal(e)
		}
		if viaTUI {
			b := backendFor(rt)
			if _, err := b.ServiceAction(context.Background(), "nginx", "stop"); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, e := execute(t, rt, "--json", "service", "nginx", "stop"); e != nil {
				t.Fatal(e)
			}
		}
		raw, e := os.ReadFile(filepath.Join(rt.StateHome, ".nixcp", "config.yaml"))
		if e != nil {
			t.Fatal(e)
		}
		return string(raw)
	}
	// the twin state homes differ only by tempdir path in owner.home and
	// possibly trailing newlines; normalize both before comparing bytes
	norm := func(y string) string {
		y = homePathRx.ReplaceAllString(y, "home: HOME")
		return strings.TrimRight(y, "\n")
	}
	tuiYAML := norm(run(true))
	cliYAML := norm(run(false))
	if tuiYAML != cliYAML {
		t.Fatalf("TUI and CLI produced different config.yaml:\nTUI:\n%s\nCLI:\n%s", tuiYAML, cliYAML)
	}
	if !strings.Contains(tuiYAML, "desiredState: stopped") {
		t.Fatalf("expected nginx stopped in YAML:\n%s", tuiYAML)
	}
}
