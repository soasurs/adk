package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
)

type MessageStore struct {
	db *Postgres
}

func NewMessageStore(db *Postgres) *MessageStore {
	return &MessageStore{db: db}
}

func (m *MessageStore) SaveMessage(ctx context.Context, msg *storage.Message) error {
	query := `
		INSERT INTO messages (id, session_id, role, content, tool_calls, token_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	var toolCallsJSON []byte
	if len(msg.ToolCalls) > 0 {
		toolCallsJSON = marshalJSON(msg.ToolCalls)
	}

	_, err := m.db.DB().Exec(ctx, query,
		msg.ID,
		msg.SessionID,
		msg.Role,
		msg.Content,
		toolCallsJSON,
		msg.TokenCount,
		msg.CreatedAt,
	)
	return err
}

func (m *MessageStore) GetConversation(ctx context.Context, sessionID uuid.UUID, limit int) ([]storage.Message, error) {
	query := `
		SELECT id, session_id, role, content, tool_calls, token_count, created_at
		FROM messages
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := m.db.DB().Query(ctx, query, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []storage.Message
	for rows.Next() {
		msg := storage.Message{}
		var toolCallsJSON []byte
		err := rows.Scan(
			&msg.ID,
			&msg.SessionID,
			&msg.Role,
			&msg.Content,
			&toolCallsJSON,
			&msg.TokenCount,
			&msg.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if len(toolCallsJSON) > 0 {
			var toolCalls []storage.ToolCall
			json.Unmarshal(toolCallsJSON, &toolCalls)
			msg.ToolCalls = toolCalls
		}

		messages = append(messages, msg)
	}

	// Reverse to get chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

func (m *MessageStore) GetMessage(ctx context.Context, id uuid.UUID) (*storage.Message, error) {
	query := `
		SELECT id, session_id, role, content, tool_calls, token_count, created_at
		FROM messages
		WHERE id = $1
	`
	msg := &storage.Message{}
	var toolCallsJSON []byte
	err := m.db.DB().QueryRow(ctx, query, id).Scan(
		&msg.ID,
		&msg.SessionID,
		&msg.Role,
		&msg.Content,
		&toolCallsJSON,
		&msg.TokenCount,
		&msg.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(toolCallsJSON) > 0 {
		var toolCalls []storage.ToolCall
		json.Unmarshal(toolCallsJSON, &toolCalls)
		msg.ToolCalls = toolCalls
	}

	return msg, nil
}
