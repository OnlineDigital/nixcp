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
)

// AppError is a typed command error.
type AppError struct {
	Code      string
	Message   string
	Hint      string
	Details   string
	ExitClass ExitCode
	Cause     error
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
