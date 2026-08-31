// Package database contains bounded, local-only verification for databases
// declared in NixCP state.
package database

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// DBCredentials is the per-database account NixCP provisions for a site.
// User is always the database name; Password is the generated owner-only secret.
type DBCredentials struct {
	Database string
	User     string
	Password string
}

// LocalChecker verifies a local socket connection and every affected named
// database. Lookup is injected so tests do not depend on PATH; the production
// default uses exec.LookPath. Commands use argv exclusively.
//
// When Credentials is non-empty, each database is verified as its own dedicated
// user (the password is passed via MYSQL_PWD env, never argv, so it cannot leak
// through ps); databases without a credential still fall back to the anonymous
// current-OS-user socket check.
type LocalChecker struct {
	Runner      execx.Runner
	Lookup      func(string) (string, error)
	Credentials map[string]DBCredentials
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
	if len(c.Credentials) == 0 {
		// Anonymous socket probe only when no per-site account is involved.
		if err := c.query(ctx, client, "", DBCredentials{}, false); err != nil {
			return classifyConnection(err)
		}
	}
	for _, name := range databases {
		cred, hasCred := c.Credentials[name]
		if err := c.query(ctx, client, name, cred, hasCred); err != nil {
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

func (c LocalChecker) query(ctx context.Context, client, database string, cred DBCredentials, hasCred bool) error {
	args := []string{"--protocol=socket", "--batch", "--skip-column-names"}
	cmd := &execx.Command{Name: client, Args: args, StdoutMax: execx.DefaultStdoutLimit, StderrMax: execx.DefaultStderrLimit}
	if hasCred {
		if cred.User != "" {
			cmd.Args = append(cmd.Args, "--user", cred.User)
		}
		if cred.Password != "" {
			cmd.Env = append(os.Environ(), "MYSQL_PWD="+cred.Password)
		}
	}
	if database != "" {
		cmd.Args = append(cmd.Args, "--database", database)
	}
	cmd.Args = append(cmd.Args, "--execute", "SELECT 1")
	result, err := c.Runner.Run(ctx, cmd)
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
