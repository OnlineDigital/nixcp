package state

import "fmt"

// StateError represents a deterministic validation/migration error for state files.
type StateError struct {
	Code    string
	Message string
	Cause   error
}

func (e *StateError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *StateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newStateError(code, message string, cause error) *StateError {
	return &StateError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}
