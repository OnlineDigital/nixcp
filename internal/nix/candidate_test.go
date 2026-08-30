package nix

import (
	"os"
	"strings"
	"testing"

	"github.com/nixcp/nixcp/internal/state"
)

func TestPrepareCandidateTraditionalUsesStagedModule(t *testing.T) {
	dir := t.TempDir()
	candidate, err := PrepareCandidate(dir, []byte("{ ... }: {}\n"), state.RebuildConfig{Mode: "traditional"})
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := os.ReadFile(candidate.WrapperPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrapper), candidate.ModulePath) || strings.Contains(string(wrapper), ".nixcp/generated") {
		t.Fatalf("wrapper does not isolate candidate: %s", wrapper)
	}
	if candidate.Build == nil || !strings.Contains(strings.Join(candidate.Build.Args, " "), candidate.WrapperPath) {
		t.Fatalf("build must target wrapper: %#v", candidate.Build)
	}
}
func TestPrepareCandidateFlakeDoesNotBuildOldImport(t *testing.T) {
	candidate, err := PrepareCandidate(t.TempDir(), []byte("{}"), state.RebuildConfig{Mode: "flake", Target: ".#host", Impure: true})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Build != nil {
		t.Fatal("flake candidate must not silently build configured target")
	}
}
