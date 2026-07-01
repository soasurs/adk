package model

import (
	"context"
	"iter"

	"github.com/soasurs/adk/tool"
)

// LLM is a provider-agnostic interface for interacting with a large language model.
type LLM interface {
	Name() string
	// GenerateContent sends the request to the LLM and yields responses.
	// When stream is false, exactly one *LLMResponse is yielded (the complete response).
	// When stream is true, zero or more partial *LLMResponse are yielded (Partial=true)
	// followed by one complete *LLMResponse (Partial=false, TurnComplete=true).
	GenerateContent(ctx context.Context, req *LLMRequest, cfg *GenerateConfig, stream bool) iter.Seq2[*LLMResponse, error]
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

// ThinkingLevel controls Gemini's thinking depth.
type ThinkingLevel string

const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
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
	Temperature float64
	// ReasoningEffort controls OpenAI-style reasoning effort. It is primarily
	// intended for OpenAI-compatible APIs that expose reasoning_effort.
	ReasoningEffort ReasoningEffort
	// ThinkingLevel controls Gemini thinking depth. It is ignored by providers
	// that do not expose a thinking-level setting.
	ThinkingLevel ThinkingLevel
	ServiceTier   ServiceTier
	// MaxTokens overrides the maximum number of tokens to generate.
	// A zero value leaves the decision to the provider (which may use its own default).
	MaxTokens int64
	// ThinkingBudget overrides Anthropic's token budget for extended thinking.
	// A positive value enables thinking for Anthropic-compatible APIs even when
	// EnableThinking is nil. Other providers ignore it unless they happen to
	// support the same concept.
	ThinkingBudget int64
	// EnableThinking explicitly enables or disables the model's internal
	// reasoning/thinking capability when no more specific provider control is
	// supplied. A nil value leaves the decision to the provider.
	EnableThinking *bool
}

// ContentPartType identifies the modality of a ContentPart.
type ContentPartType string

const (
	// ContentPartTypeText represents a plain-text content part.
	ContentPartTypeText ContentPartType = "text"
	// ContentPartTypeImageURL represents an image provided via HTTPS URL.
	ContentPartTypeImageURL ContentPartType = "image_url"
	// ContentPartTypeImageBase64 represents an image provided as raw base64-encoded
	// data together with its MIME type. The adapter constructs the data URI automatically.
	ContentPartTypeImageBase64 ContentPartType = "image_base64"
)

// ImageDetail controls the resolution at which the model processes an image.
// Refer to the provider's vision guide for details.
type ImageDetail string

const (
	ImageDetailAuto ImageDetail = "auto"
	ImageDetailLow  ImageDetail = "low"
	ImageDetailHigh ImageDetail = "high"
)

// ContentPart represents a single piece of content within a message.
// A message may contain one or more parts of mixed modalities (text, image, etc.).
type ContentPart struct {
	// Type identifies the kind of content.
	Type ContentPartType
	// Text holds the plain-text content when Type is ContentPartTypeText.
	Text string
	// ImageURL is the HTTPS URL of the image when Type is ContentPartTypeImageURL.
	ImageURL string
	// ImageBase64 is the raw base64-encoded image data (no data URI prefix)
	// when Type is ContentPartTypeImageBase64.
	ImageBase64 string
	// MIMEType is the MIME type of the base64 image (e.g. "image/jpeg", "image/png").
	// Required when Type is ContentPartTypeImageBase64.
	MIMEType string
	// ImageDetail controls the fidelity at which the image is processed.
	// Defaults to "auto" when empty. Relevant for ContentPartTypeImageURL and
	// ContentPartTypeImageBase64.
	ImageDetail ImageDetail
}

// ToolCall represents a single tool invocation requested by the LLM.
type ToolCall struct {
	// ID is a unique identifier for this tool call, used to match results back.
	ID string
	// Name is the name of the tool to invoke.
	Name string
	// Arguments is a JSON-encoded string of the tool's input parameters.
	Arguments string
	// ThoughtSignature is an opaque token that some providers (e.g. Gemini thinking
	// models) attach to a function-call part. It must be echoed back verbatim in the
	// subsequent request so the provider can restore its reasoning context.
	// Non-Gemini adapters leave this field nil and ignore it on input.
	ThoughtSignature []byte
}

// TokenUsage holds the token consumption statistics for a single LLM call.
type TokenUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// Content is the provider-neutral payload carried by an Event.
type Content struct {
	// Role is the participant role (system, user, assistant, tool).
	Role Role
	// Content is the plain text content.
	// For multi-modal user content use Parts instead; Parts takes precedence
	// when non-empty.
	Content string
	// Parts holds multi-modal content (text, images, etc.).
	// When non-empty, Parts takes precedence over Content.
	// Currently only supported for RoleUser messages.
	Parts []ContentPart
	// ReasoningContent holds the model's internal chain-of-thought output, when
	// returned by reasoning models (e.g. DeepSeek-R1, o-series via compatible
	// providers). Most adapters treat it as informational only; providers that
	// require it for continuity, such as DeepSeek thinking-mode tool calls, may
	// forward it on subsequent turns.
	ReasoningContent string
	// ToolCalls is populated when Role is RoleAssistant and the model requests tool invocations.
	ToolCalls []ToolCall
	// ToolCallID links a RoleTool message back to the ToolCall.ID it is responding to.
	ToolCallID string
}

// Choice represents one completion candidate returned by the LLM.
type Choice struct {
	// Content is the assistant content for this choice.
	Content Content
	// FinishReason indicates why generation stopped.
	FinishReason FinishReason
}

// LLMRequest is the provider-agnostic request payload sent to an LLM.
type LLMRequest struct {
	// Model is the identifier of the model to use.
	Model string
	// Contents is the conversation history, projected from session events.
	Contents []Content
	// Tools is the list of tools the model may call during generation.
	Tools []tool.Tool
}

// LLMResponse is the provider-agnostic response returned by an LLM.
type LLMResponse struct {
	Content      Content
	FinishReason FinishReason
	// Usage holds the token consumption reported by the LLM provider.
	// Only populated on the final complete response (Partial=false).
	Usage *TokenUsage
	// Partial indicates this response is a streaming fragment.
	// When true, only Content.Content and Content.ReasoningContent carry
	// incremental (delta) text; other fields may be zero-valued.
	Partial bool
	// TurnComplete indicates the LLM has finished generating its full response.
	// Set to true on the final complete response (Partial=false).
	TurnComplete bool
}

// Event is the fundamental unit emitted by Agent.Run and persisted in a
// session. Complete events form the durable conversation ledger; partial events
// are transient streaming fragments for real-time display.
type Event struct {
	// ID is assigned by Runner before the event is persisted. Zero means the
	// event has not been persisted yet.
	ID int64
	// SessionID identifies the session that owns this event when persisted.
	SessionID string
	// Author identifies the producer of the event, for example "user" or an
	// agent name. It is display metadata and is not forwarded to the LLM.
	Author string
	// Content contains the payload for this event.
	// When Partial=true, only Content.Content and Content.ReasoningContent
	// carry incremental (delta) text; all other fields may be zero-valued.
	// When Partial=false, Content is fully assembled.
	Content Content
	// FinishReason indicates why model generation stopped for assistant events.
	FinishReason FinishReason
	// Usage holds token consumption when the event was produced by an LLM call.
	Usage *TokenUsage
	// Partial indicates this is a streaming fragment, not a complete message.
	// Callers (e.g. Runner) should forward partial events to the client for
	// real-time display but only persist complete events (Partial=false).
	Partial bool
	// CreatedAt is set by Runner or the session backend when persisted.
	CreatedAt int64
	// UpdatedAt is set by Runner or the session backend when persisted.
	UpdatedAt int64
}

// Persistable reports whether the event should be stored in session history.
func (e Event) Persistable() bool {
	return !e.Partial
}

// EventsFromContents wraps provider-facing contents as complete events.
func EventsFromContents(contents []Content) []Event {
	events := make([]Event, len(contents))
	for i, content := range contents {
		events[i] = Event{Content: content}
	}
	return events
}

// EventHistory wraps provider-facing contents as complete events.
func EventHistory(contents ...Content) []Event {
	return EventsFromContents(contents)
}
