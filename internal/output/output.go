package output

import (
	"encoding/json"
	"io"
)

// Warning represents a user-visible warning in command output. It may be a
// stable warning code or a structured compatibility warning.
type Warning = any

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
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
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

// WriteJSON writes exactly one JSON object to out.
func WriteJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
