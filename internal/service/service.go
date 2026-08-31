// Package service implements the closed NixCP service allowlist and its
// systemd/health adapters. It never accepts arbitrary unit names.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/nixcp/nixcp/internal/execx"
)

type Name string

const (
	Nginx   Name = "nginx"
	MariaDB Name = "mariadb"
	Valkey  Name = "valkey"
)

func Parse(raw string) (Name, error) {
	switch Name(raw) {
	case Nginx, MariaDB, Valkey:
		return Name(raw), nil
	default:
		return "", fmt.Errorf("unsupported service %q", raw)
	}
}
func (n Name) Unit() string {
	switch n {
	case Nginx:
		return "nginx.service"
	case MariaDB:
		return "mysql.service"
	case Valkey:
		// NixOS's Redis-compatible service module derives this unit name even
		// when its package is Valkey (services.redis.package = pkgs.valkey).
		return "redis-nixcp.service"
	default:
		return ""
	}
}

type Actual struct {
	Active  bool   `json:"active"`
	Enabled bool   `json:"enabled"`
	Health  string `json:"health"`
}
type Systemd interface {
	Status(context.Context, Name) (Actual, error)
	Restart(context.Context, Name) error
}

// Adapter uses a static service-to-unit mapping and explicit argv only.
type Adapter struct{ Runner execx.Runner }

func (a Adapter) Status(ctx context.Context, n Name) (Actual, error) {
	if n.Unit() == "" {
		return Actual{}, fmt.Errorf("unsupported service %q", n)
	}
	active, err := a.run(ctx, "systemctl", []string{"is-active", "--quiet", n.Unit()})
	if err != nil && active.ExitCode != 3 {
		return Actual{}, commandError("systemd status", active, err)
	}
	enabled, err := a.run(ctx, "systemctl", []string{"is-enabled", "--quiet", n.Unit()})
	if err != nil && enabled.ExitCode != 1 && enabled.ExitCode != 3 && enabled.ExitCode != 4 {
		return Actual{}, commandError("systemd enabled status", enabled, err)
	}
	result := Actual{Active: active.ExitCode == 0, Enabled: enabled.ExitCode == 0, Health: "not_checked"}
	if result.Active {
		if err := a.check(ctx, n); err != nil {
			return Actual{}, err
		}
		result.Health = "healthy"
	} else {
		result.Health = "inactive"
	}
	return result, nil
}
func (a Adapter) Restart(ctx context.Context, n Name) error {
	if n.Unit() == "" {
		return fmt.Errorf("unsupported service %q", n)
	}
	res, err := a.run(ctx, "sudo", []string{"--", "systemctl", "restart", n.Unit()})
	if err != nil {
		return commandError("systemd restart", res, err)
	}
	actual, err := a.Status(ctx, n)
	if err != nil {
		return err
	}
	if !actual.Active {
		return fmt.Errorf("%s did not become active", n)
	}
	return nil
}
func (a Adapter) Check(ctx context.Context, affected []string) error {
	for _, raw := range affected {
		n, err := Parse(raw)
		if err != nil {
			return err
		}
		actual, err := a.Status(ctx, n)
		if err != nil {
			return err
		}
		if !actual.Active {
			return fmt.Errorf("%s is not active", n)
		}
	}
	return nil
}
func (a Adapter) check(ctx context.Context, n Name) error {
	var name string
	var args []string
	switch n {
	case Nginx:
		name, args = "nginx", []string{"-t"}
	case MariaDB:
		name, args = "mysqladmin", []string{"ping", "--protocol=socket"}
	case Valkey:
		name, args = "valkey-cli", []string{"-h", "127.0.0.1", "ping"}
	}
	res, err := a.run(ctx, name, args)
	if err != nil {
		return commandError(string(n)+" health", res, err)
	}
	if n == Valkey && strings.TrimSpace(res.Stdout) != "PONG" {
		return fmt.Errorf("valkey health returned unexpected response")
	}
	// Listener locality is checked with a fixed, non-shell command. Public
	// listeners are rejected for data services; nginx must expose only HTTP.
	listeners, err := a.run(ctx, "ss", []string{"-ltnH"})
	if err != nil {
		return commandError("listener check", listeners, err)
	}
	return validateListeners(n, listeners.Stdout)
}
func validateListeners(n Name, out string) error {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		local := f[3]
		if n == Nginx && strings.HasSuffix(local, ":443") {
			return fmt.Errorf("nginx HTTPS listener is forbidden")
		}
		if n == MariaDB && (strings.HasSuffix(local, ":3306") && !(strings.Contains(local, "127.0.0.1:") || strings.Contains(local, "[::1]:"))) {
			return fmt.Errorf("mariadb listener is not local")
		}
		if n == Valkey && (strings.HasSuffix(local, ":6379") && !(strings.Contains(local, "127.0.0.1:") || strings.Contains(local, "[::1]:"))) {
			return fmt.Errorf("valkey listener is not local")
		}
	}
	return nil
}
func (a Adapter) run(ctx context.Context, name string, args []string) (execx.Result, error) {
	if a.Runner == nil {
		return execx.Result{}, fmt.Errorf("command runner unavailable")
	}
	return a.Runner.Run(ctx, &execx.Command{Name: name, Args: args, StdoutMax: execx.DefaultStdoutLimit, StderrMax: execx.DefaultStderrLimit})
}
func commandError(step string, r execx.Result, err error) error {
	if s := strings.TrimSpace(r.Stderr); s != "" {
		return fmt.Errorf("%s failed: %w: %s", step, err, s)
	}
	return fmt.Errorf("%s failed: %w", step, err)
}
