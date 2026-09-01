package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
)

func TestParseIsClosedAllowlist(t *testing.T) {
	for _, name := range []string{"nginx", "mariadb", "valkey"} {
		if _, err := Parse(name); err != nil {
			t.Fatalf("Parse(%q): %v", name, err)
		}
	}
	if _, err := Parse("sshd"); err == nil {
		t.Fatal("arbitrary systemd unit accepted")
	}
}
func TestAdapterStatusUsesOnlyFixedArgv(t *testing.T) {
	f := &execx.FakeRunner{Handle: func(c *execx.Command) (execx.Result, error) { return execx.Result{}, nil }}
	a := Adapter{Runner: f}
	actual, err := a.Status(context.Background(), Nginx)
	if err != nil {
		t.Fatal(err)
	}
	if !actual.Active || !actual.Enabled || actual.Health != "healthy" {
		t.Fatalf("unexpected actual: %#v", actual)
	}
	want := [][]string{{"systemctl", "is-active", "--quiet", "nginx.service"}, {"systemctl", "is-enabled", "--quiet", "nginx.service"}, {"sudo", "--", "nginx", "-t"}, {"ss", "-ltnH"}}
	var got [][]string
	for _, c := range f.Runs {
		got = append(got, append([]string{c.Name}, c.Args...))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv\ngot %#v\nwant %#v", got, want)
	}
}
func TestAdapterReturnsSystemdFailure(t *testing.T) {
	f := &execx.FakeRunner{Handle: func(c *execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: "unit failed"}, errors.New("exit status 1")
	}}
	if _, err := (Adapter{Runner: f}).Status(context.Background(), Nginx); err == nil {
		t.Fatal("expected error")
	}
}
func TestLocalityAndHTTPValidation(t *testing.T) {
	if err := validateListeners(Valkey, "LISTEN 0 511 0.0.0.0:6379 0.0.0.0:*"); err == nil {
		t.Fatal("public valkey accepted")
	}
	if err := validateListeners(MariaDB, "LISTEN 0 511 [::1]:3306 [::]:*"); err != nil {
		t.Fatal(err)
	}
	if err := validateListeners(Nginx, "LISTEN 0 511 0.0.0.0:443 0.0.0.0:*"); err == nil {
		t.Fatal("HTTPS listener accepted")
	}
}
