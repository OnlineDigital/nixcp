package transaction

import (
	"context"
	"errors"
	"github.com/nixcp/nixcp/internal/execx"
	"reflect"
	"testing"
)

func TestRebuilderUsesRestrictedArgv(t *testing.T) {
	fake := &execx.FakeRunner{Handle: func(c *execx.Command) (execx.Result, error) {
		if c.Name == "readlink" {
			return execx.Result{Stdout: "/nix/store/generation\n"}, nil
		}
		return execx.Result{}, nil
	}}
	r := NixOSRebuilder{Runner: fake, SwitchArgs: []string{"switch", "--flake", ".#host", "--impure"}}
	if e := r.Build(context.Background(), "/safe/candidate"); e != nil {
		t.Fatal(e)
	}
	if e := r.Switch(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := r.Rollback(context.Background(), "/nix/store/generation"); e != nil {
		t.Fatal(e)
	}
	got := [][]string{}
	for _, c := range fake.Runs {
		got = append(got, append([]string{c.Name}, c.Args...))
	}
	want := [][]string{{"nixos-rebuild", "build", "-I", "nixos-config=/safe/candidate"}, {"sudo", "--", "nixos-rebuild", "switch", "--flake", ".#host", "--impure"}, {"sudo", "--", "/nix/store/generation/bin/switch-to-configuration", "switch"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}
func TestRebuilderRejectsUnsafeOrFailedCalls(t *testing.T) {
	r := NixOSRebuilder{Runner: &execx.FakeRunner{}, SwitchArgs: []string{"switch", "--bad\nflag"}}
	if e := r.Switch(context.Background()); e == nil {
		t.Fatal("accepted unsafe")
	}
	if e := r.Rollback(context.Background(), "/tmp/no"); e == nil {
		t.Fatal("accepted unsafe rollback")
	}
	f := &execx.FakeRunner{Handle: func(*execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: "bounded"}, errors.New("failed")
	}}
	if e := (NixOSRebuilder{Runner: f}).Build(context.Background(), "candidate"); e == nil {
		t.Fatal("expected build error")
	}
}
