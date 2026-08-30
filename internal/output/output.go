package output

import (
	"encoding/json"
	"io"
)

// Warning represents a user-visible warning in command output. It may be a
// stable warning code or a structured compatibility warning.
type Warning = any

// Diagnostic is a structured process exit diagnostic attached to failure
// envelopes when a child process (php, artisan) exited non-zero.
type Diagnostic struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Exit    *int   `json:"exit,omitempty"`
	Signal  string `json:"signal,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
}

// SuccessEnvelope is the JSON payload for successful command execution.
type SuccessEnvelope struct {
	Ok       bool   `json:"ok"`
	Command  string `json:"command"`
	Changed  bool   `json:"changed"`
	Data     any    `json:"data"`
	Warnings any    `json:"warnings"`
}

// ErrorInfo is attached to failed command envelopes.
type ErrorInfo struct {
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	Hint        string       `json:"hint"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// ErrorEnvelope is the JSON payload for failed command execution.
type ErrorEnvelope struct {
	Ok       bool      `json:"ok"`
	Command  string    `json:"command"`
	Error    ErrorInfo `json:"error"`
	Warnings any       `json:"warnings"`
}

// Success creates a stable success envelope.
func Success(command string, changed bool, data any, warnings any) SuccessEnvelope {
	return SuccessEnvelope{
		Ok:       true,
		Command:  command,
		Changed:  changed,
		Data:     data,
		Warnings: warnings,
	}
}

// Error creates a stable error envelope.
func Error(command, code, message, hint string, warnings any) ErrorEnvelope {
	return ErrorEnvelope{
		Ok:      false,
		Command: command,
		Error: ErrorInfo{
			Code:    code,
			Message: message,
			Hint:    hint,
		},
		Warnings: warnings,
	}
}

// ErrorWithDiagnostics creates a stable error envelope with structured
// process-exit diagnostics attached (e.g. php/artisan passthrough).
func ErrorWithDiagnostics(command, code, message, hint string, warnings any, diags []Diagnostic) ErrorEnvelope {
	return ErrorEnvelope{
		Ok:      false,
		Command: command,
		Error: ErrorInfo{
			Code:        code,
			Message:     message,
			Hint:        hint,
			Diagnostics: diags,
		},
		Warnings: warnings,
	}
}

// WriteJSON writes exactly one JSON object to out.
func WriteJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	// Escape HTML so JSON copied into a browser or log viewer cannot become
	// markup. Encoder still emits exactly one newline-terminated object.
	enc.SetEscapeHTML(true)
	return enc.Encode(v)
}
