package runner

import (
	"context"
	"errors"
	"time"

	"github.com/soasurs/adk/session"
)

const turnCleanupTimeout = 5 * time.Second

// FailureClassifier converts an execution error into structured information
// that is safe to persist and display. It must not copy arbitrary error text.
type FailureClassifier func(err error, stage session.TurnFailureStage) *session.TurnFailure

// DefaultFailureClassifier trusts only errors implementing
// session.TurnFailureProvider. Other errors receive a stable generic code and
// no persisted message.
func DefaultFailureClassifier(err error, stage session.TurnFailureStage) *session.TurnFailure {
	var provider session.TurnFailureProvider
	if errors.As(err, &provider) {
		failure := provider.TurnFailure()
		if failure.Stage == "" {
			failure.Stage = stage
		}
		if failure.Code != "" {
			return &failure
		}
	}
	code := "execution_error"
	switch stage {
	case session.TurnFailureStageAgent:
		code = "agent_error"
	case session.TurnFailureStageProvider:
		code = "provider_error"
	case session.TurnFailureStageTool:
		code = "tool_error"
	case session.TurnFailureStagePersistence:
		code = "persistence_error"
	case session.TurnFailureStageConsumer:
		code = "consumer_error"
	}
	return &session.TurnFailure{Code: code, Stage: stage}
}

func withTurnCleanupContext(ctx context.Context, fn func(context.Context) error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), turnCleanupTimeout)
	defer cancel()
	return fn(cleanupCtx)
}

func finalizeFailedTurn(
	ctx context.Context,
	turns session.TurnStore,
	turnID string,
	cause error,
	stage session.TurnFailureStage,
	classifier FailureClassifier,
) error {
	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		return turns.FinalizeTurn(ctx, turnID, session.TurnOutcome{
			Status: session.TurnInterrupted,
			Reason: session.TurnReasonDeadline,
		})
	case errors.Is(cause, context.Canceled):
		return turns.FinalizeTurn(ctx, turnID, session.TurnOutcome{
			Status: session.TurnInterrupted,
			Reason: session.TurnReasonCanceled,
		})
	default:
		failure := classifier(cause, stage)
		return turns.FinalizeTurn(ctx, turnID, session.TurnOutcome{
			Status:  session.TurnFailed,
			Reason:  session.TurnReasonAgentError,
			Failure: failure,
		})
	}
}
