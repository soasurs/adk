package llmagent

import (
	"errors"
	"fmt"
)

// ErrMaxIterationsExceeded indicates that an agent run hit its configured
// MaxIterations limit.
var ErrMaxIterationsExceeded = errors.New("llmagent: max iterations exceeded")

// MaxIterationsError reports that an agent run exceeded its configured
// MaxIterations limit.
type MaxIterationsError struct {
	MaxIterations int
}

// Error implements the error interface.
func (e *MaxIterationsError) Error() string {
	return fmt.Sprintf("llmagent: max iterations exceeded (%d)", e.MaxIterations)
}

// Unwrap allows callers to match ErrMaxIterationsExceeded with errors.Is.
func (e *MaxIterationsError) Unwrap() error {
	return ErrMaxIterationsExceeded
}

// ErrToolNotFound indicates that a requested tool name was not registered on
// the agent.
var ErrToolNotFound = errors.New("llmagent: tool not found")

// ToolNotFoundError reports that a requested tool name was not registered on
// the agent.
type ToolNotFoundError struct {
	Name string
}

// Error implements the error interface.
func (e *ToolNotFoundError) Error() string {
	return fmt.Sprintf("llmagent: tool %q not found", e.Name)
}

// Unwrap allows callers to match ErrToolNotFound with errors.Is.
func (e *ToolNotFoundError) Unwrap() error {
	return ErrToolNotFound
}
