package model

import (
	"context"
	"encoding/json"
	"fmt"
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

// GenerateConfig holds provider-neutral settings for a generation request.
// Provider-specific options such as reasoning effort, service tier, or thinking
// controls belong to the corresponding adapter package.
type GenerateConfig struct {
	// Temperature controls sampling randomness. A zero value leaves the decision
	// to the provider.
	Temperature float64
	// MaxTokens overrides the maximum number of tokens to generate.
	// A zero value leaves the decision to the provider (which may use its own default).
	MaxTokens int64
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
	// Arguments is the raw JSON payload of the tool's input parameters.
	Arguments json.RawMessage
	// ThoughtSignature is an opaque token that some providers (e.g. Gemini thinking
	// models) attach to a function-call part. It must be echoed back verbatim in the
	// subsequent request so the provider can restore its reasoning context.
	// Non-Gemini adapters leave this field nil and ignore it on input.
	ThoughtSignature []byte
}

// ToolResponse represents the completed outcome of one tool invocation.
type ToolResponse struct {
	// ToolCallID links this response to the ToolCall.ID it answers.
	ToolCallID string
	// Name is the tool name associated with the response.
	Name string
	// Outcome is either a successful *tool.Result or a model-visible
	// *tool.HandledError.
	Outcome tool.Outcome
}

// Text returns the plain-text form that should be used when a provider does
// not support structured tool results.
func (r ToolResponse) Text() string {
	if r.Outcome == nil {
		return ""
	}
	return r.Outcome.Text()
}

// MarshalJSON encodes the sealed outcome as exactly one result or error field.
func (r ToolResponse) MarshalJSON() ([]byte, error) {
	var wire struct {
		ToolCallID string             `json:"tool_call_id"`
		Name       string             `json:"name,omitempty"`
		Result     *tool.Result       `json:"result,omitempty"`
		Error      *tool.HandledError `json:"error,omitempty"`
	}
	wire.ToolCallID = r.ToolCallID
	wire.Name = r.Name
	switch outcome := r.Outcome.(type) {
	case *tool.Result:
		if outcome == nil {
			return nil, fmt.Errorf("model: tool response contains a nil result")
		}
		wire.Result = outcome
	case *tool.HandledError:
		if outcome == nil {
			return nil, fmt.Errorf("model: tool response contains a nil handled error")
		}
		wire.Error = outcome
	default:
		return nil, fmt.Errorf("model: tool response contains unsupported outcome %T", r.Outcome)
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes a tool response containing exactly one result or error.
func (r *ToolResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		ToolCallID string             `json:"tool_call_id"`
		Name       string             `json:"name,omitempty"`
		Result     *tool.Result       `json:"result,omitempty"`
		Error      *tool.HandledError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if (wire.Result == nil) == (wire.Error == nil) {
		return fmt.Errorf("model: tool response must contain exactly one outcome")
	}
	r.ToolCallID = wire.ToolCallID
	r.Name = wire.Name
	if wire.Result != nil {
		r.Outcome = wire.Result
	} else {
		r.Outcome = wire.Error
	}
	return nil
}

// TokenUsage holds the token consumption statistics for a single LLM call.
type TokenUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Details          *TokenUsageDetails
}

// TokenUsageDetails holds provider-neutral token usage breakdowns for a single
// LLM call. These fields are informational; callers should use TokenUsage's
// aggregate fields for billing and limit accounting unless they explicitly need
// a provider-reported breakdown.
type TokenUsageDetails struct {
	// CachedPromptTokens is the number of prompt tokens served from cache.
	CachedPromptTokens int64 `json:"cached_prompt_tokens,omitempty"`
	// CacheCreationPromptTokens is the number of prompt tokens used to create a cache entry.
	CacheCreationPromptTokens int64 `json:"cache_creation_prompt_tokens,omitempty"`
	// CacheReadPromptTokens is the number of prompt tokens read from a cache entry.
	CacheReadPromptTokens int64 `json:"cache_read_prompt_tokens,omitempty"`
	// ReasoningTokens is the number of output tokens used for model reasoning.
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
	// ToolUsePromptTokens is the number of prompt tokens from tool execution results.
	ToolUsePromptTokens int64 `json:"tool_use_prompt_tokens,omitempty"`
	// AudioPromptTokens is the number of prompt tokens from audio input.
	AudioPromptTokens int64 `json:"audio_prompt_tokens,omitempty"`
	// AudioCompletionTokens is the number of completion tokens from audio output.
	AudioCompletionTokens int64 `json:"audio_completion_tokens,omitempty"`
	// AcceptedPredictionTokens is the number of predicted output tokens accepted by the model.
	AcceptedPredictionTokens int64 `json:"accepted_prediction_tokens,omitempty"`
	// RejectedPredictionTokens is the number of predicted output tokens rejected by the model.
	RejectedPredictionTokens int64 `json:"rejected_prediction_tokens,omitempty"`
}

// IsZero reports whether d contains no provider-reported detail values.
func (d TokenUsageDetails) IsZero() bool {
	return d == TokenUsageDetails{}
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
	// ToolResponse is populated when Role is RoleTool.
	ToolResponse *ToolResponse
	// ToolCallID links a RoleTool message back to the ToolCall.ID it is
	// responding to. It is kept as a text fallback for simple callers; when
	// ToolResponse is non-nil, ToolResponse.ToolCallID takes precedence.
	ToolCallID string
}

// ToolResponseValue returns the explicit tool response when present, otherwise
// it builds a successful response from the legacy RoleTool fallback fields.
func (c Content) ToolResponseValue() ToolResponse {
	if c.ToolResponse != nil {
		response := *c.ToolResponse
		if response.ToolCallID == "" {
			response.ToolCallID = c.ToolCallID
		}
		if response.Outcome == nil && c.Content != "" {
			response.Outcome = &tool.Result{Content: c.Content}
		}
		return response
	}
	return ToolResponse{
		ToolCallID: c.ToolCallID,
		Outcome:    &tool.Result{Content: c.Content},
	}
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
	// TurnID groups all events produced by one Runner.Run call. It is a
	// correlation identifier, not an ordering key; event ordering remains
	// defined by CreatedAt and ID.
	TurnID string
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
