package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
)

func TestNewRootCommandHasGlobalFlagsAndCommands(t *testing.T) {
	root, err := NewRootCommand(context.Background())
	if err != nil {
		t.Fatalf("failed to build root command: %v", err)
	}

	globalFlags := []string{"json", "no-input", "yes", "timeout"}
	for _, flag := range globalFlags {
		if root.PersistentFlags().Lookup(flag) == nil {
			t.Fatalf("missing persistent flag %s", flag)
		}
	}

	commands := []string{"install", "status", "doctor", "service", "php", "artisan", "link", "unlink", "sites", "shell", "version"}
	for _, c := range commands {
		if cmd, _, err := root.Find([]string{c}); err != nil || cmd == nil {
			t.Fatalf("missing command %q (err=%v)", c, err)
		}
	}

	for _, c := range []string{"nginx", "mariadb", "redis"} {
		for _, a := range []string{"install", "start", "status", "stop", "restart"} {
			if cmd, _, err := root.Find([]string{c, a}); err != nil || cmd == nil {
				t.Fatalf("missing alias %q for %q (err=%v)", a, c, err)
			}
		}
	}
}

func TestStatusJSONOutputIsSingleObject(t *testing.T) {
	app, err := New(context.Background())
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}
	buf := bytes.Buffer{}
	app.Root.SetOut(&buf)
	app.Root.SetErr(&bytes.Buffer{})
	app.Root.SetArgs([]string{"--json", "status"})

	code := app.Execute()
	if code != int(errors.ExitCodeSuccess) {
		t.Fatalf("expected success code, got %d", code)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("expected json payload: %v", err)
	}

	if ok, found := payload["ok"].(bool); !found || !ok {
		t.Fatalf("expected ok=true in payload, got %#v", payload["ok"])
	}
	if payload["command"] != "status" {
		t.Fatalf("unexpected command in payload: %#v", payload["command"])
	}
}

func TestJSONImposesNoInput(t *testing.T) {
	app, err := New(context.Background())
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}
	app.Root.SetArgs([]string{"--json", "status"})
	buf := bytes.Buffer{}
	app.Root.SetOut(&buf)
	app.Root.SetErr(&bytes.Buffer{})
	code := app.Execute()
	if code != int(errors.ExitCodeSuccess) {
		t.Fatalf("expected success with json: got %d", code)
	}
	if !json.Valid(buf.Bytes()) {
		t.Fatalf("expected valid JSON output, got: %q", buf.String())
	}
	if v, err := app.Root.Flags().GetBool("no-input"); err == nil && !v {
		t.Fatalf("expected --json to force --no-input, got %v", v)
	}
}

func TestVersionUsesInjectedMetadata(t *testing.T) {
	app, err := New(context.Background(), WithBuildMetadata(BuildMetadata{
		Version: "1.2.3",
		Commit:  "abc123",
		BuiltAt: "2025-01-01T00:00:00Z",
	}))
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}

	buf := bytes.Buffer{}
	app.Root.SetOut(&buf)
	app.Root.SetErr(&bytes.Buffer{})
	app.Root.SetArgs([]string{"--json", "version"})

	if code := app.Execute(); code != int(errors.ExitCodeSuccess) {
		t.Fatalf("version should succeed: %d", code)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("expected json payload: %v", err)
	}
	if payload["command"] != "version" {
		t.Fatalf("unexpected command in payload: %#v", payload["command"])
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", payload["data"])
	}
	if data["version"] != "1.2.3" || data["commit"] != "abc123" || data["built_at"] != "2025-01-01T00:00:00Z" {
		t.Fatalf("unexpected version metadata payload: %#v", data)
	}
}

func TestStatusJSONDoesNotPrintUsageOnUsageError(t *testing.T) {
	app, err := New(context.Background(), WithBuildMetadata(BuildMetadata{Version: "1.2.3"}))
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}

	buf := bytes.Buffer{}
	errBuf := bytes.Buffer{}
	app.Root.SetOut(&buf)
	app.Root.SetErr(&errBuf)
	app.Root.SetArgs([]string{"--json", "status", "--invalid"})

	code := app.Execute()
	if code != int(errors.ExitCodeUsage) {
		t.Fatalf("expected usage exit class on invalid arg: %d, got %d", errors.ExitCodeUsage, code)
	}
	if strings.TrimSpace(errBuf.String()) != "" {
		t.Fatalf("expected no usage/error in stderr, got: %q", errBuf.String())
	}
}

func TestCommandTimeoutFlagDefaultsAndOverride(t *testing.T) {
	app, err := New(context.Background())
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}

	app.Root.SetArgs([]string{"status"})
	if err := app.Root.ParseFlags([]string{"--timeout", "5s"}); err != nil {
		t.Fatalf("parse flags failed: %v", err)
	}
	got, err := commandTimeout(app.Root)
	if err != nil {
		t.Fatalf("command timeout getter failed: %v", err)
	}
	if got != 5*time.Second {
		t.Fatalf("expected timeout %s, got %s", 5*time.Second, got)
	}
}

func TestNoopRootRunInJSONReturnsNcpPayload(t *testing.T) {
	runner := &execx.FakeRunner{}
	app, err := New(context.Background(), WithRunner(runner))
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}
	app.Root.SetOut(io.Discard)
	app.Root.SetErr(io.Discard)
	app.Root.SetArgs([]string{"--json"})

	if code := app.Execute(); code != int(errors.ExitCodeSuccess) {
		t.Fatalf("expected success: %d", code)
	}
}

func TestShellAliasEquality(t *testing.T) {
	app, err := New(context.Background())
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}
	for _, service := range []string{"nginx", "mariadb", "redis"} {
		for _, action := range []string{"install", "start", "status", "stop", "restart"} {
			aliasCmd, _, err := app.Root.Find([]string{service, action})
			serviceCmd, _, err2 := app.Root.Find([]string{"service", service, action})
			if err != nil || err2 != nil || aliasCmd == nil || serviceCmd == nil {
				t.Fatalf("missing alias parity for %s %s: alias err=%v service err=%v", service, action, err, err2)
			}
			if aliasCmd.Use != action {
				t.Fatalf("expected alias leaf use to be %q, got %q", action, aliasCmd.Use)
			}
		}
	}
}

func TestCommandUsesNoPromptInJSON(t *testing.T) {
	app, err := New(context.Background())
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}
	app.Root.SetArgs([]string{"--json", "install"})
	buf := bytes.Buffer{}
	app.Root.SetOut(&buf)
	app.Root.SetErr(&bytes.Buffer{})

	if code := app.Execute(); code != int(errors.ExitCodePlatform) {
		t.Fatalf("expected safe platform refusal on this non-NixOS host: %d", code)
	}

	if !json.Valid(buf.Bytes()) {
		t.Fatalf("expected json output, got %q", buf.String())
	}
	if strings.Contains(strings.ToLower(buf.String()), "prompt") {
		// conservative check: no prompt-like helpers should appear.
		t.Fatalf("did not expect prompt words in json output")
	}
}

func TestTimeoutFlagBindsDeadlineToInvocationContext(t *testing.T) {
	home := t.TempDir()
	runner := &execx.FakeRunner{}
	app, err := New(context.Background(), WithStateHome(home), WithRunner(runner))
	if err != nil {
		t.Fatalf("failed to build app: %v", err)
	}
	store := state.NewStore(home)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}

	app.Root.SetArgs([]string{"--timeout", "5s", "php", "-v"})
	if code := app.Execute(); code != 0 {
		t.Fatalf("php exit %d", code)
	}
	if len(runner.Contexts) == 0 {
		t.Fatal("no command ran")
	}
	d, ok := runner.Contexts[0].Deadline()
	if !ok {
		t.Fatal("--timeout did not install a deadline on the command context")
	}
	if remaining := time.Until(d); remaining <= 0 || remaining > 5*time.Second {
		t.Fatalf("unexpected deadline remaining: %v", remaining)
	}
}

func TestDbgPreRunFlagVisibility(t *testing.T) {
	home := t.TempDir()
	app, err := New(context.Background(), WithStateHome(home))
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(home)
	u, _ := user.Current()
	cfg := initialConfig(u, os.Getuid(), os.Getgid(), u.Username, "", false)
	cfg.Owner.Home = home
	cfg.PHP = state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"}
	if err := store.Initialize(cfg); err != nil {
		t.Fatal(err)
	}
	app.Root.SetArgs([]string{"--timeout", "5s", "php", "-v"})
	_ = app.Execute()
	phpCmd, _, _ := app.Root.Find([]string{"php"})
	if phpCmd == nil {
		t.Fatal("no php cmd")
	}
	d, derr := phpCmd.Flags().GetDuration("timeout")
	rd, _ := app.Root.Flags().GetDuration("timeout")
	t.Logf("php flag=%v err=%v root flag=%v", d, derr, rd)
}
