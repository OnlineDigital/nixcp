package command

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
)

func TestComposerPassesRawArgvAndResolvedPHP(t *testing.T) {
	app, runner, _ := phpTestApp(t)
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	args := []string{"composer", "require", "vendor/package", "--opt=has space", "; touch should-not-run", "$(not-a-shell)"}
	app.Root.SetArgs(args)
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 {
		t.Fatalf("runs = %d", len(runner.Runs))
	}
	run := runner.Runs[0]
	if run.Name != "/etc/nixcp/php/8.3/bin/php" || run.Dir != dir {
		t.Fatalf("unexpected command: %#v", run)
	}
	if !run.Interactive {
		t.Fatal("human Composer runs must attach the caller terminal for live output")
	}
	want := append([]string{composerScript}, args[1:]...)
	if strings.Join(run.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv changed:\n got %#v\nwant %#v", run.Args, want)
	}
	if !envContains(run.Env, "NIXCP_PHP_VERSION=8.3") || !envContains(run.Env, "NIXCP_PHP_BIN=/etc/nixcp/php/8.3/bin") || !envContains(run.Env, "PATH=/etc/nixcp/php/8.3/bin") {
		t.Fatalf("Composer environment did not select resolved PHP: %#v", run.Env)
	}
}

func TestComposerStripsGlobalFlagsAndUsesTimeoutContext(t *testing.T) {
	app, runner, _ := phpTestApp(t)
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"composer", "install", "--json", "--no-input", "--yes", "--timeout", "45s"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 || strings.Join(runner.Runs[0].Args, " ") != composerScript+" install" {
		t.Fatalf("global flags leaked into Composer argv: %#v", runner.Runs)
	}
	if len(runner.Contexts) != 1 {
		t.Fatalf("expected a Composer execution context, got %d", len(runner.Contexts))
	}
	deadline, ok := runner.Contexts[0].Deadline()
	if !ok || time.Until(deadline) > 45*time.Second || time.Until(deadline) < 40*time.Second {
		t.Fatalf("--timeout was not applied to Composer context: deadline=%v ok=%v", deadline, ok)
	}
}

func TestComposerForwardsOutputAndChildExitCode(t *testing.T) {
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 17, Stdout: "partial output\n", Stderr: "composer failed\n"}, &execx.ProcessExitError{ExitCode: 17, Stderr: "composer failed"}
	}}
	app, err := New(nil, WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app.Root.SetOut(&stdout)
	app.Root.SetErr(&stderr)
	app.Root.SetArgs([]string{"composer", "install"})
	if code := app.Execute(); code != 17 {
		t.Fatalf("expected child exit 17, got %d", code)
	}
	if stdout.String() != "partial output\n" || !strings.Contains(stderr.String(), "composer failed\n") || !strings.Contains(stderr.String(), "composer_execution_failed") {
		t.Fatalf("unexpected output forwarding: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestComposerForwardsSuccessfulStdoutAndStderr(t *testing.T) {
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: "installed\n", Stderr: "notice\n"}, nil
	}}
	app, err := New(nil, WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app.Root.SetOut(&stdout)
	app.Root.SetErr(&stderr)
	app.Root.SetArgs([]string{"composer", "install"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if stdout.String() != "installed\n" || stderr.String() != "notice\n" {
		t.Fatalf("output was not propagated: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestComposerJSONIsSingleEnvelopeWithProcessDiagnostics(t *testing.T) {
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 12, Stdout: "not raw JSON\n", Stderr: "dependency failed"}, &execx.ProcessExitError{ExitCode: 12, Stderr: "dependency failed"}
	}}
	app, err := New(nil, WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"composer", "install", "--json"})
	if code := app.Execute(); code != 12 {
		t.Fatalf("expected child exit 12, got %d", code)
	}
	var got output.ErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output %q: %v", buf.String(), err)
	}
	if got.Command != "ncp composer" || got.Error.Code != "composer_execution_failed" || len(got.Error.Diagnostics) != 1 || got.Error.Diagnostics[0].Exit == nil || *got.Error.Diagnostics[0].Exit != 12 {
		t.Fatalf("unexpected Composer JSON envelope: %#v", got)
	}
	if strings.Contains(buf.String(), "not raw JSON") {
		t.Fatalf("child stdout must not break JSON envelope: %q", buf.String())
	}
	if len(runner.Runs) != 1 || runner.Runs[0].Interactive {
		t.Fatal("JSON Composer runs must capture output instead of attaching the terminal")
	}
}

func envContains(env []string, prefix string) bool {
	for _, value := range env {
		if value == prefix || strings.HasPrefix(value, prefix+":") {
			return true
		}
	}
	return false
}

func TestComposerEnvReplacesStalePHPSelection(t *testing.T) {
	env := composerEnv([]string{"PATH=/usr/bin", "NIXCP_PHP_VERSION=8.0", "NIXCP_PHP_BIN=/old"}, "8.3")
	if !envContains(env, "NIXCP_PHP_VERSION=8.3") || !envContains(env, "NIXCP_PHP_BIN=/etc/nixcp/php/8.3/bin") || !envContains(env, "PATH=/etc/nixcp/php/8.3/bin:/usr/bin") {
		t.Fatalf("unexpected Composer environment: %#v", env)
	}
	for _, value := range env {
		if value == "NIXCP_PHP_VERSION=8.0" || value == "NIXCP_PHP_BIN=/old" {
			t.Fatalf("stale PHP selection survived: %#v", env)
		}
	}
}
