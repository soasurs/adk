package database

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/message"
)

type databaseSession struct {
	db        *sqlx.DB `json:"-"`
	q         *queries `json:"-"`
	SessionID int64    `json:"session_id" db:"session_id"`
	CreatedAt int64    `json:"created_at" db:"created_at"`
	UpdatedAt int64    `json:"updated_at" db:"updated_at"`
	DeletedAt int64    `json:"deleted_at" db:"deleted_at"`
}

// NewDatabaseSession creates a new session in the database using default table names.
//
// Deprecated: prefer NewDatabaseSessionService, which supports custom table
// names via Option functions and integrates with InitSchema. NewDatabaseSession
// is retained for simple single-tenant use cases where the default table names
// are acceptable.
func NewDatabaseSession(ctx context.Context, db *sqlx.DB, sessionID int64) (session.Session, error) {
	return newDatabaseSession(ctx, db, sessionID, defaultQueries)
}

func newDatabaseSession(ctx context.Context, db *sqlx.DB, sessionID int64, q *queries) (session.Session, error) {
	s := &databaseSession{db: db, q: q, SessionID: sessionID, CreatedAt: time.Now().UnixMilli()}
	_, err := db.ExecContext(ctx, q.createSession, s.SessionID, s.CreatedAt, s.UpdatedAt, s.DeletedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *databaseSession) GetSessionID() int64 {
	return s.SessionID
}
func (s *databaseSession) CreateMessage(ctx context.Context, message *message.Message) error {
	message.SessionID = s.SessionID
	_, err := s.db.ExecContext(
		ctx,
		s.q.createMessage,
		message.MessageID,
		message.SessionID,
		message.Role,
		message.Name,
		message.Content,
		message.ReasoningContent,
		message.ToolCalls,
		message.ToolCallID,
		message.Parts,
		message.PromptTokens,
		message.CompletionTokens,
		message.TotalTokens,
		message.CreatedAt,
		message.UpdatedAt,
	)
	return err
}

func (s *databaseSession) DeleteMessage(ctx context.Context, messageID int64) error {
	_, err := s.db.ExecContext(ctx, s.q.deleteMessage, time.Now().UnixMilli(), s.SessionID, messageID)
	return err
}

func (s *databaseSession) GetMessages(ctx context.Context, limit, offset int64) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, s.q.getMessages, s.SessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) ListMessages(ctx context.Context) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, s.q.listMessages, s.SessionID)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) ListCompactedMessages(ctx context.Context) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, s.q.listCompactedMessages, s.SessionID)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) CompactMessages(ctx context.Context, splitMessageID int64, summaryMsg *message.Message) error {
	now := time.Now()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Archive messages before the split point. When splitMessageID is 0, archive all.
	if splitMessageID > 0 {
		_, err = tx.ExecContext(ctx, s.q.compactMessagesBefore, now.UnixMilli(), s.SessionID, splitMessageID)
	} else {
		_, err = tx.ExecContext(ctx, s.q.compactActiveMessages, now.UnixMilli(), s.SessionID)
	}
	if err != nil {
		return err
	}

	// Insert the summary as a new active message. The listMessagesExpr ordering
	// guarantees that system-role messages are returned before conversation messages,
	// so created_at does not need to precede the kept messages.
	_, err = tx.ExecContext(
		ctx,
		s.q.createMessage,
		summaryMsg.MessageID,
		s.SessionID,
		summaryMsg.Role,
		summaryMsg.Name,
		summaryMsg.Content,
		summaryMsg.ReasoningContent,
		summaryMsg.ToolCalls,
		summaryMsg.ToolCallID,
		summaryMsg.Parts,
		summaryMsg.PromptTokens,
		summaryMsg.CompletionTokens,
		summaryMsg.TotalTokens,
		summaryMsg.CreatedAt,
		summaryMsg.UpdatedAt,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}
