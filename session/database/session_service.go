package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"

	"soasurs.dev/soasurs/adk/session"
)

const (
	getSessionExpr    = "SELECT * FROM sessions WHERE session_id = $1 AND deleted_at = $2 LIMIT 1"
	deleteSessionExpr = "UPDATE sessions SET deleted_at = $1 WHERE session_id = $2 AND deleted_at = $3"
)

type databaseSessionService struct {
	db *sqlx.DB
}

func NewDatabaseSessionService(db *sqlx.DB) session.SessionService {
	return &databaseSessionService{db: db}
}

func (ss *databaseSessionService) CreateSession(ctx context.Context, sessionID int64) (session.Session, error) {
	return NewDatabaseSession(ctx, ss.db, sessionID)
}

func (ss *databaseSessionService) DeleteSession(ctx context.Context, sessionID int64) error {
	now := time.Now()
	_, err := ss.db.ExecContext(ctx, deleteSessionExpr, now.UnixMilli(), sessionID, 0)
	return err
}

func (ss *databaseSessionService) GetSession(ctx context.Context, sessionID int64) (session.Session, error) {
	s := new(databaseSession)
	err := ss.db.GetContext(ctx, s, getSessionExpr, sessionID, 0)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	s.db = ss.db
	return s, nil
}
