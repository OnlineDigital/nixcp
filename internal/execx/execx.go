package execx

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
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
	Name      string
	Args      []string
	Dir       string
	Env       []string
	StdoutMax int
	StderrMax int
}

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

	return res, err
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

// FakeRunner is deterministic and side-effect-free for tests.
type FakeRunner struct {
	// If set, handler decides output and error per invocation.
	Handle func(cmd *Command) (Result, error)
	// Runs contains all commands executed.
	Runs []*Command
}

func (f *FakeRunner) Run(ctx context.Context, cmd *Command) (Result, error) {
	if cmd == nil {
		return Result{}, fmt.Errorf("command is nil")
	}
	copyCmd := *cmd
	f.Runs = append(f.Runs, &copyCmd)
	if ctx != nil {
		_ = ctx
	}
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
