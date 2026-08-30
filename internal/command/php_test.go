package command

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
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
	s, err := shellSnippet("bash")
	if err != nil || !bytes.Contains([]byte(s), []byte("$1\" = php")) {
		t.Fatalf("bad snippet %q %v", s, err)
	}
}
