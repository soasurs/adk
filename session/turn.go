package session

import (
	"context"
	"errors"
	"fmt"
)

// TurnStatus describes the durable lifecycle state of one Runner execution.
type TurnStatus string

const (
	// TurnRunning indicates that the execution has started but has not finalized.
	TurnRunning TurnStatus = "running"
	// TurnCompleted indicates that the execution finished normally.
	TurnCompleted TurnStatus = "completed"
	// TurnInterrupted indicates that execution stopped because it was canceled,
	// timed out, abandoned, or no longer consumed by the caller.
	TurnInterrupted TurnStatus = "interrupted"
	// TurnFailed indicates that execution terminated with an agent or framework error.
	TurnFailed TurnStatus = "failed"
)

// TurnReason is a stable, non-sensitive explanation for a terminal Turn state.
type TurnReason string

const (
	// TurnReasonCanceled indicates context cancellation.
	TurnReasonCanceled TurnReason = "canceled"
	// TurnReasonDeadline indicates that the execution deadline expired.
	TurnReasonDeadline TurnReason = "deadline_exceeded"
	// TurnReasonConsumerStopped indicates that the caller stopped consuming events.
	TurnReasonConsumerStopped TurnReason = "consumer_stopped"
	// TurnReasonAgentError indicates an agent, tool, provider, or framework failure.
	TurnReasonAgentError TurnReason = "agent_error"
	// TurnReasonAbandoned indicates a running Turn left behind by an earlier execution.
	TurnReasonAbandoned TurnReason = "abandoned"
)

// TurnFailureStage identifies where a safe, structured Turn failure occurred.
type TurnFailureStage string

const (
	// TurnFailureStageAgent identifies agent orchestration failures.
	TurnFailureStageAgent TurnFailureStage = "agent"
	// TurnFailureStageProvider identifies model-provider failures.
	TurnFailureStageProvider TurnFailureStage = "provider"
	// TurnFailureStageTool identifies tool execution or protocol failures.
	TurnFailureStageTool TurnFailureStage = "tool"
	// TurnFailureStagePersistence identifies session persistence failures.
	TurnFailureStagePersistence TurnFailureStage = "persistence"
	// TurnFailureStageConsumer identifies output delivery or consumer failures.
	TurnFailureStageConsumer TurnFailureStage = "consumer"
)

// TurnFailure contains structured failure information that is safe to persist
// and display. Message must never be populated from an arbitrary error string.
type TurnFailure struct {
	Code    string           `json:"code" db:"failure_code"`
	Message string           `json:"message" db:"failure_message"`
	Stage   TurnFailureStage `json:"stage" db:"failure_stage"`
}

// TurnFailureProvider may be implemented by typed errors that can expose
// structured failure information safe to persist and display.
type TurnFailureProvider interface {
	TurnFailure() TurnFailure
}

// Turn records the durable lifecycle metadata for one Runner execution. Events
// remain stored separately and are associated by SessionID and ID.
type Turn struct {
	ID         string       `json:"turn_id" db:"turn_id"`
	SessionID  string       `json:"session_id" db:"session_id"`
	Status     TurnStatus   `json:"status" db:"status"`
	Reason     TurnReason   `json:"reason" db:"reason"`
	Failure    *TurnFailure `json:"failure,omitempty" db:"-"`
	StartedAt  int64        `json:"started_at" db:"started_at"`
	FinishedAt int64        `json:"finished_at" db:"finished_at"`
}

// TurnOutcome describes one terminal state transition for a running Turn.
type TurnOutcome struct {
	// Status is the terminal state to record.
	Status TurnStatus
	// Reason is the stable terminal classification. It is empty for completed Turns.
	Reason TurnReason
	// Failure contains optional safe display metadata for a non-completed Turn.
	Failure *TurnFailure
}

// Validate checks that the outcome represents a valid terminal Turn state.
func (o TurnOutcome) Validate() error {
	switch o.Status {
	case TurnCompleted:
		if o.Reason != "" || o.Failure != nil {
			return fmt.Errorf("session: completed turn outcome must not include reason or failure")
		}
	case TurnInterrupted, TurnFailed:
		if o.Reason == "" {
			return fmt.Errorf("session: terminal turn outcome reason is empty")
		}
	default:
		return fmt.Errorf("session: turn outcome status %q is not terminal", o.Status)
	}
	switch o.Status {
	case TurnInterrupted:
		switch o.Reason {
		case TurnReasonCanceled, TurnReasonDeadline, TurnReasonConsumerStopped, TurnReasonAbandoned:
		default:
			return fmt.Errorf("session: reason %q is invalid for interrupted turn", o.Reason)
		}
	case TurnFailed:
		if o.Reason != TurnReasonAgentError {
			return fmt.Errorf("session: reason %q is invalid for failed turn", o.Reason)
		}
	}
	if o.Failure != nil {
		if !validTurnFailureCode(o.Failure.Code) {
			return fmt.Errorf("session: turn failure code %q is invalid", o.Failure.Code)
		}
		if o.Failure.Stage == "" {
			return fmt.Errorf("session: turn failure stage is empty")
		}
		switch o.Failure.Stage {
		case TurnFailureStageAgent,
			TurnFailureStageProvider,
			TurnFailureStageTool,
			TurnFailureStagePersistence,
			TurnFailureStageConsumer:
		default:
			return fmt.Errorf("session: turn failure stage %q is invalid", o.Failure.Stage)
		}
	}
	return nil
}

func validTurnFailureCode(code string) bool {
	if code == "" || len(code) > 128 {
		return false
	}
	for _, r := range code {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// TurnStore is an optional Session capability for durable Turn lifecycle
// tracking. Implementations must only allow transitions from TurnRunning to a
// terminal state and must make repeated finalization attempts fail atomically.
type TurnStore interface {
	// BeginTurn durably creates a running Turn.
	BeginTurn(ctx context.Context, turn Turn) error
	// FinalizeTurn atomically transitions a running Turn to one terminal outcome.
	FinalizeTurn(ctx context.Context, turnID string, outcome TurnOutcome) error
	// GetTurn returns one Turn, or nil when it does not exist.
	GetTurn(ctx context.Context, turnID string) (*Turn, error)
	// ListTurns returns all Turns in timeline order.
	ListTurns(ctx context.Context) ([]*Turn, error)
	// InterruptRunningTurns marks every running Turn as interrupted with reason.
	InterruptRunningTurns(ctx context.Context, reason TurnReason) error
}

// ErrTurnNotFound identifies a requested Turn that does not exist.
var ErrTurnNotFound = errors.New("session: turn not found")

// TurnNotFoundError reports a missing Turn.
type TurnNotFoundError struct {
	TurnID string
}

func (e *TurnNotFoundError) Error() string {
	return fmt.Sprintf("session: turn %q not found", e.TurnID)
}

// Unwrap supports errors.Is(err, ErrTurnNotFound).
func (e *TurnNotFoundError) Unwrap() error { return ErrTurnNotFound }

// ErrTurnStateConflict identifies an invalid Turn lifecycle transition.
var ErrTurnStateConflict = errors.New("session: turn state conflict")

// TurnStateConflictError reports an attempted transition from a non-running Turn.
type TurnStateConflictError struct {
	TurnID string
	Status TurnStatus
}

func (e *TurnStateConflictError) Error() string {
	return fmt.Sprintf("session: turn %q has status %q", e.TurnID, e.Status)
}

// Unwrap supports errors.Is(err, ErrTurnStateConflict).
func (e *TurnStateConflictError) Unwrap() error { return ErrTurnStateConflict }
