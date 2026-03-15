package message

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"soasurs.dev/soasurs/adk/model"
)

// ToolCall represents a persisted tool call within an assistant message.
type ToolCall struct {
	// ID is the unique identifier of this tool call, matching model.ToolCall.ID.
	ID        string `json:"id" db:"id"`
	Name      string `json:"name" db:"name"`
	Arguments string `json:"arguments" db:"arguments"`
}

// ToolCalls is a slice of ToolCall that serializes to/from JSON for database storage.
type ToolCalls []ToolCall

// Value implements driver.Valuer so ToolCallList can be written to the database as a JSON string.
func (tl ToolCalls) Value() (driver.Value, error) {
	b, err := json.Marshal(tl)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner so ToolCallList can be read from the database from a JSON string.
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
		return fmt.Errorf("message: unsupported ToolCallList source type: %T", src)
	}
	return json.Unmarshal([]byte(s), tl)
}

// Message represents a persisted conversation message.
type Message struct {
	MessageID int64     `json:"message_id" db:"message_id"`
	Role      string    `json:"role" db:"role"`
	Content   string    `json:"content" db:"content"`
	ToolCalls ToolCalls `json:"tool_calls" db:"tool_calls"`
	// ToolCallID links a tool-role message back to the assistant's ToolCall.ID it responds to.
	ToolCallID string `json:"tool_call_id" db:"tool_call_id"`
	CreatedAt  int64  `json:"created_at" db:"created_at"`
	UpdatedAt  int64  `json:"updated_at" db:"updated_at"`
	DeletedAt  int64  `json:"deleted_at" db:"deleted_at"`
}

// ToModel converts a persisted Message to a model.Message for LLM consumption.
func (m *Message) ToModel() model.Message {
	toolCalls := make([]model.ToolCall, len(m.ToolCalls))
	for i, tc := range m.ToolCalls {
		toolCalls[i] = model.ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}
	return model.Message{
		Role:       model.Role(m.Role),
		Content:    m.Content,
		ToolCalls:  toolCalls,
		ToolCallID: m.ToolCallID,
	}
}

// FromModel creates a Message from a model.Message, ready for persistence.
// MessageID and timestamp fields must be set by the caller before saving.
func FromModel(m model.Message) *Message {
	toolCalls := make(ToolCalls, len(m.ToolCalls))
	for i, tc := range m.ToolCalls {
		toolCalls[i] = ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}
	return &Message{
		Role:       string(m.Role),
		Content:    m.Content,
		ToolCalls:  toolCalls,
		ToolCallID: m.ToolCallID,
	}
}
