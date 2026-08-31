package database

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nixcp/nixcp/internal/execx"
)

func TestLocalCheckerUsesArgvAndChecksOnlyAffectedDatabases(t *testing.T) {
	runner := &execx.FakeRunner{}
	checker := LocalChecker{Runner: runner, Lookup: func(name string) (string, error) {
		if name != "mysql" {
			t.Fatalf("unexpected lookup %q", name)
		}
		return "/nix/store/mysql/bin/mysql", nil
	}}
	if err := checker.Check(context.Background(), []string{"nginx"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.Runs) != 0 {
		t.Fatalf("unrelated transaction ran database checks: %#v", runner.Runs)
	}
	if err := checker.Check(context.Background(), []string{"nginx", "database:app", "database:app"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.Runs) != 2 {
		t.Fatalf("runs=%d, want connection plus database", len(runner.Runs))
	}
	if got, want := runner.Runs[0].Args, []string{"--protocol=socket", "--batch", "--skip-column-names", "--execute", "SELECT 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("connection argv=%q want %q", got, want)
	}
	if got, want := runner.Runs[1].Args, []string{"--protocol=socket", "--batch", "--skip-column-names", "--database", "app", "--execute", "SELECT 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("database argv=%q want %q", got, want)
	}
}

func TestLocalCheckerDistinguishesUnavailableToolServiceAndDatabase(t *testing.T) {
	cases := []struct {
		name   string
		lookup func(string) (string, error)
		run    func(*execx.Command) (execx.Result, error)
		want   any
	}{
		{
			name: "tool", lookup: func(string) (string, error) { return "", errors.New("not found") }, want: &ToolUnavailableError{},
		},
		{
			name: "service", lookup: func(string) (string, error) { return "mysql", nil }, run: func(*execx.Command) (execx.Result, error) {
				return execx.Result{Stderr: "ERROR 2002 (HY000): Can't connect to local server"}, errors.New("exit")
			}, want: &ServiceUnavailableError{},
		},
		{
			name: "missing database", lookup: func(string) (string, error) { return "mysql", nil }, run: func(c *execx.Command) (execx.Result, error) {
				for _, arg := range c.Args {
					if arg == "--database" {
						return execx.Result{Stderr: "ERROR 1049 (42000): Unknown database 'app'"}, errors.New("exit")
					}
				}
				return execx.Result{}, nil
			}, want: &DatabaseMissingError{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &execx.FakeRunner{Handle: tc.run}
			err := (LocalChecker{Runner: runner, Lookup: tc.lookup}).Check(context.Background(), []string{"database:app"})
			switch tc.want.(type) {
			case *ToolUnavailableError:
				var target *ToolUnavailableError
				if !errors.As(err, &target) {
					t.Fatalf("error=%T %v", err, err)
				}
			case *ServiceUnavailableError:
				var target *ServiceUnavailableError
				if !errors.As(err, &target) {
					t.Fatalf("error=%T %v", err, err)
				}
			case *DatabaseMissingError:
				var target *DatabaseMissingError
				if !errors.As(err, &target) {
					t.Fatalf("error=%T %v", err, err)
				}
			}
		})
	}
}
