package model

import (
	"context"

	"soasurs.dev/soasurs/adk/tool"
)

// LLM is a provider-agnostic interface for interacting with a large language model.
type LLM interface {
	Name() string
	Generate(context.Context, *LLMRequest, *GenerateConfig) (*LLMResponse, error)
}

// Role represents the role of a message participant.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// FinishReason indicates why the LLM stopped generating tokens.
type FinishReason string

const (
	// FinishReasonStop means the model hit a natural stop point or a stop sequence.
	FinishReasonStop FinishReason = "stop"
	// FinishReasonToolCalls means the model wants to call one or more tools.
	FinishReasonToolCalls FinishReason = "tool_calls"
	// FinishReasonLength means the maximum token limit was reached.
	FinishReasonLength FinishReason = "length"
	// FinishReasonContentFilter means the content was filtered.
	FinishReasonContentFilter FinishReason = "content_filter"
)

// ReasoningEffort controls how much reasoning the model performs.
type ReasoningEffort string

const (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXhigh   ReasoningEffort = "xhigh"
)

// ServiceTier specifies the service tier for processing the request.
type ServiceTier string

const (
	ServiceTierAuto     ServiceTier = "auto"
	ServiceTierDefault  ServiceTier = "default"
	ServiceTierFlex     ServiceTier = "flex"
	ServiceTierScale    ServiceTier = "scale"
	ServiceTierPriority ServiceTier = "priority"
)

// GenerateConfig holds optional configuration for a generation request.
type GenerateConfig struct {
	Temperature     float64
	TopP            float64
	ReasoningEffort ReasoningEffort
	ServiceTier     ServiceTier
}

// ToolCall represents a single tool invocation requested by the LLM.
type ToolCall struct {
	// ID is a unique identifier for this tool call, used to match results back.
	ID string
	// Name is the name of the tool to invoke.
	Name string
	// Arguments is a JSON-encoded string of the tool's input parameters.
	Arguments string
}

// Message represents a single message in the conversation.
type Message struct {
	// Role is the participant role (system, user, assistant, tool).
	Role Role
	// Content is the text content of the message.
	Content string
	// ToolCalls is populated when Role is RoleAssistant and the model requests tool invocations.
	ToolCalls []ToolCall
	// ToolCallID links a RoleTool message back to the ToolCall.ID it is responding to.
	ToolCallID string
}

// Choice represents one completion candidate returned by the LLM.
type Choice struct {
	// Message is the assistant message for this choice.
	Message Message
	// FinishReason indicates why generation stopped.
	FinishReason FinishReason
}

// LLMRequest is the provider-agnostic request payload sent to an LLM.
type LLMRequest struct {
	// Model is the identifier of the model to use.
	Model string
	// Messages is the conversation history.
	Messages []Message
	// Tools is the list of tools the model may call during generation.
	Tools []tool.Tool
}

// LLMResponse is the provider-agnostic response returned by an LLM.
type LLMResponse struct {
	Message      Message
	FinishReason FinishReason
}
