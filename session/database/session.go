package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

type databaseTurn struct {
	TurnID         string                   `db:"turn_id"`
	SessionID      string                   `db:"session_id"`
	Status         session.TurnStatus       `db:"status"`
	Reason         session.TurnReason       `db:"reason"`
	FailureCode    string                   `db:"failure_code"`
	FailureMessage string                   `db:"failure_message"`
	FailureStage   session.TurnFailureStage `db:"failure_stage"`
	StartedAt      int64                    `db:"started_at"`
	FinishedAt     int64                    `db:"finished_at"`
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

func (s *databaseSession) ListArchivedEvents(ctx context.Context) ([]*event.Event, error) {
	events := make([]*event.Event, 0)
	err := s.db.SelectContext(ctx, &events, s.q.listArchivedEvents, s.SessionID)
	if err != nil {
		return nil, err
	}
	return events, nil
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

func (s *databaseSession) BeginTurn(ctx context.Context, turn session.Turn) error {
	if turn.ID == "" {
		return fmt.Errorf("database: begin turn: turn ID is empty")
	}
	if turn.SessionID != "" && turn.SessionID != s.SessionID {
		return fmt.Errorf("database: begin turn: session ID %q does not match %q", turn.SessionID, s.SessionID)
	}
	if turn.Status != session.TurnRunning {
		return fmt.Errorf("database: begin turn: status must be %q", session.TurnRunning)
	}
	_, err := s.db.ExecContext(
		ctx,
		s.q.createTurn,
		s.SessionID,
		turn.ID,
		turn.Status,
		"",
		turn.StartedAt,
		0,
	)
	return err
}

func (s *databaseSession) FinalizeTurn(ctx context.Context, turnID string, outcome session.TurnOutcome) error {
	if err := outcome.Validate(); err != nil {
		return err
	}
	return s.finalizeTurn(ctx, turnID, outcome)
}

func (s *databaseSession) GetTurn(ctx context.Context, turnID string) (*session.Turn, error) {
	turn := new(databaseTurn)
	err := s.db.GetContext(ctx, turn, s.q.getTurn, s.SessionID, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return turn.toSession(), nil
}

func (s *databaseSession) ListTurns(ctx context.Context) ([]*session.Turn, error) {
	stored := make([]*databaseTurn, 0)
	if err := s.db.SelectContext(ctx, &stored, s.q.listTurns, s.SessionID); err != nil {
		return nil, err
	}
	turns := make([]*session.Turn, 0, len(stored))
	for _, turn := range stored {
		turns = append(turns, turn.toSession())
	}
	return turns, nil
}

func (s *databaseSession) InterruptRunningTurns(ctx context.Context, reason session.TurnReason) error {
	if err := (session.TurnOutcome{Status: session.TurnInterrupted, Reason: reason}).Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, s.q.interruptRunning, reason, time.Now().UnixMilli(), s.SessionID)
	return err
}

func (s *databaseSession) finalizeTurn(
	ctx context.Context,
	turnID string,
	outcome session.TurnOutcome,
) error {
	var failure session.TurnFailure
	if outcome.Failure != nil {
		failure = *outcome.Failure
	}
	result, err := s.db.ExecContext(
		ctx,
		s.q.finalizeTurn,
		outcome.Status,
		outcome.Reason,
		failure.Code,
		failure.Message,
		failure.Stage,
		time.Now().UnixMilli(),
		s.SessionID,
		turnID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	turn, err := s.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if turn == nil {
		return &session.TurnNotFoundError{TurnID: turnID}
	}
	return &session.TurnStateConflictError{TurnID: turnID, Status: turn.Status}
}

func (t *databaseTurn) toSession() *session.Turn {
	turn := &session.Turn{
		ID:         t.TurnID,
		SessionID:  t.SessionID,
		Status:     t.Status,
		Reason:     t.Reason,
		StartedAt:  t.StartedAt,
		FinishedAt: t.FinishedAt,
	}
	if t.FailureCode != "" || t.FailureMessage != "" || t.FailureStage != "" {
		turn.Failure = &session.TurnFailure{
			Code:    t.FailureCode,
			Message: t.FailureMessage,
			Stage:   t.FailureStage,
		}
	}
	return turn
}
