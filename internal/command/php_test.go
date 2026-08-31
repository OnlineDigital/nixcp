package command

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	shellpkg "github.com/nixcp/nixcp/internal/shell"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/php"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
)

func phpTestApp(t *testing.T) (*ApplicationRoot, *execx.FakeRunner, string) {
	t.Helper()
	home := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	runner := &execx.FakeRunner{}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
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
	return app, runner, home
}
func TestPHPRunsResolvedExactBinaryWithArgv(t *testing.T) {
	app, r, _ := phpTestApp(t)
	d := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(d)
	app.Root.SetArgs([]string{"php", "-v", "x y"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("code %d", code)
	}
	if len(r.Runs) != 1 || r.Runs[0].Name != "/etc/nixcp/php/8.3/bin/php" || r.Runs[0].Args[1] != "x y" || r.Runs[0].Dir != d {
		t.Fatalf("bad command %#v", r.Runs)
	}
}
func TestPHPUseWritesLocalMarkerAtomically(t *testing.T) {
	app, _, _ := phpTestApp(t)
	d := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(d)
	app.Root.SetArgs([]string{"php", "use", "8.3"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("code %d", code)
	}
	b, err := os.ReadFile(filepath.Join(d, ".php-version"))
	if err != nil || string(b) != "8.3\n" {
		t.Fatalf("marker %q %v", b, err)
	}
}

func TestPHPUseAcceptsStickyWorldWritableAncestor(t *testing.T) {
	app, _, _ := phpTestApp(t)
	d := t.TempDir() // CI temp roots are commonly beneath sticky /tmp.
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(d); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"php", "use", "8.3"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("sticky ancestor should be accepted, exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(d, ".php-version")); err != nil {
		t.Fatal(err)
	}
}

func TestPHPUseRejectsNonStickyWorldWritableAncestor(t *testing.T) {
	app, _, _ := phpTestApp(t)
	unsafe := filepath.Join(t.TempDir(), "unsafe")
	project := filepath.Join(unsafe, "project")
	if err := os.Mkdir(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"php", "use", "8.3"})
	if code := app.Execute(); code == 0 {
		t.Fatal("non-sticky world-writable ancestor must be refused")
	}
	if _, err := os.Stat(filepath.Join(project, ".php-version")); !os.IsNotExist(err) {
		t.Fatalf("marker written despite refusal: %v", err)
	}
}
func TestArtisanRejectsSymlink(t *testing.T) {
	app, _, _ := phpTestApp(t)
	d := t.TempDir()
	target := filepath.Join(d, "target")
	os.WriteFile(target, []byte("x"), 0600)
	os.Symlink(target, filepath.Join(d, "artisan"))
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(d)
	app.Root.SetArgs([]string{"artisan"})
	if code := app.Execute(); code == 0 {
		t.Fatal("expected symlink refusal")
	}
}
func TestShellSnippetOnlyWrapsPHPUse(t *testing.T) {
	s, err := shellpkg.Snippet("bash")
	if err != nil || !bytes.Contains([]byte(s), []byte("$1\" = php")) {
		t.Fatalf("bad snippet %q %v", s, err)
	}
}

func TestPHPPassthroughPropagatesChildExitCode(t *testing.T) {
	_, _, home := phpTestApp(t)
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 7, Stderr: "fatal: boom"},
			&execx.ProcessExitError{Cmd: []string{"php", "-v"}, ExitCode: 7, Stderr: "fatal: boom"}
	}}
	// swap the runner by rebuilding the app with the failing runner
	app2, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app2.Root.SetOut(&buf)
	app2.Root.SetArgs([]string{"php", "-v"})
	code := app2.Execute()
	if code != 7 {
		t.Fatalf("expected php's own exit code 7, got %d", code)
	}
}

func TestPHPInteractiveReplGetsTTY(t *testing.T) {
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"php", "-a"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) == 0 || !runner.Runs[0].Interactive {
		t.Fatal("php -a must run with Interactive (TTY passthrough)")
	}
}

func TestArtisanTinkerGetsTTY(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	app, runner, _ := phpTestApp(t)
	app.Root.SetArgs([]string{"artisan", "tinker"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(runner.Runs) == 0 {
		t.Fatal("no run recorded")
	}
	run := runner.Runs[0]
	if !run.Interactive {
		t.Fatal("artisan tinker must run with Interactive (TTY passthrough)")
	}
	if run.Args[0] != "./artisan" || run.Args[1] != "tinker" {
		t.Fatalf("unexpected argv: %v", run.Args)
	}
}

func TestArtisanFailurePropagatesExitCode(t *testing.T) {
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
		return execx.Result{ExitCode: 5, Stderr: "migrate failed"}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"artisan", "migrate"})
	if code := app.Execute(); code != 5 {
		t.Fatalf("expected artisan's own exit code 5, got %d", code)
	}
}

func TestArtisanPreconditions(t *testing.T) {
	// fresh home: not configured
	app, _, _ := phpTestAppNoInit(t)
	app.Root.SetArgs([]string{"artisan", "migrate"})
	if code := app.Execute(); code != int(apperrors.ExitCodePrecond) {
		t.Fatalf("expected precond exit, got %d", code)
	}
}

func phpTestAppNoInit(t *testing.T) (*ApplicationRoot, *execx.FakeRunner, string) {
	t.Helper()
	home := t.TempDir()
	runner := &execx.FakeRunner{}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	return app, runner, home
}

func TestShellEmitPrintsOnlyShellCode(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	app, _, _ := phpTestApp(t)
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"php", "use", "8.3", "--shell-emit=bash"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	code := buf.String()
	want, wantErr := shellpkg.Activation("bash", "8.3")
	if wantErr != nil {
		t.Fatal(wantErr)
	}
	if code != want {
		t.Fatalf("shell-emit stdout must be exactly the activation code.\ngot: %q\nwant: %q", code, want)
	}
	if strings.Contains(code, `"command"`) && strings.Contains(code, `"ncp"`) {
		t.Fatal("JSON envelope leaked into shell-emit output")
	}
	// The wrapper contract requires the marker write too.
	b, err := os.ReadFile(filepath.Join(dir, ".php-version"))
	if err != nil || string(b) != "8.3\n" {
		t.Fatalf("marker not written: %v %q", err, string(b))
	}
}

func TestShellEmitRejectsUninstalledVersion(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	app, _, _ := phpTestApp(t)
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"php", "use", "8.4", "--shell-emit=bash"})
	if code := app.Execute(); code == 0 {
		t.Fatal("uninstalled version must fail")
	}
	if buf.Len() != 0 {
		t.Fatalf("no stdout allowed on failure, got %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".php-version")); !os.IsNotExist(err) {
		t.Fatal("marker must not be written for invalid activation")
	}
}

func TestPHPEnvDeduplicatesNixcpVars(t *testing.T) {
	base := []string{"PATH=/usr/bin", "NIXCP_PHP_VERSION=8.0", "NIXCP_PHP_BIN=/old"}
	got := phpEnv(phpEnv(base, "8.3"), "8.4")
	counts := map[string]int{}
	for _, kv := range got {
		if strings.HasPrefix(kv, "NIXCP_PHP_VERSION=") || strings.HasPrefix(kv, "NIXCP_PHP_BIN=") {
			counts[strings.SplitN(kv, "=", 2)[0]]++
		}
	}
	if counts["NIXCP_PHP_VERSION"] != 1 || counts["NIXCP_PHP_BIN"] != 1 {
		t.Fatalf("expected exactly one of each NIXCP var, got %v (env: %v)", counts, got)
	}
	if got[len(got)-1] != "NIXCP_PHP_BIN="+filepath.Dir(php.Binary("8.4")) {
		t.Fatalf("stale NIXCP_PHP_BIN kept: %v", got)
	}
}

func TestPHPGlobalFlagsDoNotLeakToChildArgv(t *testing.T) {
	app, r, _ := phpTestApp(t)
	d := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(d)
	// NixCP-only flags placed after `php` must be consumed by NixCP and
	// stripped from the argv the interpreter receives.
	app.Root.SetArgs([]string{"php", "-v", "--json", "--timeout", "30s"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("code %d", code)
	}
	if len(r.Runs) != 1 {
		t.Fatalf("expected exactly one child run, got %d", len(r.Runs))
	}
	for _, a := range r.Runs[0].Args {
		if a == "--json" || a == "--timeout" || a == "30s" {
			t.Fatalf("NixCP-only flag %q leaked to child argv: %#v", a, r.Runs[0].Args)
		}
	}
}

func TestPHPTimeoutValueNotForwardedAsStrayArg(t *testing.T) {
	app, r, _ := phpTestApp(t)
	d := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(d)
	app.Root.SetArgs([]string{"php", "-r", "echo 1;", "--timeout=45s"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("code %d", code)
	}
	if len(r.Runs) != 1 {
		t.Fatalf("expected exactly one child run, got %d", len(r.Runs))
	}
	for _, a := range r.Runs[0].Args {
		if strings.Contains(a, "--timeout") || a == "45s" {
			t.Fatalf("timeout token/value leaked to child argv: %#v", r.Runs[0].Args)
		}
	}
}

func TestPHPSessionEmitsGlobalDefaultActivation(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	app, _, _ := phpTestApp(t)
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"php", "session", "--shell-emit=bash"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want, err := shellpkg.Activation("bash", "8.3")
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != want {
		t.Fatalf("session must emit the global default activation.\ngot: %q\nwant: %q", buf.String(), want)
	}
}

func TestPHPSessionPrefersActiveEnvVersion(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	app, _, _ := phpTestApp(t)
	t.Setenv("NIXCP_PHP_VERSION", "8.3")
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"php", "session", "--shell-emit=bash"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want, _ := shellpkg.Activation("bash", "8.3")
	if buf.String() != want {
		t.Fatalf("session must prefer the active env version, got %q", buf.String())
	}
}

func TestPHPSessionSilentWithoutDefault(t *testing.T) {
	home := t.TempDir()
	u, _ := user.Current()
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{}))
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}} // no GlobalDefault set
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	app.Root.SetOut(&buf)
	app.Root.SetArgs([]string{"php", "session", "--shell-emit=bash"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if buf.Len() != 0 {
		t.Fatalf("session must be silent when no default is configured, got %q", buf.String())
	}
}

func TestExtensionInstallFailsWithoutInstalledPHP(t *testing.T) {
	home := t.TempDir()
	u, _ := user.Current()
	app, err := New(context.Background(), WithStateHome(home), WithRunner(&execx.FakeRunner{}))
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.PHP = state.PHPConfig{Installed: []string{}} // none installed yet
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"php", "ext", "install", "redis"})
	if code := app.Execute(); code == 0 {
		t.Fatal("ext install must fail when no PHP version is installed")
	}
}

func TestExtensionInstallUnavailableForInstalledVersions(t *testing.T) {
	home := t.TempDir()
	u, _ := user.Current()
	store := state.NewStore(home)
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	rt := defaultRuntime()
	rt.Runner = &execx.FakeRunner{}
	rt.StateHome = home
	rt.Transactions = &transaction.Manager{Root: filepath.Join(home, ".nixcp"), Locker: transaction.FlockLocker{Path: filepath.Join(home, ".nixcp", "lock")}, Rebuilder: testRebuilder{}, Health: phpHealth{}}
	root, err := NewRootCommand(context.Background(), func(r *Runtime) { *r = rt })
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"php", "ext", "install", "redis"})
	if err := root.Execute(); err != nil {
		t.Fatalf("expected redis PHP extension to be available for installed 8.3, got error: %v", err)
	}
}
