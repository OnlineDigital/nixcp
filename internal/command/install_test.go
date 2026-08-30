package command

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
)

type acceptingPlatform struct{}

func (acceptingPlatform) Check() error { return nil }
func withPlatform(p interface{ Check() error }) RuntimeOption {
	return func(r *Runtime) { r.Platform = p }
}

func TestInstallAdmissionFailureDoesNotCreateState(t *testing.T) {
	bad := &failingPlatform{}
	app, _ := New(context.Background(), withPlatform(bad))
	app.Root.SetArgs([]string{"--json", "install"})
	out := bytes.Buffer{}
	app.Root.SetOut(&out)
	app.Root.SetErr(&bytes.Buffer{})
	if got := app.Execute(); got != 5 {
		t.Fatalf("exit=%d", got)
	}
	var v map[string]any
	if json.Unmarshal(out.Bytes(), &v) != nil || v["ok"] != false {
		t.Fatalf("bad JSON: %s", out.String())
	}
}

type failingPlatform struct{}

func (*failingPlatform) Check() error { return os.ErrPermission }

func TestInstallPayloadDocumentsTraditionalAndFlake(t *testing.T) {
	traditional := installPayload("/home/a/.nixcp", "/home/a/.nixcp/generated/nixcp-module.nix", stateRebuild("traditional", "", false))
	if !bytes.Contains([]byte(traditional["importInstruction"].(string)), []byte("configuration.nix")) {
		t.Fatal("traditional handoff missing")
	}
	flake := installPayload("/home/a/.nixcp", "/home/a/.nixcp/generated/nixcp-module.nix", stateRebuild("flake", ".#host", true))
	if !bytes.Contains([]byte(flake["importInstruction"].(string)), []byte("--impure")) {
		t.Fatal("flake purity handoff missing")
	}
}
func stateRebuild(mode, target string, impure bool) state.RebuildConfig {
	return state.RebuildConfig{Mode: mode, Target: target, Impure: impure}
}

func TestConfirmImportUsesArgvAndNeverSwitches(t *testing.T) {
	runner := &execx.FakeRunner{}
	cmd := newInstallCommand(Runtime{})
	cmd.SetContext(context.Background())
	if err := confirmImport(cmd, runner, stateRebuild("flake", ".#h", true)); err != nil {
		t.Fatal(err)
	}
	if len(runner.Runs) != 1 || runner.Runs[0].Name != "nixos-rebuild" || runner.Runs[0].Args[0] != "build" {
		t.Fatalf("unexpected commands: %#v", runner.Runs)
	}
}

func TestStaticArtifactsArePrivate(t *testing.T) {
	u, _ := user.Current()
	store := state.NewStore(t.TempDir())
	_ = u
	if err := os.MkdirAll(store.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := installStaticArtifacts(store); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(store.Root, "shell", "bash.sh"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private shell artifact: %v %#o", err, info.Mode().Perm())
	}
}
