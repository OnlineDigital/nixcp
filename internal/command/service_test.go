package command

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
)

type fakeSystemd struct {
	actual   service.Actual
	restarts int
	err      error
}

func (f *fakeSystemd) Status(context.Context, service.Name) (service.Actual, error) {
	return f.actual, f.err
}
func (f *fakeSystemd) Restart(context.Context, service.Name) error { f.restarts++; return f.err }

type fakeRebuild struct{ calls []string }

func (f *fakeRebuild) CurrentGeneration(context.Context) (string, error) {
	f.calls = append(f.calls, "current")
	return "/nix/store/old", nil
}
func (f *fakeRebuild) Build(context.Context, string) error {
	f.calls = append(f.calls, "build")
	return nil
}
func (f *fakeRebuild) Switch(context.Context) error { f.calls = append(f.calls, "switch"); return nil }
func (f *fakeRebuild) Rollback(context.Context, string) error {
	f.calls = append(f.calls, "rollback")
	return nil
}
func serviceRuntime(t *testing.T) (Runtime, *fakeSystemd, *fakeRebuild) {
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
func execute(t *testing.T, rt Runtime, args ...string) (string, error) {
	t.Helper()
	root, e := NewRootCommand(context.Background(), func(r *Runtime) { *r = rt })
	if e != nil {
		t.Fatal(e)
	}
	b := new(bytes.Buffer)
	root.SetOut(b)
	root.SetArgs(args)
	e = root.Execute()
	return b.String(), e
}
func TestServiceInstallTransactionAndNoop(t *testing.T) {
	rt, _, reb := serviceRuntime(t)
	if _, e := execute(t, rt, "service", "nginx", "install"); e != nil {
		t.Fatal(e)
	}
	if got := len(reb.calls); got != 3 {
		t.Fatalf("calls=%v", reb.calls)
	}
	if out, e := execute(t, rt, "nginx", "install", "--json"); e != nil {
		t.Fatal(e)
	} else if !bytes.Contains([]byte(out), []byte(`"changed":false`)) {
		t.Fatalf("not noop: %s", out)
	}
}
func TestServiceStatusDriftJSONAndAlias(t *testing.T) {
	rt, sys, _ := serviceRuntime(t)
	sys.actual = service.Actual{Active: true, Enabled: false, Health: "healthy"}
	out, e := execute(t, rt, "nginx", "status", "--json")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains([]byte(out), []byte(`"drift":true`)) {
		t.Fatalf("missing drift %s", out)
	}
}
func TestRestartDoesNotTransaction(t *testing.T) {
	rt, sys, reb := serviceRuntime(t)
	snap, e := state.NewStore(rt.StateHome).Load()
	if e != nil {
		t.Fatal(e)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if e = state.NewStore(rt.StateHome).WriteSnapshot(snap); e != nil {
		t.Fatal(e)
	}
	if _, e = execute(t, rt, "service", "nginx", "restart"); e != nil {
		t.Fatal(e)
	}
	if sys.restarts != 1 || len(reb.calls) != 0 {
		t.Fatalf("restarts=%d rebuild=%v", sys.restarts, reb.calls)
	}
}
