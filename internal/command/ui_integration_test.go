package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/spf13/cobra"
)

// The interactive UX contract: prompts only on a real TTY, never under
// --json/--no-input, confirmations skipped under --yes. Tests run with a
// non-TTY stdin, so every scenario here pins the non-interactive path —
// which must stay byte-stable for scripts.

// linkedTestApp builds an initialized app with one linked site, following
// the harness style of site_test.go.
func linkedTestApp(t *testing.T, args ...string) (*ApplicationRoot, string) {
	t.Helper()
	home, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) {
		rt.Services = testSystemd{}
		rt.Transactions = testTransaction(home)
	})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	if err := store.Initialize(testSiteConfig(home)); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	snap.Sites = append(snap.Sites, state.SiteConfig{SchemaVersion: 2, ID: "app-example-com", Domain: "app.example.com", ProjectPath: project, DocumentRoot: filepath.Join(project, "public"), PHP: snap.Config.PHP.GlobalDefault, Nginx: state.NginxConfig{Handler: state.HandlerConfig{Type: "generic"}}})
	if err := store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs(args)
	return app, home
}

func TestUnlinkWithoutTTYProceedsWithoutPrompt(t *testing.T) {
	app, _ := linkedTestApp(t, "unlink", "app.example.com")
	if err := app.Root.Execute(); err != nil {
		t.Fatalf("expected non-interactive unlink to proceed, got %v", err)
	}
}

func TestUnlinkYesFlagSkipsPrompt(t *testing.T) {
	app, _ := linkedTestApp(t, "--yes", "unlink", "app.example.com")
	if err := app.Root.Execute(); err != nil {
		t.Fatalf("expected --yes unlink to proceed, got %v", err)
	}
}

func TestUnlinkJSONEnvelopeStableWithoutDiagnostics(t *testing.T) {
	app, _ := linkedTestApp(t, "--json", "unlink", "app.example.com")
	if err := app.Root.Execute(); err != nil {
		t.Fatalf("expected --json unlink to proceed, got %v", err)
	}
}

func TestLinkGenericDefaultByteStableWithoutTTY(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 0}, nil
	}}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner), withPlatform(acceptingPlatform{}), func(rt *Runtime) {
		rt.Services = testSystemd{}
		rt.Transactions = testTransaction(home)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.NewStore(home).Initialize(testSiteConfig(home)); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	snap, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snap.Config.Services.Nginx = state.ServiceConfig{Installed: true, DesiredState: "running"}
	if err := store.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--json", "link", "plain.example.com", "--path", project})
	if err := app.Root.Execute(); err != nil {
		t.Fatalf("expected non-interactive link with generic default, got %v", err)
	}
	reload, err := state.NewStore(home).Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reload.Sites {
		if s.Domain == "plain.example.com" && s.Nginx.Handler.Type != "generic" {
			t.Fatalf("expected generic handler without TTY, got %q", s.Nginx.Handler.Type)
		}
	}
}

func TestCommandUIModeMapsFlags(t *testing.T) {
	// Set argv so cobra parses the persistent flags into the executed
	// subcommand, then read the mode off that subcommand.
	app, _ := linkedTestApp(t, "--json", "--no-input", "--yes", "unlink", "app.example.com")
	if err := app.Root.Execute(); err != nil {
		t.Fatal(err)
	}
	unlink := findSubcommand(app.Root, "unlink")
	mode := commandUIMode(unlink)
	if !mode.JSON || !mode.NoInput || !mode.Yes {
		t.Fatalf("expected all three flags mapped, got %+v", mode)
	}
	if mode.Interactive() || mode.Confirmable() {
		t.Fatalf("json/no-input/yes must disable all prompting, got %+v", mode)
	}
	// A bare command maps to an all-false mode.
	app2, _ := linkedTestApp(t, "unlink", "app.example.com")
	if err := app2.Root.Execute(); err != nil {
		t.Fatal(err)
	}
	mode = commandUIMode(findSubcommand(app2.Root, "unlink"))
	if mode.JSON || mode.NoInput || mode.Yes {
		t.Fatalf("expected cleared flags mapped, got %+v", mode)
	}
}

func findSubcommand(root *cobra.Command, prefix string) *cobra.Command {
	for _, c := range root.Commands() {
		if strings.HasPrefix(c.Use, prefix) {
			return c
		}
	}
	return nil
}
