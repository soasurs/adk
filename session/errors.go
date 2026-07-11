package session

import (
	"errors"
	"fmt"
)

// ErrArchiveBoundaryNotFound indicates that an archive boundary is not an
// active event in the session.
var ErrArchiveBoundaryNotFound = errors.New("session: archive boundary not found")

// ArchiveBoundaryNotFoundError reports the missing archive boundary event.
type ArchiveBoundaryNotFoundError struct {
	EventID int64
}

// Error implements the error interface.
func (e *ArchiveBoundaryNotFoundError) Error() string {
	return fmt.Sprintf("session: archive boundary event %d not found", e.EventID)
}

// Unwrap allows callers to match ErrArchiveBoundaryNotFound with errors.Is.
func (e *ArchiveBoundaryNotFoundError) Unwrap() error {
	return ErrArchiveBoundaryNotFound
}
