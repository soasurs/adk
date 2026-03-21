package runner

import (
	"errors"
	"fmt"
)

// ErrSessionNotFound indicates that the requested session does not exist.
var ErrSessionNotFound = errors.New("runner: session not found")

// SessionNotFoundError reports that the requested session does not exist.
type SessionNotFoundError struct {
	SessionID int64
}

// Error implements the error interface.
func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("runner: session %d not found", e.SessionID)
}

// Unwrap allows callers to match ErrSessionNotFound with errors.Is.
func (e *SessionNotFoundError) Unwrap() error {
	return ErrSessionNotFound
}
