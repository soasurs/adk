// Package trace defines span-oriented observability hooks for ADK runtime
// operations.
package trace

import (
	"context"
	"time"

	"github.com/soasurs/adk/model"
)

// Kind identifies the ADK operation represented by a span.
type Kind string

// ToolOutcome identifies whether a completed tool call succeeded or returned
// a model-visible failure.
type ToolOutcome string

const (
	// ToolOutcomeSuccess identifies a successful tool response.
	ToolOutcomeSuccess ToolOutcome = "success"
	// ToolOutcomeFailure identifies a handled, model-visible tool failure.
	ToolOutcomeFailure ToolOutcome = "failure"
)

const (
	// KindRunnerRun spans one complete Runner.Run call.
	KindRunnerRun Kind = "adk.runner.run"
	// KindRunnerLock spans waiting for a per-session run lock.
	KindRunnerLock Kind = "adk.runner.lock"
	// KindSessionLoad spans loading active session history.
	KindSessionLoad Kind = "adk.session.load"
	// KindEventPersist spans persisting one complete event.
	KindEventPersist Kind = "adk.session.persist_event"
	// KindAgentRun spans one Agent.Run invocation.
	KindAgentRun Kind = "adk.agent.run"
	// KindLLMIteration spans one LLM-agent loop iteration, including the model
	// call and any tool calls requested by that response.
	KindLLMIteration Kind = "adk.llm.iteration"
	// KindLLMCall spans one GenerateContent call.
	KindLLMCall Kind = "adk.llm.call"
	// KindToolCall spans one tool invocation.
	KindToolCall Kind = "adk.tool.call"
)

// RunInfo carries identifiers that correlate spans emitted during one run.
type RunInfo struct {
	// RunID identifies one Runner.Run call.
	RunID string
	// TurnID groups all events produced by one Runner.Run call.
	TurnID string
	// SessionID identifies the session passed to Runner.Run.
	SessionID string
	// AppID identifies the application or tenant that owns the session.
	AppID string
	// UserID identifies the end user that owns the session.
	UserID string
}

// Event describes an ADK operation span or span event.
type Event struct {
	// Kind identifies the ADK operation.
	Kind Kind
	// Time is the event timestamp. Implementations use time.Now when it is zero.
	Time time.Time
	// Duration is an optional measured operation duration.
	Duration time.Duration

	// RunID identifies one Runner.Run call.
	RunID string
	// TurnID groups all events produced by one Runner.Run call.
	TurnID string
	// SessionID identifies the session that owns the operation.
	SessionID string
	// AppID identifies the application or tenant that owns the session.
	AppID string
	// UserID identifies the end user that owns the session.
	UserID string

	// AgentName identifies the agent involved in the operation.
	AgentName string
	// Model identifies the LLM model involved in the operation.
	Model string
	// Iteration is the 1-based LLM-agent loop iteration.
	Iteration int
	// Stream reports whether the LLM call used streaming mode.
	Stream bool

	// EventID identifies the persisted event involved in the operation.
	EventID int64
	// EventAuthor is the ADK event author.
	EventAuthor string
	// EventRole is the content role of the ADK event.
	EventRole model.Role
	// EventCount reports how many events were processed by the operation.
	EventCount int
	// Partial reports whether the event is a streaming fragment.
	Partial bool

	// ToolName identifies the tool involved in the operation.
	ToolName string
	// ToolCallID identifies the model-requested tool call.
	ToolCallID string
	// ToolIndex is the 0-based tool-call index within one assistant response.
	ToolIndex int

	// FinishReason reports why the LLM stopped generating.
	FinishReason model.FinishReason
	// PromptTokens reports provider input token usage.
	PromptTokens int64
	// CompletionTokens reports provider output token usage.
	CompletionTokens int64
	// TotalTokens reports provider total token usage.
	TotalTokens int64
	// PartialResponses counts partial streaming responses observed in an LLM call.
	PartialResponses int
	// StoppedEarly reports that downstream iteration stopped before completion.
	StoppedEarly bool
	// ToolOutcome reports the completed outcome of a tool call.
	ToolOutcome ToolOutcome
	// Err is the Go error that ended the operation, if any.
	Err error
	// Attributes carries implementation-specific fields.
	Attributes map[string]any
}

// WithRunInfo returns a copy of e populated with any missing run identifiers.
func (e Event) WithRunInfo(info RunInfo) Event {
	if e.RunID == "" {
		e.RunID = info.RunID
	}
	if e.TurnID == "" {
		e.TurnID = info.TurnID
	}
	if e.SessionID == "" {
		e.SessionID = info.SessionID
	}
	if e.AppID == "" {
		e.AppID = info.AppID
	}
	if e.UserID == "" {
		e.UserID = info.UserID
	}
	return e
}

// Tracer starts ADK operation spans.
type Tracer interface {
	// Start starts a span for event and returns a context carrying that span.
	Start(ctx context.Context, event Event) (context.Context, Span)
}

// Span observes a started ADK operation.
type Span interface {
	// AddEvent records an instantaneous event on the span.
	AddEvent(ctx context.Context, event Event)
	// End completes the span.
	End(ctx context.Context, event Event)
}

// DiscardTracer ignores all spans.
type DiscardTracer struct{}

// Start implements Tracer.
func (DiscardTracer) Start(ctx context.Context, _ Event) (context.Context, Span) {
	return ctx, discardSpan{}
}

type discardSpan struct{}

func (discardSpan) AddEvent(context.Context, Event) {}
func (discardSpan) End(context.Context, Event)      {}
