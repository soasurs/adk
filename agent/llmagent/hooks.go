package llmagent

import (
	"time"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
)

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

// ToolCallResult captures the outcome of a single tool invocation.
type ToolCallResult struct {
	// Message is the tool message that will be appended to the conversation.
	Message model.Message
	// Result is the raw successful tool result string.
	Result string
	// Err is the tool lookup or execution error, if any.
	Err error
	// Duration is the wall-clock time spent in the tool invocation.
	Duration time.Duration
}
