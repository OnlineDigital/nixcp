package execx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	DefaultStdoutLimit = 64 * 1024
	DefaultStderrLimit = 64 * 1024
)

// Result captures process execution result with bounded output.
type Result struct {
	Cmd        []string `json:"-"`
	ExitCode   int      `json:"-"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	TimedOut   bool     `json:"timed_out"`
	DurationMs int64    `json:"duration_ms"`
}

// Command describes a controlled argv-based command execution.
type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string
	// Stdin, Stdout, and Stderr optionally override the host streams when
	// Interactive is set. Callers normally supply their Cobra streams so
	// passthrough output also works when the CLI is embedded or redirected.
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	StdoutMax int
	StderrMax int
	// Interactive attaches the process to the caller's terminal: stdin,
	// stdout and stderr stream through unbounded instead of being captured.
	// Use for user-facing REPLs (e.g. `artisan tinker`) that need a TTY.
	// Output is NOT captured in interactive mode; the exit code still is.
	Interactive bool
}

// IsInteractive reports whether the command needs a TTY attached.
func (c *Command) IsInteractive() bool { return c != nil && c.Interactive }

// Runner executes a command without shell interpretation.
type Runner interface {
	Run(context.Context, *Command) (Result, error)
}

// RedactArgv removes secrets-like arguments for safe diagnostics.
func RedactArgv(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	out := make([]string, len(argv))
	copy(out, argv)
	for i := 1; i < len(out); i++ {
		lower := strings.ToLower(out[i])
		if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "key=") {
			out[i] = "[redacted]"
		}
	}
	return out
}

// RealRunner uses exec.CommandContext with context-aware cancellation.
type RealRunner struct{}

func (r *RealRunner) Run(ctx context.Context, cmd *Command) (Result, error) {
	if cmd == nil {
		return Result{}, fmt.Errorf("command is nil")
	}
	if strings.TrimSpace(cmd.Name) == "" {
		return Result{}, fmt.Errorf("command name is empty")
	}

	stdoutLimit := DefaultStdoutLimit
	if cmd.StdoutMax > 0 {
		stdoutLimit = cmd.StdoutMax
	}
	stderrLimit := DefaultStderrLimit
	if cmd.StderrMax > 0 {
		stderrLimit = cmd.StderrMax
	}

	started := time.Now()
	execCmd := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	if cmd.Dir != "" {
		execCmd.Dir = cmd.Dir
	}
	if cmd.Env != nil {
		execCmd.Env = cmd.Env
	}

	if cmd.IsInteractive() {
		// TTY passthrough: the child inherits the real terminal so interactive
		// tools (artisan tinker) behave exactly as if invoked directly.
		// Signals reach it through the process group; its exit code is
		// propagated verbatim via ProcessExitError.
		execCmd.Stdin = cmd.Stdin
		if execCmd.Stdin == nil {
			execCmd.Stdin = os.Stdin
		}
		execCmd.Stdout = cmd.Stdout
		if execCmd.Stdout == nil {
			execCmd.Stdout = os.Stdout
		}
		execCmd.Stderr = cmd.Stderr
		if execCmd.Stderr == nil {
			execCmd.Stderr = os.Stderr
		}
		runErr := execCmd.Run()
		res := Result{Cmd: append([]string{cmd.Name}, cmd.Args...), ExitCode: exitCodeFromError(runErr), DurationMs: time.Since(started).Milliseconds()}
		if ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
		}
		if runErr != nil {
			return res, &ProcessExitError{Cmd: res.Cmd, ExitCode: res.ExitCode, Signal: signalFromError(runErr), Err: runErr}
		}
		return res, nil
	}

	bufOut := &boundedBuffer{limit: stdoutLimit}
	bufErr := &boundedBuffer{limit: stderrLimit}
	execCmd.Stdout = bufOut
	execCmd.Stderr = bufErr

	err := execCmd.Run()
	res := Result{
		Cmd:        append([]string{cmd.Name}, cmd.Args...),
		Stdout:     bufOut.String(),
		Stderr:     bufErr.String(),
		DurationMs: time.Since(started).Milliseconds(),
	}

	res.ExitCode = exitCodeFromError(err)
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
	}
	if err != nil {
		return res, &ProcessExitError{Cmd: res.Cmd, ExitCode: res.ExitCode, Signal: signalFromError(err), Stderr: res.Stderr, Err: err}
	}
	return res, nil
}

func exitCodeFromError(err error) int {
	exitErr, ok := err.(*exec.ExitError)
	if err == nil {
		return 0
	}
	if !ok {
		return 1
	}
	if exitErr.ExitCode() >= 0 {
		return exitErr.ExitCode()
	}
	return 1
}

// signalFromError reports the signal that killed the child, if any.
func signalFromError(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return status.Signal().String()
		}
	}
	return ""
}

// ProcessExitError is returned when a child process exits with a non-zero
// status or is terminated by a signal. It carries the exit code so CLI
// commands can propagate it verbatim and surface bounded stderr
// diagnostics in JSON envelopes.
type ProcessExitError struct {
	Cmd      []string
	ExitCode int
	Stderr   string
	Signal   string
	Err      error
}

func (e *ProcessExitError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Signal != "" {
		return fmt.Sprintf("command %q exited via signal %s", e.Cmd, e.Signal)
	}
	return fmt.Sprintf("command %q exited with code %d", e.Cmd, e.ExitCode)
}

func (e *ProcessExitError) Unwrap() error { return e.Err }

// Diagnostic renders a one-line structured diagnostic for JSON envelopes.
func (e *ProcessExitError) Diagnostic() string {
	if e == nil {
		return ""
	}
	argv := "[" + strings.Join(e.Cmd, " ") + "]"
	if e.Signal != "" {
		return fmt.Sprintf("%s exited via signal %s", argv, e.Signal)
	}
	return fmt.Sprintf("%s exited with code %d", argv, e.ExitCode)
}

// FakeRunner is deterministic and side-effect-free for tests.
type FakeRunner struct {
	// If set, handler decides output and error per invocation.
	Handle func(cmd *Command) (Result, error)
	// Runs contains all commands executed.
	Runs []*Command
	// Contexts records the context passed to each Run, aligned with Runs;
	// tests use it to assert deadline/cancellation propagation.
	Contexts []context.Context
}

func (f *FakeRunner) Run(ctx context.Context, cmd *Command) (Result, error) {
	if cmd == nil {
		return Result{}, fmt.Errorf("command is nil")
	}
	copyCmd := *cmd
	f.Runs = append(f.Runs, &copyCmd)
	f.Contexts = append(f.Contexts, ctx)
	if f.Handle != nil {
		return f.Handle(cmd)
	}
	return Result{Cmd: append([]string{cmd.Name}, cmd.Args...)}, nil
}

// boundedBuffer captures output up to an explicit byte limit.
type boundedBuffer struct {
	limit int
	buf   strings.Builder
	size  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.size
	if remaining <= 0 {
		return len(p), nil
	}
	toWrite := p
	if len(p) > remaining {
		toWrite = p[:remaining]
	}
	_, _ = b.buf.WriteString(string(toWrite))
	b.size += len(toWrite)
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}

var _ io.Writer = (*boundedBuffer)(nil)
