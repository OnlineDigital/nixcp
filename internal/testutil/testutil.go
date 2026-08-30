// Package testutil provides shared test fakes and fixtures used across
// command-package tests: deterministic runners, accepting platform stubs,
// and state snapshots that validate cleanly.
package testutil

import (
	"context"
	"os"
	"path/filepath"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/transaction"
)

// AcceptingPlatform passes every host admission check.
type AcceptingPlatform struct{}

func (AcceptingPlatform) Check() error { return nil }

// FailingPlatform fails host admission with a stable error.
type FailingPlatform struct{ Err error }

func (p FailingPlatform) Check() error { return p.Err }

// RunnerSystemctlOK returns a FakeRunner that answers every systemctl probe
// the way the real adapter expects for an active+enabled unit (is-active and
// is-enabled both succeed; non-systemctl commands succeed generically).
func RunnerSystemctlOK() *execx.FakeRunner {
	return &execx.FakeRunner{Handle: func(cmd *execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: 0}, nil
	}}
}

// SystemdAllActive reports every allowlisted service active+enabled+healthy.
type SystemdAllActive struct{}

func (SystemdAllActive) Status(context.Context, service.Name) (service.Actual, error) {
	return service.Actual{Active: true, Enabled: true, Health: "healthy"}, nil
}
func (SystemdAllActive) Restart(context.Context, service.Name) error { return nil }

// SystemdAllInactive reports every allowlisted service stopped.
type SystemdAllInactive struct{}

func (SystemdAllInactive) Status(context.Context, service.Name) (service.Actual, error) {
	return service.Actual{Active: false, Enabled: false, Health: "stopped"}, nil
}
func (SystemdAllInactive) Restart(context.Context, service.Name) error { return nil }

// RebuilderNoop succeeds at every transaction step without touching NixOS.
type RebuilderNoop struct{}

func (RebuilderNoop) CurrentGeneration(context.Context) (string, error) { return "test-gen", nil }
func (RebuilderNoop) Build(context.Context, string) error               { return nil }
func (RebuilderNoop) Switch(context.Context) error                      { return nil }
func (RebuilderNoop) Rollback(context.Context, string) error            { return nil }

// HealthNoop passes every health check.
type HealthNoop struct{}

func (HealthNoop) Check(context.Context, []string) error { return nil }

// Transaction returns a transaction manager over an isolated state root
// whose rebuild and health steps always succeed.
func Transaction(home string) *transaction.Manager {
	root := state.NewStore(home).Root
	return &transaction.Manager{
		Root:      root,
		Locker:    transaction.FlockLocker{Path: filepath.Join(root, "lock")},
		Rebuilder: RebuilderNoop{},
		Health:    HealthNoop{},
	}
}

// BaseConfig returns a minimal, validating configuration snapshot for an
// isolated state home.
func BaseConfig(home string) state.ConfigSnapshot {
	return state.ConfigSnapshot{
		SchemaVersion: 2,
		Owner:         state.Owner{Username: "u", UID: os.Getuid(), Group: "g", GID: os.Getgid(), Home: home},
		Platform:      state.Platform{System: "x86_64-linux"},
		Rebuild:       state.RebuildConfig{Mode: "traditional"},
		Services: state.ServiceStates{
			Nginx:   state.ServiceConfig{DesiredState: "stopped"},
			MariaDB: state.ServiceConfig{DesiredState: "stopped"},
			Redis:   state.ServiceConfig{DesiredState: "stopped"},
		},
		PHP: state.PHPConfig{Installed: []string{"8.3"}, GlobalDefault: "8.3"},
	}
}
