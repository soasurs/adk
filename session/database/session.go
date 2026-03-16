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
	defaultSessionsTable = "sessions"
	defaultMessagesTable = "messages"
)

// queries holds all pre-built SQL expressions for a given pair of table names.
type queries struct {
	createSession         string
	getSession            string
	deleteSession         string
	createMessage         string
	deleteMessage         string
	getMessages           string
	listMessages          string
	listCompactedMessages string
	compactActiveMessages string
	compactMessagesBefore string
}

// buildQueries constructs SQL expressions using the provided table names.
func buildQueries(sessionsTable, messagesTable string) *queries {
	return &queries{
		createSession: "INSERT INTO " + sessionsTable + " (session_id, created_at, updated_at, deleted_at) VALUES ($1, $2, $3, $4)",
		getSession:    "SELECT * FROM " + sessionsTable + " WHERE session_id = $1 AND deleted_at = $2 LIMIT 1",
		deleteSession: "UPDATE " + sessionsTable + " SET deleted_at = $1 WHERE session_id = $2 AND deleted_at = $3",
		// Only active (non-deleted, non-compacted) messages are inserted with compacted_at = 0.
		createMessage:         "INSERT INTO " + messagesTable + " (message_id, role, name, content, reasoning_content, tool_calls, tool_call_id, prompt_tokens, completion_tokens, total_tokens, created_at, updated_at, compacted_at, deleted_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, 0, 0)",
		deleteMessage:         "DELETE FROM " + messagesTable + " WHERE message_id = $1 AND deleted_at = 0",
		getMessages:           "SELECT * FROM " + messagesTable + " WHERE deleted_at = 0 AND compacted_at = 0 ORDER BY created_at ASC LIMIT $1 OFFSET $2",
		listMessages:          "SELECT * FROM " + messagesTable + " WHERE deleted_at = 0 AND compacted_at = 0 ORDER BY created_at ASC",
		listCompactedMessages: "SELECT * FROM " + messagesTable + " WHERE compacted_at > 0 AND deleted_at = 0 ORDER BY created_at ASC",
		// compactActiveMessages sets compacted_at on all currently active messages.
		compactActiveMessages: "UPDATE " + messagesTable + " SET compacted_at = $1 WHERE deleted_at = 0 AND compacted_at = 0",
		// compactMessagesBefore archives only messages whose message_id is less than
		// the given split point, leaving messages at or after it active.
		compactMessagesBefore: "UPDATE " + messagesTable + " SET compacted_at = $1 WHERE deleted_at = 0 AND compacted_at = 0 AND message_id < $2",
	}
}

// defaultQueries is built from the default table names for backward compatibility.
var defaultQueries = buildQueries(defaultSessionsTable, defaultMessagesTable)

type databaseSession struct {
	db        *sqlx.DB `json:"-"`
	q         *queries `json:"-"`
	SessionID int64    `json:"session_id" db:"session_id"`
	CreatedAt int64    `json:"created_at" db:"created_at"`
	UpdatedAt int64    `json:"updated_at" db:"updated_at"`
	DeletedAt int64    `json:"deleted_at" db:"deleted_at"`
}

// NewDatabaseSession creates a new session in the database using default table names.
// For custom table names, use NewDatabaseSessionService with option functions.
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
	_, err := s.db.ExecContext(
		ctx,
		s.q.createMessage,
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
	_, err := s.db.ExecContext(ctx, s.q.deleteMessage, messageID, 0)
	return err
}

func (s *databaseSession) GetMessages(ctx context.Context, limit, offset int64) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, s.q.getMessages, limit, offset)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) ListMessages(ctx context.Context) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, s.q.listMessages)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) ListCompactedMessages(ctx context.Context) ([]*message.Message, error) {
	messages := make([]*message.Message, 0)
	err := s.db.SelectContext(ctx, &messages, s.q.listCompactedMessages)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *databaseSession) CompactMessages(ctx context.Context, splitMessageID int64, summaryMsg *message.Message) error {
	now := time.Now()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Archive messages before the split point. When splitMessageID is 0, archive all.
	if splitMessageID > 0 {
		_, err = tx.ExecContext(ctx, s.q.compactMessagesBefore, now.UnixMilli(), splitMessageID)
	} else {
		_, err = tx.ExecContext(ctx, s.q.compactActiveMessages, now.UnixMilli())
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
		summaryMsg.Role,
		summaryMsg.Name,
		summaryMsg.Content,
		summaryMsg.ReasoningContent,
		summaryMsg.ToolCalls,
		summaryMsg.ToolCallID,
		summaryMsg.PromptTokens,
		summaryMsg.CompletionTokens,
		summaryMsg.TotalTokens,
		summaryMsg.CreatedAt,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}
