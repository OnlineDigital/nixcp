package command

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
)

// Service-command orchestration contract: preconditions fail with their
// documented stable codes (never a stack trace), mutations route through
// the transaction manager (build → switch → health, rollback on failure),
// and the JSON error envelope carries the machine-readable code. These
// complement service_test.go, which covers the happy paths.

// requireAppErr asserts the command failed with the given stable code.
func requireAppErr(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected failure with code %q, got success", code)
	}
	appErr := errors.Normalize(err)
	if appErr.Code != code {
		t.Fatalf("expected code %q, got %q (message: %s)", code, appErr.Code, appErr.Message)
	}
}

// envelopeFromJSON parses stdout of a failed --json command.
func envelopeFromJSON(t *testing.T, out string) output.ErrorEnvelope {
	t.Helper()
	var env output.ErrorEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not a JSON error envelope: %v\n%s", err, out)
	}
	return env
}

// executeJSON runs the full application lifecycle (root + envelope
// rendering) so failed --json commands emit their envelope on stdout,
// exactly as in production; returns stdout and the process exit code.
func executeJSON(t *testing.T, rt Runtime, args ...string) (string, int) {
	t.Helper()
	app, e := New(context.Background(), func(r *Runtime) { *r = rt })
	if e != nil {
		t.Fatal(e)
	}
	out := new(bytes.Buffer)
	app.Root.SetOut(out)
	app.Root.SetArgs(args)
	code := app.Execute()
	return out.String(), code
}

func TestServiceCommandsRejectUninitializedState(t *testing.T) {
	// A fresh state home with no config.yaml: service mutations and
	// restarts must fail with not_configured and, under --json, emit the
	// machine-readable envelope (with remediation hint) instead of prose.
	home := t.TempDir()
	rt := defaultRuntime()
	rt.Services = &fakeSystemd{actual: service.Actual{Active: true, Enabled: true, Health: "healthy"}}
	rt.StateHome = home
	rt.Runner = &execx.FakeRunner{}
	for _, args := range [][]string{
		{"service", "nginx", "install"},
		{"service", "nginx", "restart"},
	} {
		out, code := executeJSON(t, rt, append(args, "--json")...)
		if code != int(errors.ExitCodePrecond) {
			t.Fatalf("%v: expected precond exit %d, got %d (out=%s)", args, errors.ExitCodePrecond, code, out)
		}
		env := envelopeFromJSON(t, out)
		if env.Ok || env.Error.Code != "not_configured" {
			t.Fatalf("envelope mismatch for %v: %+v", args, env)
		}
		if env.Error.Hint == "" {
			t.Fatalf("not_configured must keep its remediation hint: %s", out)
		}
	}
}

func TestRestartPreconditionsFailWithStableCodes(t *testing.T) {
	rt, sys, _ := serviceRuntime(t)

	// Not installed/desired-running: restart is refused up front.
	out, err := execute(t, rt, "service", "nginx", "restart")
	requireAppErr(t, err, "service_not_running")
	out, code := executeJSON(t, rt, "service", "nginx", "restart", "--json")
	if code != int(errors.ExitCodePrecond) {
		t.Fatalf("expected precond exit %d, got %d", errors.ExitCodePrecond, code)
	}
	if env := envelopeFromJSON(t, out); env.Error.Code != "service_not_running" {
		t.Fatalf("envelope code mismatch: %+v", env)
	}
	if sys.restarts != 0 {
		t.Fatalf("refused restart must not touch systemd, restarts=%d", sys.restarts)
	}

	// Desired running but systemd reports inactive: still refused, no restart.
	snap, e := state.NewStore(rt.StateHome).Load()
	if e != nil {
		t.Fatal(e)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if e = state.NewStore(rt.StateHome).WriteSnapshot(snap); e != nil {
		t.Fatal(e)
	}
	sys.actual = service.Actual{Active: false, Enabled: true, Health: "unknown"}
	_, err = execute(t, rt, "service", "nginx", "restart")
	requireAppErr(t, err, "service_not_active")
	out, code = executeJSON(t, rt, "service", "nginx", "restart", "--json")
	if code != int(errors.ExitCodePrecond) {
		t.Fatalf("expected precond exit %d, got %d", errors.ExitCodePrecond, code)
	}
	if env := envelopeFromJSON(t, out); env.Error.Code != "service_not_active" {
		t.Fatalf("envelope code mismatch: %+v", env)
	}
	if sys.restarts != 0 {
		t.Fatalf("inactive service must not be restarted, restarts=%d", sys.restarts)
	}
}

func TestServiceMutationFailureRollsBackAndReports(t *testing.T) {
	// A failing switch inside the real transaction manager must roll
	// back the desired state and surface a stable transaction failure in
	// the envelope — not a partial mutation.
	rt, sys, _ := serviceRuntime(t)
	order := []string{}
	reb := &switchFailRebuild{order: &order, failSwitch: true}
	rt.Transactions = &transaction.Manager{
		Root:      filepath.Join(rt.StateHome, ".nixcp"),
		Locker:    transaction.FlockLocker{Path: filepath.Join(rt.StateHome, ".nixcp", "lock")},
		Rebuilder: reb,
		// Health is required by the manager's validation; the switch fails
		// before health ever runs, and the rollback path must restore the
		// pre-mutation desired state.
		Health: desiredHealth{systemd: sys, name: service.Nginx, running: true},
		NewID:  func() string { return "tx" },
		Now:    func() time.Time { return time.Unix(1, 0) },
	}
	out, code := executeJSON(t, rt, "service", "nginx", "install", "--json")
	if code != int(errors.ExitCodeHealth) {
		t.Fatalf("expected health-class exit %d, got %d (out=%s)", errors.ExitCodeHealth, code, out)
	}
	if env := envelopeFromJSON(t, out); env.Error.Code != "service_transaction_failed" {
		t.Fatalf("envelope code mismatch: %+v", env)
	}
	if !reb.rolledBack {
		t.Fatal("failing switch must trigger generation rollback")
	}
	// Desired state in the store must be untouched by the failed mutation.
	snap, e := state.NewStore(rt.StateHome).Load()
	if e != nil {
		t.Fatal(e)
	}
	if snap.Config.Services.Nginx.Installed || snap.Config.Services.Nginx.DesiredState == "running" {
		t.Fatalf("failed install mutated desired state: %+v", snap.Config.Services.Nginx)
	}
}

// switchFailRebuild wraps the Manager's rebuild interface with an
// optional switch failure; it records whether rollback was requested.
type switchFailRebuild struct {
	order      *[]string
	failSwitch bool
	rolledBack bool
}

func (r *switchFailRebuild) CurrentGeneration(context.Context) (string, error) {
	*r.order = append(*r.order, "current")
	return "generation-old", nil
}
func (r *switchFailRebuild) Build(_ context.Context, p string) error {
	*r.order = append(*r.order, "build:"+filepath.Base(p))
	return nil
}
func (r *switchFailRebuild) Switch(context.Context) error {
	if r.failSwitch {
		*r.order = append(*r.order, "switch:failed")
		return errors.New("switch_failed", "nixos-rebuild switch failed", "See journalctl", errors.ExitCodeBuild)
	}
	*r.order = append(*r.order, "switch")
	return nil
}
func (r *switchFailRebuild) Rollback(context.Context, string) error {
	r.rolledBack = true
	*r.order = append(*r.order, "rollback")
	return nil
}
