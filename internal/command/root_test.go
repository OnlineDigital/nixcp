package command

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
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
	root, err := NewRootCommand(context.Background())
	if err != nil {
		t.Fatalf("failed to build root command: %v", err)
	}

	buf := bytes.Buffer{}
	root.SetOut(&buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--json", "status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("expected status success, got error: %v", err)
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
	// ensure command runs and emits JSON when no-input isn't explicitly provided
	root, err := NewRootCommand(context.Background())
	if err != nil {
		t.Fatalf("failed to build root command: %v", err)
	}
	root.SetArgs([]string{"--json", "status"})
	buf := bytes.Buffer{}
	root.SetOut(&buf)
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatalf("expected success with json: %v", err)
	}
	if !json.Valid(buf.Bytes()) {
		t.Fatalf("expected valid JSON output, got: %q", buf.String())
	}
}
