// Package database contains bounded, local-only verification for databases
// declared in NixCP state.
package database

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/nixcp/nixcp/internal/execx"
)

// ToolUnavailableError means neither supported local MariaDB client is
// installed. It is intentionally distinct from a database that does not
// exist, so operators get an actionable host-runtime diagnostic.
type ToolUnavailableError struct{ Err error }

func (e *ToolUnavailableError) Error() string {
	return "MariaDB client unavailable: install mysql or mariadb"
}
func (e *ToolUnavailableError) Unwrap() error { return e.Err }

// ServiceUnavailableError means a local client exists but could not connect
// to the local MariaDB socket/service.
type ServiceUnavailableError struct{ Detail string }

func (e *ServiceUnavailableError) Error() string {
	if e.Detail == "" {
		return "MariaDB service unavailable"
	}
	return "MariaDB service unavailable: " + e.Detail
}

// DatabaseMissingError identifies a declared database absent from MariaDB.
type DatabaseMissingError struct{ Database string }

func (e *DatabaseMissingError) Error() string {
	return fmt.Sprintf("MariaDB database %q is missing", e.Database)
}

// Checker follows transaction.HealthChecker's contract without importing the
// transaction package. Implementations inspect only database:<name> resources.
type Checker interface {
	Check(context.Context, []string) error
}

// LocalChecker verifies a local socket connection and every affected named
// database. Lookup is injected so tests do not depend on PATH; the production
// default uses exec.LookPath. Commands use argv exclusively.
type LocalChecker struct {
	Runner execx.Runner
	Lookup func(string) (string, error)
}

func (c LocalChecker) Check(ctx context.Context, affected []string) error {
	databases := affectedDatabases(affected)
	if len(databases) == 0 {
		return nil
	}
	if c.Runner == nil {
		return fmt.Errorf("MariaDB checker is not configured")
	}
	lookup := c.Lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	client, err := localClient(lookup)
	if err != nil {
		return &ToolUnavailableError{Err: err}
	}
	if err := c.query(ctx, client, ""); err != nil {
		return classifyConnection(err)
	}
	for _, name := range databases {
		if err := c.query(ctx, client, name); err != nil {
			if isMissingDatabase(err.Error()) {
				return &DatabaseMissingError{Database: name}
			}
			return classifyConnection(err)
		}
	}
	return nil
}

func affectedDatabases(affected []string) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, resource := range affected {
		if name, ok := strings.CutPrefix(resource, "database:"); ok && name != "" {
			if _, exists := seen[name]; !exists {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	return names
}

func localClient(lookup func(string) (string, error)) (string, error) {
	var lookupErr error
	for _, name := range []string{"mysql", "mariadb"} {
		path, err := lookup(name)
		if err == nil {
			return path, nil
		}
		lookupErr = err
	}
	return "", lookupErr
}

func (c LocalChecker) query(ctx context.Context, client, database string) error {
	args := []string{"--protocol=socket", "--batch", "--skip-column-names"}
	if database != "" {
		args = append(args, "--database", database)
	}
	args = append(args, "--execute", "SELECT 1")
	result, err := c.Runner.Run(ctx, &execx.Command{Name: client, Args: args, StdoutMax: execx.DefaultStdoutLimit, StderrMax: execx.DefaultStderrLimit})
	if err == nil && result.ExitCode == 0 {
		return nil
	}
	detail := strings.TrimSpace(strings.Join([]string{result.Stderr, result.Stdout}, "\n"))
	if detail == "" && err != nil {
		detail = err.Error()
	}
	return errors.New(detail)
}

func isMissingDatabase(detail string) bool {
	detail = strings.ToLower(detail)
	return strings.Contains(detail, "unknown database") || strings.Contains(detail, "error 1049")
}

func classifyConnection(err error) error {
	detail := err.Error()
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "can't connect") || strings.Contains(lower, "cannot connect") || strings.Contains(lower, "error 2002") || strings.Contains(lower, "error 2003") {
		return &ServiceUnavailableError{Detail: detail}
	}
	return fmt.Errorf("MariaDB verification failed: %w", err)
}
