package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"

	"soasurs.dev/soasurs/adk/session"
	"soasurs.dev/soasurs/adk/session/message"
)

const (
	createSessionExpr     = "INSERT INTO sessions (session_id, created_at, updated_at, deleted_at) VALUES ($1, $2, $3, $4)"
	createMessageExpr     = "INSERT INTO messages (message_id, role, content, tool_calls, tool_call_id, created_at, updated_at, deleted_at) VALUES ($1, $2, $3, $4, $5, $6, 0, 0)"
	deleteMessageExpr     = "DELETE FROM messages WHERE message_id = $1 AND deleted_at = $2"
	getMessagesExpr       = "SELECT * FROM messages WHERE deleted_at = $1 ORDER BY created_at ASC LIMIT $2 OFFSET $3"
	getAllMessagesExpr    = "SELECT * FROM messages WHERE deleted_at = $1 ORDER BY created_at ASC"
	deleteAllMessagesExpr = "UPDATE messages SET deleted_at = $1 WHERE deleted_at = $2"
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
		message.Content,
		message.ToolCalls,
		message.ToolCallID,
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
	err := s.db.SelectContext(ctx, &messages, getMessagesExpr, 0, limit, offset)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) CompactMessages(ctx context.Context, compactor func(context.Context, []*message.Message) (*message.Message, error)) error {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, getAllMessagesExpr, 0)
	if err != nil {
		return err
	}

	summary, err := compactor(ctx, messages)
	if err != nil {
		return err
	}

	now := time.Now()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, deleteAllMessagesExpr, now.UnixMilli(), 0)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(
		ctx,
		createMessageExpr,
		summary.MessageID,
		summary.Role,
		summary.Content,
		summary.ToolCalls,
		summary.ToolCallID,
		summary.CreatedAt,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}
