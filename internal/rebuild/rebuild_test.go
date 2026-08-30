package rebuild

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
)

func TestNixOSUsesRestrictedArgv(t *testing.T) {
	fake := &execx.FakeRunner{Handle: func(c *execx.Command) (execx.Result, error) {
		if c.Name == "readlink" {
			return execx.Result{Stdout: "/nix/store/generation\n"}, nil
		}
		return execx.Result{}, nil
	}}
	r := NixOS{Runner: fake, SwitchArgs: []string{"switch", "--flake", ".#host", "--impure"}}
	if err := r.Build(context.Background(), "/safe/candidate"); err != nil {
		t.Fatal(err)
	}
	if err := r.Switch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Rollback(context.Background(), "/nix/store/generation"); err != nil {
		t.Fatal(err)
	}
	got := make([][]string, 0, len(fake.Runs))
	for _, cmd := range fake.Runs {
		got = append(got, append([]string{cmd.Name}, cmd.Args...))
	}
	want := [][]string{
		{"nixos-rebuild", "build", "-I", "nixos-config=/safe/candidate"},
		{"sudo", "--", "nixos-rebuild", "switch", "--flake", ".#host", "--impure"},
		{"sudo", "--", "/nix/store/generation/bin/switch-to-configuration", "switch"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got argv %q, want %q", got, want)
	}
}

func TestNixOSRejectsUnsafeOrFailedCalls(t *testing.T) {
	r := NixOS{Runner: &execx.FakeRunner{}, SwitchArgs: []string{"switch", "--bad\nflag"}}
	if err := r.Switch(context.Background()); err == nil {
		t.Fatal("accepted unsafe switch argument")
	}
	if err := r.Rollback(context.Background(), "/tmp/no"); err == nil {
		t.Fatal("accepted unsafe rollback generation")
	}
	fake := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: "bounded"}, errors.New("failed")
	}}
	if err := (NixOS{Runner: fake}).Build(context.Background(), "candidate"); err == nil {
		t.Fatal("expected build error")
	}
}
