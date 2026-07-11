// Package event defines the persisted session event representation.
package event

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
)

// Parts is a slice of ContentPart that serializes to/from JSON for database storage.
type Parts []model.ContentPart

// Value implements driver.Valuer so Parts can be written to the database as a JSON string.
func (p Parts) Value() (driver.Value, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner so Parts can be read from the database from a JSON string.
func (p *Parts) Scan(src any) error {
	if src == nil {
		*p = Parts{}
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("event: unsupported Parts source type: %T", src)
	}
	return json.Unmarshal([]byte(s), p)
}

// ToolCall represents a persisted tool call within an assistant event.
type ToolCall struct {
	// ID is the unique identifier of this tool call, matching model.ToolCall.ID.
	ID        string          `json:"id" db:"id"`
	Name      string          `json:"name" db:"name"`
	Arguments json.RawMessage `json:"arguments" db:"arguments"`
	// ThoughtSignature is provider-supplied opaque state that must survive
	// history persistence for subsequent Gemini tool-call turns.
	ThoughtSignature []byte `json:"thought_signature,omitempty" db:"thought_signature"`
}

// UnmarshalJSON accepts both the current raw JSON argument value and the older
// JSON-string representation used by pre-structured tool calls.
func (tc *ToolCall) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID               string          `json:"id"`
		Name             string          `json:"name"`
		Arguments        json.RawMessage `json:"arguments"`
		ThoughtSignature []byte          `json:"thought_signature,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	args := raw.Arguments
	var oldArgs string
	if len(args) > 0 && json.Unmarshal(args, &oldArgs) == nil {
		args = json.RawMessage(oldArgs)
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	tc.ID = raw.ID
	tc.Name = raw.Name
	tc.Arguments = args
	tc.ThoughtSignature = raw.ThoughtSignature
	return nil
}

// ToolCalls is a slice of ToolCall that serializes to/from JSON for database storage.
type ToolCalls []ToolCall

// Value implements driver.Valuer so ToolCalls can be written to the database as a JSON string.
func (tl ToolCalls) Value() (driver.Value, error) {
	b, err := json.Marshal(tl)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner so ToolCalls can be read from the database from a JSON string.
func (tl *ToolCalls) Scan(src any) error {
	if src == nil {
		*tl = ToolCalls{}
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("event: unsupported ToolCalls source type: %T", src)
	}
	return json.Unmarshal([]byte(s), tl)
}

// ToolResponse is a persisted tool response within a tool event.
type ToolResponse struct {
	ToolCallID string             `json:"tool_call_id"`
	Name       string             `json:"name,omitempty"`
	Result     *tool.Result       `json:"result,omitempty"`
	Error      *tool.HandledError `json:"error,omitempty"`
}

// Value implements driver.Valuer so ToolResponse can be written as JSON.
func (tr ToolResponse) Value() (driver.Value, error) {
	if tr.ToolCallID == "" && tr.Name == "" && tr.Result == nil && tr.Error == nil {
		return "", nil
	}
	if (tr.Result == nil) == (tr.Error == nil) {
		return nil, fmt.Errorf("event: tool response must contain exactly one outcome")
	}
	b, err := json.Marshal(tr)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner so ToolResponse can be read from a JSON string.
// It accepts the legacy flat shape containing is_error for history compatibility.
func (tr *ToolResponse) Scan(src any) error {
	if src == nil {
		*tr = ToolResponse{}
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("event: unsupported ToolResponse source type: %T", src)
	}
	if s == "" {
		*tr = ToolResponse{}
		return nil
	}
	var raw struct {
		ToolCallID        string             `json:"tool_call_id"`
		Name              string             `json:"name,omitempty"`
		Result            *tool.Result       `json:"result,omitempty"`
		Error             *tool.HandledError `json:"error,omitempty"`
		Content           string             `json:"content,omitempty"`
		StructuredContent json.RawMessage    `json:"structured_content,omitempty"`
		IsError           bool               `json:"is_error,omitempty"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return err
	}
	if raw.Result != nil || raw.Error != nil {
		if (raw.Result == nil) == (raw.Error == nil) {
			return fmt.Errorf("event: tool response must contain exactly one outcome")
		}
		*tr = ToolResponse{ToolCallID: raw.ToolCallID, Name: raw.Name, Result: raw.Result, Error: raw.Error}
		return nil
	}
	if raw.ToolCallID == "" && raw.Name == "" && raw.Content == "" && len(raw.StructuredContent) == 0 && !raw.IsError {
		*tr = ToolResponse{}
		return nil
	}
	if raw.IsError {
		*tr = ToolResponse{
			ToolCallID: raw.ToolCallID,
			Name:       raw.Name,
			Error:      &tool.HandledError{Content: raw.Content, StructuredContent: raw.StructuredContent},
		}
		return nil
	}
	*tr = ToolResponse{
		ToolCallID: raw.ToolCallID,
		Name:       raw.Name,
		Result:     &tool.Result{Content: raw.Content, StructuredContent: raw.StructuredContent},
	}
	return nil
}

// UsageDetails serializes model.TokenUsageDetails to/from JSON for database storage.
type UsageDetails model.TokenUsageDetails

// Value implements driver.Valuer so UsageDetails can be written as JSON.
func (d UsageDetails) Value() (driver.Value, error) {
	if d.isZero() {
		return "", nil
	}
	details := model.TokenUsageDetails(d)
	b, err := json.Marshal(details)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner so UsageDetails can be read from a JSON string.
func (d *UsageDetails) Scan(src any) error {
	if src == nil {
		*d = UsageDetails{}
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("event: unsupported UsageDetails source type: %T", src)
	}
	if s == "" {
		*d = UsageDetails{}
		return nil
	}
	var details model.TokenUsageDetails
	if err := json.Unmarshal([]byte(s), &details); err != nil {
		return err
	}
	*d = UsageDetails(details)
	return nil
}

func (d UsageDetails) toModel() *model.TokenUsageDetails {
	if d.isZero() {
		return nil
	}
	details := model.TokenUsageDetails(d)
	return &details
}

func (d UsageDetails) isZero() bool {
	return model.TokenUsageDetails(d).IsZero()
}

// Event represents a persisted conversation event.
type Event struct {
	EventID          int64        `json:"event_id" db:"event_id"`
	SessionID        string       `json:"session_id" db:"session_id"`
	TurnID           string       `json:"turn_id" db:"turn_id"`
	Author           string       `json:"author" db:"author"`
	Role             string       `json:"role" db:"role"`
	Content          string       `json:"content" db:"text"`
	Parts            Parts        `json:"parts" db:"parts"`
	ReasoningContent string       `json:"reasoning_content" db:"reasoning_text"`
	ToolCalls        ToolCalls    `json:"tool_calls" db:"tool_calls"`
	ToolResponse     ToolResponse `json:"tool_response" db:"tool_result"`
	ToolCallID       string       `json:"tool_call_id" db:"tool_call_id"`
	FinishReason     string       `json:"finish_reason" db:"finish_reason"`
	PromptTokens     int64        `json:"prompt_tokens" db:"prompt_tokens"`
	CompletionTokens int64        `json:"completion_tokens" db:"completion_tokens"`
	TotalTokens      int64        `json:"total_tokens" db:"total_tokens"`
	UsageDetails     UsageDetails `json:"usage_details" db:"usage_details"`
	CreatedAt        int64        `json:"created_at" db:"created_at"`
	UpdatedAt        int64        `json:"updated_at" db:"updated_at"`
	// ArchivedAt is set when the event has been archived. A non-zero value means
	// the event is no longer part of the active history.
	ArchivedAt int64 `json:"archived_at" db:"archived_at"`
	DeletedAt  int64 `json:"deleted_at" db:"deleted_at"`
}

// ToModel converts a persisted Event to a model.Event.
func (e *Event) ToModel() model.Event {
	toolCalls := make([]model.ToolCall, len(e.ToolCalls))
	for i, tc := range e.ToolCalls {
		toolCalls[i] = model.ToolCall{
			ID:               tc.ID,
			Name:             tc.Name,
			Arguments:        tc.Arguments,
			ThoughtSignature: tc.ThoughtSignature,
		}
	}
	ev := model.Event{
		ID:        e.EventID,
		SessionID: e.SessionID,
		TurnID:    e.TurnID,
		Author:    e.Author,
		Content: model.Content{
			Role:             model.Role(e.Role),
			Content:          e.Content,
			Parts:            []model.ContentPart(e.Parts),
			ReasoningContent: e.ReasoningContent,
			ToolCalls:        toolCalls,
			ToolCallID:       e.ToolCallID,
		},
		FinishReason: model.FinishReason(e.FinishReason),
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
	}
	usageDetails := e.UsageDetails.toModel()
	if e.PromptTokens != 0 || e.CompletionTokens != 0 || e.TotalTokens != 0 || usageDetails != nil {
		ev.Usage = &model.TokenUsage{
			PromptTokens:     e.PromptTokens,
			CompletionTokens: e.CompletionTokens,
			TotalTokens:      e.TotalTokens,
			Details:          usageDetails,
		}
	}
	if e.ToolResponse.Result != nil || e.ToolResponse.Error != nil {
		var outcome tool.Outcome
		if e.ToolResponse.Result != nil {
			outcome = e.ToolResponse.Result.Clone()
		} else {
			outcome = e.ToolResponse.Error.Clone()
		}
		ev.Content.ToolResponse = &model.ToolResponse{
			ToolCallID: e.ToolResponse.ToolCallID,
			Name:       e.ToolResponse.Name,
			Outcome:    outcome,
		}
	} else if ev.Content.Role == model.RoleTool && (e.ToolCallID != "" || e.Content != "") {
		ev.Content.ToolResponse = &model.ToolResponse{
			ToolCallID: e.ToolCallID,
			Outcome:    &tool.Result{Content: e.Content},
		}
	}
	return ev
}

// FromModel creates an Event from a model.Event, ready for persistence.
// EventID and timestamp fields must be set by the caller before saving.
func FromModel(e model.Event) *Event {
	toolCalls := make(ToolCalls, len(e.Content.ToolCalls))
	for i, tc := range e.Content.ToolCalls {
		toolCalls[i] = ToolCall{
			ID:               tc.ID,
			Name:             tc.Name,
			Arguments:        tc.Arguments,
			ThoughtSignature: tc.ThoughtSignature,
		}
	}
	var toolResponse ToolResponse
	toolCallID := e.Content.ToolCallID
	if e.Content.ToolResponse != nil {
		toolResponse = ToolResponse{
			ToolCallID: e.Content.ToolResponse.ToolCallID,
			Name:       e.Content.ToolResponse.Name,
		}
		switch outcome := e.Content.ToolResponse.Outcome.(type) {
		case *tool.Result:
			toolResponse.Result = outcome.Clone()
		case *tool.HandledError:
			toolResponse.Error = outcome.Clone()
		}
		if toolCallID == "" {
			toolCallID = e.Content.ToolResponse.ToolCallID
		}
	} else if e.Content.Role == model.RoleTool && (e.Content.ToolCallID != "" || e.Content.Content != "") {
		toolResponse = ToolResponse{
			ToolCallID: e.Content.ToolCallID,
			Result:     &tool.Result{Content: e.Content.Content},
		}
	}
	ev := &Event{
		EventID:          e.ID,
		SessionID:        e.SessionID,
		TurnID:           e.TurnID,
		Author:           e.Author,
		Role:             string(e.Content.Role),
		Content:          e.Content.Content,
		Parts:            Parts(e.Content.Parts),
		ReasoningContent: e.Content.ReasoningContent,
		ToolCalls:        toolCalls,
		ToolResponse:     toolResponse,
		ToolCallID:       toolCallID,
		FinishReason:     string(e.FinishReason),
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
	if e.Usage != nil {
		ev.PromptTokens = e.Usage.PromptTokens
		ev.CompletionTokens = e.Usage.CompletionTokens
		ev.TotalTokens = e.Usage.TotalTokens
		if e.Usage.Details != nil {
			ev.UsageDetails = UsageDetails(*e.Usage.Details)
		}
	}
	return ev
}
