package errors

import (
	"fmt"
	"strings"
)

// ExitCode represents the stable CLI exit code class used by NixCP.
type ExitCode int

const (
	ExitCodeSuccess  ExitCode = 0
	ExitCodeUsage    ExitCode = 2
	ExitCodePrecond  ExitCode = 3
	ExitCodeLock     ExitCode = 4
	ExitCodePlatform ExitCode = 5
	ExitCodeBuild    ExitCode = 6
	ExitCodeHealth   ExitCode = 7
	ExitCodeRollback ExitCode = 8
	ExitCodeRuntime  ExitCode = 9
	// ExitCodeProcess is the base for propagating a child process's own exit
	// code verbatim: the final process exit code is ExitCodeProcess + child
	// code, matching common CLI passthrough semantics (npm/ssh style).
	ExitCodeProcess ExitCode = 100
)

// MaxProcessExitCode bounds how far process passthrough can reach so it can
// never collide with the reserved 0-9 class range.
const MaxProcessExitCode = 155

// AppError is a typed command error.
type AppError struct {
	Code      string
	Message   string
	Hint      string
	Details   string
	ExitClass ExitCode
	Cause     error
	// ProcessExit, when non-nil, marks a passthrough error whose process exit
	// code must be honored verbatim instead of the class-based code.
	ProcessExit *ProcessExit
}

// ProcessExit carries a child process exit code for verbatim propagation.
type ProcessExit struct {
	ExitCode int
	Command  string
	Stderr   string
	Signal   string
	// Live reports that the child already wrote directly to the user's
	// terminal. It lets the human renderer avoid duplicating its failure.
	Live bool
}

// WithProcessExit attaches process passthrough metadata to the error.
func (e *AppError) WithProcessExit(pe ProcessExit) *AppError {
	if e != nil {
		e.ProcessExit = &pe
	}
	return e
}

// ProcessExitCode returns the verbatim child process exit code, if any.
func (e *AppError) ProcessExitCode() (int, bool) {
	if e == nil || e.ProcessExit == nil {
		return 0, false
	}
	return e.ProcessExit.ExitCode, true
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Cause }

func (e *AppError) ExitCode() ExitCode {
	if e == nil {
		return ExitCodeRuntime
	}
	// Honor process passthrough verbatim (clamped to the safe range so the
	// 0-9 class codes stay reserved).
	if e.ProcessExit != nil {
		code := e.ProcessExit.ExitCode
		if code < 0 {
			code = 128 + (-code)
		}
		if code > int(MaxProcessExitCode) {
			code = int(MaxProcessExitCode)
		}
		if code == 0 {
			code = int(ExitCodeRuntime)
		}
		return ExitCode(code)
	}
	if e.ExitClass == 0 {
		return ExitCodeRuntime
	}
	return e.ExitClass
}

// New creates a typed command error.
func New(code, message, hint string, exit ExitCode) *AppError {
	if exit == 0 {
		exit = ExitCodeRuntime
	}
	if exit == ExitCodeSuccess {
		exit = ExitCodeRuntime
	}
	return &AppError{
		Code:      code,
		Message:   message,
		Hint:      hint,
		ExitClass: exit,
	}
}

// Normalize maps arbitrary errors into AppError.
func Normalize(err error) *AppError {
	if err == nil {
		return nil
	}
	if appErr, ok := err.(*AppError); ok {
		return appErr
	}

	msg := strings.TrimSpace(err.Error())
	exit := ExitCodeRuntime
	if msg == "" {
		msg = "unknown error"
	}
	if isUsageError(msg) {
		exit = ExitCodeUsage
	}
	return &AppError{
		Code:      "runtime_error",
		Message:   msg,
		ExitClass: exit,
		Cause:     err,
	}
}

func isUsageError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, needle := range []string{"unknown command", "unknown flag", "unknown", "unknown argument", "accepts", "expects", "invalid argument"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (e *AppError) CauseAsWarnings() []string {
	if e == nil {
		return nil
	}
	if e.Cause != nil {
		return []string{e.Cause.Error()}
	}
	if e.Details != "" {
		return []string{e.Details}
	}
	return nil
}
