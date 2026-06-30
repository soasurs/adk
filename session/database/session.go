package database

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/event"
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
func (s *databaseSession) CreateEvent(ctx context.Context, ev *event.Event) error {
	ev.SessionID = s.SessionID
	_, err := s.db.ExecContext(
		ctx,
		s.q.createEvent,
		ev.EventID,
		ev.SessionID,
		ev.Author,
		ev.Role,
		ev.Content,
		ev.ReasoningContent,
		ev.ToolCalls,
		ev.ToolCallID,
		ev.FinishReason,
		ev.Parts,
		ev.PromptTokens,
		ev.CompletionTokens,
		ev.TotalTokens,
		ev.CreatedAt,
		ev.UpdatedAt,
	)
	return err
}

func (s *databaseSession) DeleteEvent(ctx context.Context, eventID int64) error {
	_, err := s.db.ExecContext(ctx, s.q.deleteEvent, time.Now().UnixMilli(), s.SessionID, eventID)
	return err
}

func (s *databaseSession) GetEvents(ctx context.Context, limit, offset int64) ([]*event.Event, error) {
	events := make([]*event.Event, 0)
	err := s.db.SelectContext(ctx, &events, s.q.getEvents, s.SessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *databaseSession) ListEvents(ctx context.Context) ([]*event.Event, error) {
	events := make([]*event.Event, 0)
	err := s.db.SelectContext(ctx, &events, s.q.listEvents, s.SessionID)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *databaseSession) ListCompactedEvents(ctx context.Context) ([]*event.Event, error) {
	events := make([]*event.Event, 0)
	err := s.db.SelectContext(ctx, &events, s.q.listCompactedEvents, s.SessionID)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *databaseSession) CompactEvents(ctx context.Context, splitEventID int64, summaryEvent *event.Event) error {
	now := time.Now()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Archive events before the split point. When splitEventID is 0, archive all.
	if splitEventID > 0 {
		_, err = tx.ExecContext(ctx, s.q.compactEventsBefore, now.UnixMilli(), s.SessionID, splitEventID)
	} else {
		_, err = tx.ExecContext(ctx, s.q.compactActiveEvents, now.UnixMilli(), s.SessionID)
	}
	if err != nil {
		return err
	}

	// Insert the summary as a new active event.
	_, err = tx.ExecContext(
		ctx,
		s.q.createEvent,
		summaryEvent.EventID,
		s.SessionID,
		summaryEvent.Author,
		summaryEvent.Role,
		summaryEvent.Content,
		summaryEvent.ReasoningContent,
		summaryEvent.ToolCalls,
		summaryEvent.ToolCallID,
		summaryEvent.FinishReason,
		summaryEvent.Parts,
		summaryEvent.PromptTokens,
		summaryEvent.CompletionTokens,
		summaryEvent.TotalTokens,
		summaryEvent.CreatedAt,
		summaryEvent.UpdatedAt,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}
