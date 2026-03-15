package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/message"
)

const (
	createSessionExpr = "INSERT INTO sessions (session_id, created_at, updated_at, deleted_at) VALUES ($1, $2, $3, $4)"
	// Only active (non-deleted, non-compacted) messages are inserted with compacted_at = 0.
	createMessageExpr         = "INSERT INTO messages (message_id, role, name, content, reasoning_content, tool_calls, tool_call_id, prompt_tokens, completion_tokens, total_tokens, created_at, updated_at, compacted_at, deleted_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, 0, 0)"
	deleteMessageExpr         = "DELETE FROM messages WHERE message_id = $1 AND deleted_at = 0"
	getMessagesExpr           = "SELECT * FROM messages WHERE deleted_at = 0 AND compacted_at = 0 ORDER BY created_at ASC LIMIT $1 OFFSET $2"
	listMessagesExpr          = "SELECT * FROM messages WHERE deleted_at = 0 AND compacted_at = 0 ORDER BY created_at ASC"
	listCompactedMessagesExpr = "SELECT * FROM messages WHERE compacted_at > 0 AND deleted_at = 0 ORDER BY created_at ASC"
	// compactActiveMessagesExpr sets compacted_at on all currently active messages.
	compactActiveMessagesExpr = "UPDATE messages SET compacted_at = $1 WHERE deleted_at = 0 AND compacted_at = 0"
)

type databaseSession struct {
	db        *sqlx.DB `json:"-"`
	SessionID int64    `json:"session_id" db:"session_id"`
	CreatedAt int64    `json:"created_at" db:"created_at"`
	UpdatedAt int64    `json:"updated_at" db:"updated_at"`
	DeletedAt int64    `json:"deleted_at" db:"deleted_at"`
}

func NewDatabaseSession(ctx context.Context, db *sqlx.DB, sessionID int64) (session.Session, error) {
	session := &databaseSession{db: db, SessionID: sessionID, CreatedAt: time.Now().UnixMilli()}
	_, err := db.ExecContext(ctx, createSessionExpr, session.SessionID, session.CreatedAt, session.UpdatedAt, session.DeletedAt)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *databaseSession) GetSessionID() int64 {
	return s.SessionID
}
func (s *databaseSession) CreateMessage(ctx context.Context, message *message.Message) error {
	_, err := s.db.ExecContext(
		ctx,
		createMessageExpr,
		message.MessageID,
		message.Role,
		message.Name,
		message.Content,
		message.ReasoningContent,
		message.ToolCalls,
		message.ToolCallID,
		message.PromptTokens,
		message.CompletionTokens,
		message.TotalTokens,
		message.CreatedAt,
	)
	return err
}

func (s *databaseSession) DeleteMessage(ctx context.Context, messageID int64) error {
	_, err := s.db.ExecContext(ctx, deleteMessageExpr, messageID, 0)
	return err
}

func (s *databaseSession) GetMessages(ctx context.Context, limit, offset int64) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, getMessagesExpr, limit, offset)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) ListMessages(ctx context.Context) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, listMessagesExpr)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) ListCompactedMessages(ctx context.Context) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, listCompactedMessagesExpr)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) CompactMessages(ctx context.Context, compactor func(context.Context, []*message.Message) (*message.Message, error)) error {
	// Fetch all currently active messages to pass to the compactor.
	active := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &active, listMessagesExpr)
	if err != nil {
		return err
	}

	summary, err := compactor(ctx, active)
	if err != nil {
		return err
	}

	now := time.Now()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Archive active messages by setting compacted_at.
	_, err = tx.ExecContext(ctx, compactActiveMessagesExpr, now.UnixMilli())
	if err != nil {
		return err
	}

	// Insert the summary as a new active message.
	_, err = tx.ExecContext(
		ctx,
		createMessageExpr,
		summary.MessageID,
		summary.Role,
		summary.Name,
		summary.Content,
		summary.ReasoningContent,
		summary.ToolCalls,
		summary.ToolCallID,
		summary.PromptTokens,
		summary.CompletionTokens,
		summary.TotalTokens,
		summary.CreatedAt,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}
