package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/event"
)

type databaseSession struct {
	db        *sqlx.DB `json:"-"`
	q         *queries `json:"-"`
	SessionID string   `json:"session_id" db:"session_id"`
	AppID     string   `json:"app_id" db:"app_id"`
	UserID    string   `json:"user_id" db:"user_id"`
	CreatedAt int64    `json:"created_at" db:"created_at"`
	DeletedAt int64    `json:"deleted_at" db:"deleted_at"`
}

// NewDatabaseSession creates a new session in the database using default table names.
//
// Deprecated: prefer NewDatabaseSessionService, which supports custom table
// names via Option functions and integrates with InitSchema. NewDatabaseSession
// is retained for simple single-tenant use cases where the default table names
// are acceptable.
func NewDatabaseSession(ctx context.Context, db *sqlx.DB, req session.CreateSessionRequest) (session.Session, error) {
	return newDatabaseSession(ctx, db, req, defaultQueries)
}

func newDatabaseSession(ctx context.Context, db *sqlx.DB, req session.CreateSessionRequest, q *queries) (session.Session, error) {
	now := time.Now().UnixMilli()
	s := &databaseSession{
		db:        db,
		q:         q,
		SessionID: req.SessionID,
		AppID:     req.AppID,
		UserID:    req.UserID,
		CreatedAt: now,
	}
	_, err := db.ExecContext(ctx, q.createSession, s.SessionID, s.AppID, s.UserID, s.CreatedAt, s.DeletedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *databaseSession) GetSessionID() string {
	return s.SessionID
}

func (s *databaseSession) GetAppID() string {
	return s.AppID
}

func (s *databaseSession) GetUserID() string {
	return s.UserID
}

func (s *databaseSession) GetCreatedAt() int64 {
	return s.CreatedAt
}

func (s *databaseSession) CreateEvent(ctx context.Context, ev *event.Event) error {
	ev.SessionID = s.SessionID
	_, err := s.db.ExecContext(
		ctx,
		s.q.createEvent,
		ev.EventID,
		ev.SessionID,
		ev.TurnID,
		ev.Author,
		ev.Role,
		ev.Content,
		ev.ReasoningContent,
		ev.ToolCalls,
		ev.ToolResponse,
		ev.ToolCallID,
		ev.FinishReason,
		ev.Parts,
		ev.PromptTokens,
		ev.CompletionTokens,
		ev.TotalTokens,
		ev.UsageDetails,
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

func (s *databaseSession) ListTurns(ctx context.Context) ([]*session.Turn, error) {
	events, err := s.ListEvents(ctx)
	if err != nil {
		return nil, err
	}
	return session.GroupEventsByTurn(events), nil
}

func (s *databaseSession) ListArchivedEvents(ctx context.Context) ([]*event.Event, error) {
	events := make([]*event.Event, 0)
	err := s.db.SelectContext(ctx, &events, s.q.listArchivedEvents, s.SessionID)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *databaseSession) ListArchivedTurns(ctx context.Context) ([]*session.Turn, error) {
	events, err := s.ListArchivedEvents(ctx)
	if err != nil {
		return nil, err
	}
	return session.GroupEventsByTurn(events), nil
}

func (s *databaseSession) ArchiveEventsBefore(ctx context.Context, eventID int64) error {
	now := time.Now()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if eventID != 0 {
		var createdAt int64
		err = tx.GetContext(ctx, &createdAt, s.q.getArchiveBoundary, s.SessionID, eventID)
		if errors.Is(err, sql.ErrNoRows) {
			return &session.ArchiveBoundaryNotFoundError{EventID: eventID}
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, s.q.archiveEventsBefore, now.UnixMilli(), s.SessionID, createdAt, eventID)
	} else {
		_, err = tx.ExecContext(ctx, s.q.archiveActiveEvents, now.UnixMilli(), s.SessionID)
	}
	if err != nil {
		return err
	}

	return tx.Commit()
}
