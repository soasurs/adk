// Package event defines the persisted session event representation.
package event

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/soasurs/adk/model"
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
	ID        string `json:"id" db:"id"`
	Name      string `json:"name" db:"name"`
	Arguments string `json:"arguments" db:"arguments"`
	// ThoughtSignature is provider-supplied opaque state that must survive
	// history persistence for subsequent Gemini tool-call turns.
	ThoughtSignature []byte `json:"thought_signature,omitempty" db:"thought_signature"`
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

// Event represents a persisted conversation event.
type Event struct {
	EventID          int64     `json:"event_id" db:"event_id"`
	SessionID        int64     `json:"session_id" db:"session_id"`
	Author           string    `json:"author" db:"author"`
	Role             string    `json:"role" db:"role"`
	Content          string    `json:"content" db:"text"`
	Parts            Parts     `json:"parts" db:"parts"`
	ReasoningContent string    `json:"reasoning_content" db:"reasoning_text"`
	ToolCalls        ToolCalls `json:"tool_calls" db:"tool_calls"`
	ToolCallID       string    `json:"tool_call_id" db:"tool_call_id"`
	FinishReason     string    `json:"finish_reason" db:"finish_reason"`
	PromptTokens     int64     `json:"prompt_tokens" db:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens" db:"completion_tokens"`
	TotalTokens      int64     `json:"total_tokens" db:"total_tokens"`
	CreatedAt        int64     `json:"created_at" db:"created_at"`
	UpdatedAt        int64     `json:"updated_at" db:"updated_at"`
	// CompactedAt is set when the event has been archived by a CompactEvents call.
	// A non-zero value means the event is compacted and no longer part of the active history.
	CompactedAt int64 `json:"compacted_at" db:"compacted_at"`
	DeletedAt   int64 `json:"deleted_at" db:"deleted_at"`
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
	if e.PromptTokens != 0 || e.CompletionTokens != 0 || e.TotalTokens != 0 {
		ev.Usage = &model.TokenUsage{
			PromptTokens:     e.PromptTokens,
			CompletionTokens: e.CompletionTokens,
			TotalTokens:      e.TotalTokens,
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
	ev := &Event{
		EventID:          e.ID,
		SessionID:        e.SessionID,
		Author:           e.Author,
		Role:             string(e.Content.Role),
		Content:          e.Content.Content,
		Parts:            Parts(e.Content.Parts),
		ReasoningContent: e.Content.ReasoningContent,
		ToolCalls:        toolCalls,
		ToolCallID:       e.Content.ToolCallID,
		FinishReason:     string(e.FinishReason),
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
	if e.Usage != nil {
		ev.PromptTokens = e.Usage.PromptTokens
		ev.CompletionTokens = e.Usage.CompletionTokens
		ev.TotalTokens = e.Usage.TotalTokens
	}
	return ev
}
