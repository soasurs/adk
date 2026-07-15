package runner

import (
	"errors"
	"fmt"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
)

// ErrSessionNotFound indicates that the requested session does not exist.
var ErrSessionNotFound = errors.New("runner: session not found")

// SessionNotFoundError reports that the requested session does not exist.
type SessionNotFoundError struct {
	SessionID string
}

// Error implements the error interface.
func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("runner: session %q not found", e.SessionID)
}

// Unwrap allows callers to match ErrSessionNotFound with errors.Is.
func (e *SessionNotFoundError) Unwrap() error {
	return ErrSessionNotFound
}

// ErrToolExecutionUnknown indicates that persisted history contains tool
// calls without matching durable results, so their execution status cannot be
// determined safely.
var ErrToolExecutionUnknown = errors.New("runner: tool execution status unknown")

// ToolExecutionUnknownError reports unresolved tool calls from one assistant
// event. Runner returns this error before persisting a new user event or
// invoking the agent.
type ToolExecutionUnknownError struct {
	// SessionID identifies the affected session.
	SessionID string
	// TurnID identifies the turn that emitted the assistant tool calls. It may
	// be empty for events written before turn IDs were introduced.
	TurnID string
	// EventID identifies the persisted assistant event containing the calls.
	EventID int64
	// ToolCalls contains only calls that have no matching persisted result.
	ToolCalls []model.ToolCall
}

// Error implements the error interface.
func (e *ToolExecutionUnknownError) Error() string {
	return fmt.Sprintf(
		"runner: tool execution status unknown for %d call(s) in session %q at event %d",
		len(e.ToolCalls),
		e.SessionID,
		e.EventID,
	)
}

// Unwrap allows callers to match ErrToolExecutionUnknown with errors.Is.
func (e *ToolExecutionUnknownError) Unwrap() error {
	return ErrToolExecutionUnknown
}

// TurnFailure returns safe structured metadata for durable Turn display.
func (e *ToolExecutionUnknownError) TurnFailure() session.TurnFailure {
	return session.TurnFailure{
		Code:    "tool_execution_unknown",
		Message: "tool execution outcome is unknown",
		Stage:   session.TurnFailureStageTool,
	}
}
