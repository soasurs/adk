package llmagent

import (
	"context"
	"time"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
)

// BeforeLLMCall runs immediately before an LLM invocation. Returning a
// non-nil response skips the remaining before hooks and the actual LLM call.
type BeforeLLMCall func(ctx context.Context, call *LLMCall) (*model.LLMResponse, error)

// AfterLLMCall runs after an LLM invocation completes or fails.
type AfterLLMCall func(ctx context.Context, call *LLMCall, result *LLMCallResult) error

// BeforeToolCall runs immediately before a tool invocation. Returning a
// non-nil override skips the remaining before hooks and the actual tool call.
type BeforeToolCall func(ctx context.Context, call *ToolCall) (*ToolCallOverride, error)

// AfterToolCall runs after a tool invocation completes or fails.
type AfterToolCall func(ctx context.Context, call *ToolCall, result *ToolCallResult) error

// LLMCall describes one LLM invocation within an LlmAgent Run loop.
type LLMCall struct {
	// AgentName is the logical name of the enclosing agent.
	AgentName string
	// Iteration is the 1-based LLM turn within the current Run call.
	Iteration int
	// Request is the request passed to the model.
	Request *model.LLMRequest
	// GenerateConfig is the optional generation config passed to the model.
	GenerateConfig *model.GenerateConfig
	// Stream indicates whether the model call is streaming.
	Stream bool
}

// LLMCallResult captures the outcome of a single LLM invocation.
type LLMCallResult struct {
	// Response is the final complete model response when one was produced.
	Response *model.LLMResponse
	// Err is the terminal error returned by the model, if any.
	Err error
	// PartialResponses counts how many partial streaming responses were observed.
	PartialResponses int
	// Duration is the wall-clock time spent in the model call.
	Duration time.Duration
	// StoppedEarly reports whether downstream iteration stopped before the full
	// result was consumed.
	StoppedEarly bool
}

// ToolCall describes one tool invocation requested by the model.
type ToolCall struct {
	// AgentName is the logical name of the enclosing agent.
	AgentName string
	// Iteration is the 1-based LLM turn that produced this tool call.
	Iteration int
	// ToolIndex is the 0-based position within the assistant message's ToolCalls.
	ToolIndex int
	// Request is the tool call requested by the model.
	Request model.ToolCall
	// Tool is the resolved tool implementation. It is nil when the tool was not found.
	Tool tool.Tool
	// Definition is the resolved tool metadata. When the tool was not found,
	// only Definition.Name is guaranteed to be set.
	Definition tool.Definition
}

// ToolCallOverride is a completed outcome supplied by a BeforeToolCall hook in
// place of invoking the resolved tool.
type ToolCallOverride struct {
	// Outcome is the successful or handled-failure outcome returned to the model.
	Outcome tool.Outcome
}

// CompleteToolCall creates an override that completes a tool call with outcome.
func CompleteToolCall(outcome tool.Outcome) *ToolCallOverride {
	return &ToolCallOverride{Outcome: outcome}
}

// ToolCallResult captures the outcome observed by an AfterToolCall hook.
type ToolCallResult struct {
	// Response is the completed tool response. Its outcome is either a successful
	// *tool.Result or a model-visible *tool.HandledError. It is nil when Err is
	// non-nil.
	Response *model.ToolResponse
	// Err is the terminal tool execution error, if any. A non-nil Err aborts the
	// current agent Run.
	Err error
	// Duration is the wall-clock time spent in the tool invocation.
	Duration time.Duration
}
