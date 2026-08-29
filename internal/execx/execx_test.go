package execx

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestFakeRunnerCapturesInvocation(t *testing.T) {
	runner := &FakeRunner{}
	cmd := &Command{Name: "php", Args: []string{"--version"}}
	res, err := runner.Run(context.Background(), cmd)
	if err != nil {
		t.Fatalf("fake runner should not fail: %v", err)
	}
	if got := res.Cmd[0]; got != "php" {
		t.Fatalf("expected captured argv start, got %q", got)
	}
	if len(runner.Runs) != 1 {
		t.Fatalf("expected one run, got %d", len(runner.Runs))
	}
}

func TestFakeRunnerUsesHandlerAndKeepsDeterministicExitCode(t *testing.T) {
	runner := &FakeRunner{Handle: func(cmd *Command) (Result, error) {
		return Result{ExitCode: 42, Stdout: "done", DurationMs: 1}, nil
	}}
	res, err := runner.Run(context.Background(), &Command{Name: "echo", Args: []string{"ok"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", res.ExitCode)
	}
}

func TestBoundedBufferTruncatesOutput(t *testing.T) {
	buf := &boundedBuffer{limit: 4}
	payload := []byte("abcdef")
	n, err := buf.Write(payload)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("expected write count %d, got %d", len(payload), n)
	}
	if got := buf.String(); got != "abcd" {
		t.Fatalf("expected truncated output, got %q", got)
	}
}

func TestRealRunnerRejectsInvalidCommand(t *testing.T) {
	r := &RealRunner{}
	_, err := r.Run(context.Background(), &Command{Name: ""})
	if err == nil {
		t.Fatalf("expected error for empty command")
	}
}

func TestRedactArgvHidesSensitiveValues(t *testing.T) {
	argz := RedactArgv([]string{"php", "--password=abc", "token=shh", "safe", "secret"})
	if argz[1] != "[redacted]" || argz[2] != "[redacted]" {
		t.Fatalf("expected redaction in sensitive args, got %#v", argz)
	}
	if argz[0] != "php" || argz[3] != "safe" || argz[4] != "[redacted]" {
		t.Fatalf("unexpected untouched args: %#v", argz)
	}
}

func TestRealRunnerCollectsBoundedOutput(t *testing.T) {
	r := &RealRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, _ := r.Run(ctx, &Command{Name: "bash", Args: []string{"-lc", "printf 'hello-world'"}, StdoutMax: 4})
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", res.ExitCode)
	}
	if len(res.Stdout) != 4 {
		t.Fatalf("expected bounded stdout of 4, got %d (%q)", len(res.Stdout), res.Stdout)
	}
	if !bytes.Contains([]byte(res.Stdout), []byte("hell")) {
		t.Fatalf("expected truncated stdout content, got %q", res.Stdout)
	}
}
