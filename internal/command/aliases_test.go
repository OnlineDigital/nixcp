package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
)

// artisanAliasProject creates a temp dir containing a readable ./artisan and
// makes it the working directory, returning a restore callback.
func artisanAliasProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAliasAForwardsArgvToArtisan(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	// Flags and args must reach artisan verbatim, including ones that look
	// like options of the target tool (`--force`, `--seed`).
	app.Root.SetArgs([]string{"a", "migrate:fresh", "--seed", "--force"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 {
		t.Fatalf("runs = %d", len(runner.Runs))
	}
	want := []string{"./artisan", "migrate:fresh", "--seed", "--force"}
	if strings.Join(runner.Runs[0].Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv changed:\n got %#v\nwant %#v", runner.Runs[0].Args, want)
	}
}

func TestAliasAMRunsMigrateWithAppendedFlags(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"am", "--step=2"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 {
		t.Fatalf("runs = %d", len(runner.Runs))
	}
	want := []string{"./artisan", "migrate", "--step=2"}
	if strings.Join(runner.Runs[0].Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv changed:\n got %#v\nwant %#v", runner.Runs[0].Args, want)
	}
}

func TestAliasAMAloneRunsPlainMigrate(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"am"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 || strings.Join(runner.Runs[0].Args, " ") != "./artisan migrate" {
		t.Fatalf("unexpected argv: %#v", runner.Runs)
	}
}

func TestAliasTinkerReusesArtisanTTYPassthrough(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"tinker"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) == 0 {
		t.Fatal("no run recorded")
	}
	run := runner.Runs[0]
	if !run.Interactive {
		t.Fatal("ncp tinker must run with Interactive (TTY passthrough)")
	}
	if strings.Join(run.Args, " ") != "./artisan tinker" {
		t.Fatalf("unexpected argv: %#v", run.Args)
	}
}

func TestAliasTinkerForwardsAppendedArgs(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"tinker", "vendor/package"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := []string{"./artisan", "tinker", "vendor/package"}
	if strings.Join(runner.Runs[0].Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv changed:\n got %#v\nwant %#v", runner.Runs[0].Args, want)
	}
}

func TestAliasCIRunsComposerInstallAndForwardsFlags(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"ci", "--prefer-dist", "--no-interaction"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 {
		t.Fatalf("runs = %d", len(runner.Runs))
	}
	want := append([]string{composerScript, "install"}, "--prefer-dist", "--no-interaction")
	if strings.Join(runner.Runs[0].Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv changed:\n got %#v\nwant %#v", runner.Runs[0].Args, want)
	}
}

func TestAliasCIStripsNixcpGlobalFlags(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	// NixCP's own flags are consumed by the pre-run; Composer only sees its
	// own argv (install + whatever the user appended).
	app.Root.SetArgs([]string{"ci", "--json", "--timeout", "45s", "--no-dev"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 {
		t.Fatalf("runs = %d", len(runner.Runs))
	}
	for _, a := range runner.Runs[0].Args {
		if a == "--json" || a == "--timeout" || a == "45s" {
			t.Fatalf("NixCP-only flag %q leaked to child argv: %#v", a, runner.Runs[0].Args)
		}
	}
	if strings.Join(runner.Runs[0].Args, " ") != composerScript+" install --no-dev" {
		t.Fatalf("unexpected argv: %#v", runner.Runs[0].Args)
	}
}

func TestAliasAStripsNixcpGlobalFlagsBeforeArtisan(t *testing.T) {
	artisanAliasProject(t)
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"a", "migrate", "--json", "--timeout=60s", "--force"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) != 1 {
		t.Fatalf("runs = %d", len(runner.Runs))
	}
	want := []string{"./artisan", "migrate", "--force"}
	if strings.Join(runner.Runs[0].Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv changed:\n got %#v\nwant %#v", runner.Runs[0].Args, want)
	}
}

func TestAliasesPropagateChildExitCode(t *testing.T) {
	artisanAliasProject(t)
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 5, Stderr: "migrate failed"}, nil
	}}
	app, err := New(nil, WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"am"})
	if code := app.Execute(); code != 5 {
		t.Fatalf("expected artisan's own exit code 5, got %d", code)
	}
}

func TestComposerRunAndPintAliasesForwardArgs(t *testing.T) {
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{}
	app, err := New(nil, WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"c", "dev", "--watch"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("composer alias exit %d", code)
	}
	if len(runner.Runs) != 1 || !strings.HasSuffix(strings.Join(runner.Runs[0].Args, " "), " run dev --watch") {
		t.Fatalf("composer argv: %#v", runner.Runs)
	}
	runner.Runs = nil
	app.Root.SetArgs([]string{"pint", "--parallel"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("pint alias exit %d", code)
	}
	if len(runner.Runs) != 1 || strings.Join(runner.Runs[0].Args, " ") != "./vendor/bin/pint --parallel" {
		t.Fatalf("pint argv: %#v", runner.Runs)
	}
}
