package command

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSkillCommandListsEveryVisibleCommand checks the determinism contract:
// every visible command appears exactly once, sorted, with hidden plumbing
// excluded.
func TestSkillCommandListsEveryVisibleCommand(t *testing.T) {
	root, err := NewRootCommand(nil)
	if err != nil {
		t.Fatalf("failed to build root command: %v", err)
	}
	entries := collectCommandReference(root)
	if len(entries) < 20 {
		t.Fatalf("expected a broad reference, got %d entries", len(entries))
	}
	seen := map[string]bool{}
	for i, e := range entries {
		if e["path"] == "" {
			t.Fatalf("entry %d has empty path", i)
		}
		if seen[e["path"]] {
			t.Fatalf("duplicate path %q", e["path"])
		}
		seen[e["path"]] = true
		if i > 0 && entries[i-1]["path"] >= e["path"] {
			t.Fatalf("entries not sorted: %q before %q", entries[i-1]["path"], e["path"])
		}
	}
	for _, want := range []string{"ncp install", "ncp php install", "ncp service nginx install", "ncp link", "ncp unlink", "ncp tui", "ncp skill"} {
		if !seen[want] {
			t.Fatalf("missing reference entry for %q", want)
		}
	}
	for _, banned := range []string{"ncp completion", "ncp help", "ncp php session"} {
		if seen[banned] {
			t.Fatalf("hidden command leaked into reference: %q", banned)
		}
	}
}

// TestSkillTextOutputIsDeterministic asserts the text rendering is stable
// across two runs and mentions the global flags exactly once.
func TestSkillTextOutputIsDeterministic(t *testing.T) {
	build := func() string {
		root, err := NewRootCommand(nil)
		if err != nil {
			t.Fatalf("failed to build root command: %v", err)
		}
		var out strings.Builder
		root.SetOut(&out)
		root.SetErr(&strings.Builder{})
		root.SetArgs([]string{"skill"})
		if code := executeRoot(root); code != 0 {
			t.Fatalf("skill exited %d", code)
		}
		return out.String()
	}
	first := build()
	second := build()
	if first != second {
		t.Fatalf("skill output is not deterministic")
	}
	if !strings.Contains(first, "== global flags ==") {
		t.Fatalf("global flags section missing")
	}
	if strings.Count(first, "== ncp link") != 1 {
		t.Fatalf("link command must appear exactly once")
	}
	if strings.Contains(first, "NixCP CLI") {
		t.Fatalf("skill output must not include the root banner")
	}
}

// TestSkillJSONOutputParses checks the --json envelope shape.
func TestSkillJSONOutputParses(t *testing.T) {
	root, err := NewRootCommand(nil)
	if err != nil {
		t.Fatalf("failed to build root command: %v", err)
	}
	root.SetArgs([]string{"--json", "skill"})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&strings.Builder{})
	code := executeRoot(root)
	if code != 0 {
		t.Fatalf("skill --json exited %d", code)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Commands []struct {
				Path string `json:"path"`
				Use  string `json:"use"`
			} `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
		t.Fatalf("invalid JSON envelope: %v", err)
	}
	if !env.OK || len(env.Data.Commands) == 0 {
		t.Fatalf("unexpected skill envelope")
	}
}
