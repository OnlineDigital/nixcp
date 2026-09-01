package command

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
)

func TestPHPFailureJSONEnvelopeCarriesProcessExitDiagnostics(t *testing.T) {
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 7, Stderr: "fatal: boom"},
			&execx.ProcessExitError{Cmd: []string{"php", "-v"}, ExitCode: 7, Stderr: "fatal: boom"}
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	// Passthrough strips NixCP-only flags before they reach the child.
	app.Root.SetArgs([]string{"php", "-v", "--json"})
	if code := app.Execute(); code != 7 {
		t.Fatalf("expected php's own exit code 7, got %d", code)
	}

	var env output.ErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("expected JSON error envelope, got %q: %v", buf.String(), err)
	}
	if env.Ok || env.Command != "ncp php" {
		t.Fatalf("unexpected envelope header: %#v", env)
	}
	if env.Error.Code != "php_execution_failed" || env.Error.Message != "fatal: boom" {
		t.Fatalf("unexpected error info: %#v", env.Error)
	}
	if len(env.Error.Diagnostics) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %#v", env.Error.Diagnostics)
	}
	d := env.Error.Diagnostics[0]
	if d.Type != "process-exit" || d.Exit == nil || *d.Exit != 7 {
		t.Fatalf("unexpected process-exit diagnostic: %#v", d)
	}
	if d.Stderr != "fatal: boom" {
		t.Fatalf("expected child stderr in diagnostic, got %q", d.Stderr)
	}
	if !strings.Contains(d.Command, "php") {
		t.Fatalf("expected child command in diagnostic, got %q", d.Command)
	}
	if d.Signal != "" {
		t.Fatalf("unexpected signal on plain exit-code failure: %q", d.Signal)
	}
}

func TestArtisanSignalFailureJSONEnvelopeCarriesSignalDiagnostic(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: "migrate killed"},
			&execx.ProcessExitError{Cmd: []string{"./artisan", "migrate"}, ExitCode: 1, Stderr: "migrate killed", Signal: "KILL"}
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"artisan", "migrate", "--json"})
	if code := app.Execute(); code != 1 {
		t.Fatalf("expected artisan's own exit code 1, got %d", code)
	}

	var env output.ErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("expected JSON error envelope, got %q: %v", buf.String(), err)
	}
	if len(env.Error.Diagnostics) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %#v", env.Error.Diagnostics)
	}
	d := env.Error.Diagnostics[0]
	if d.Type != "process-exit" || d.Signal != "KILL" || d.Exit == nil || *d.Exit != 1 {
		t.Fatalf("unexpected signal diagnostic: %#v", d)
	}
	if !strings.Contains(env.Error.Hint, "signal KILL") {
		t.Fatalf("expected signal hint, got %q", env.Error.Hint)
	}
}

func TestNonProcessJSONErrorEnvelopeStaysDiagnosticFree(t *testing.T) {
	// Unconfigured state home: `ncp php` fails with a plain precondition
	// error (no child ran), so the envelope must stay byte-identical to the
	// pre-diagnostics shape — no `diagnostics` key may appear at all.
	app, _, _ := phpTestAppNoInit(t)
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"php", "-v", "--json"})
	if code := app.Execute(); code != int(apperrors.ExitCodePrecond) {
		t.Fatalf("expected precond exit, got %d", code)
	}

	if strings.Contains(buf.String(), "diagnostics") {
		t.Fatalf("non-process error envelope must not carry diagnostics: %q", buf.String())
	}
	want := output.Error("ncp php", "not_configured", "NixCP is not initialized", "Run: ncp install", nil)
	var wantBuf bytes.Buffer
	if err := output.WriteJSON(&wantBuf, want); err != nil {
		t.Fatal(err)
	}
	if buf.String() != wantBuf.String() {
		t.Fatalf("error envelope drifted from the stable shape.\ngot:  %q\nwant: %q", buf.String(), wantBuf.String())
	}
}

func TestHumanErrorIsWrittenToStderr(t *testing.T) {
	// The regular CLI must be diagnosable too: without --json, errors belong
	// on stderr and must not disappear behind their process exit code.
	app, _, _ := phpTestAppNoInit(t)
	var stdout, stderr bytes.Buffer
	app.Root.SetOut(&stdout)
	app.Root.SetErr(&stderr)
	app.Root.SetArgs([]string{"service", "nginx", "install"})

	if code := app.Execute(); code != int(apperrors.ExitCodePrecond) {
		t.Fatalf("expected precondition exit code, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("human error must not write to stdout: %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "error [not_configured]") || !strings.Contains(got, "hint: Run: ncp install") {
		t.Fatalf("missing actionable human error on stderr: %q", got)
	}
}

func TestHumanErrorIncludesDetail(t *testing.T) {
	err := apperrors.New("systemd_error", "nginx failed", "Inspect the unit journal", apperrors.ExitCodeHealth)
	err.Details = "permission denied"
	var stderr bytes.Buffer
	writeHumanError(&stderr, err)
	if got := stderr.String(); got != "error [systemd_error]: nginx failed\nhint: Inspect the unit journal\ndetails: permission denied\n" {
		t.Fatalf("unexpected human error: %q", got)
	}
}
