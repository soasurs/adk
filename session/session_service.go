package session

import "context"

type SessionService interface {
	CreateSession(ctx context.Context, sessionID int64) (Session, error)
	DeleteSession(ctx context.Context, sessionID int64) error
	GetSession(ctx context.Context, sessionID int64) (Session, error)
}
