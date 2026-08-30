package nix

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
)

// Candidate is an isolated evaluation input. Its wrapper imports the staged
// module by its exact path and never refers to ~/.nixcp/generated; callers
// therefore cannot accidentally test the currently imported module.
type Candidate struct {
	ModulePath  string
	WrapperPath string
	Eval        *execx.Command
	Build       *execx.Command
}

// PrepareCandidate writes a self-contained wrapper next to a staged module and
// returns argv-only commands. The wrapper is intentionally module-only: a full
// host rebuild must be performed transactionally in Stage 5 after publication
// or with a host-specific overlay. It is nevertheless a real evaluation of the
// proposed module, rather than an evaluation of the stable import.
func PrepareCandidate(dir string, module []byte, rebuild state.RebuildConfig) (Candidate, error) {
	if rebuild.Mode != "traditional" && rebuild.Mode != "flake" {
		return Candidate{}, fmt.Errorf("unsupported rebuild mode %q", rebuild.Mode)
	}
	modulePath := filepath.Join(dir, "candidate-module.nix")
	wrapperPath := filepath.Join(dir, "candidate-wrapper.nix")
	wrapper := []byte("{ lib, pkgs, ... }: { imports = [ " + nixString(modulePath) + " ]; }\n")
	candidate := Candidate{
		ModulePath: modulePath, WrapperPath: wrapperPath,
		Eval:  &execx.Command{Name: "nix-instantiate", Args: []string{"--eval", "--strict", wrapperPath}},
		Build: &execx.Command{Name: "nixos-rebuild", Args: []string{"build", "-I", "nixos-config=" + wrapperPath}},
	}
	if rebuild.Mode == "flake" {
		// A flake target requires a host-owned wrapper/overlay to be faithful.
		// Do not silently run the configured target, because that would test its
		// existing absolute import rather than this candidate.
		candidate.Build = nil
	}
	return candidate, writeCandidateFiles(modulePath, module, wrapperPath, wrapper)
}

func writeCandidateFiles(modulePath string, module []byte, wrapperPath string, wrapper []byte) error {
	// Files are written by the caller's private staging directory. Atomic
	// publication is handled by the state/transaction layer; candidate files
	// are deliberately disposable.
	if err := os.WriteFile(modulePath, module, 0600); err != nil {
		return err
	}
	return os.WriteFile(wrapperPath, wrapper, 0600)
}
