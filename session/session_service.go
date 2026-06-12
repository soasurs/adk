package session

import "context"

type SessionService interface {
	CreateSession(ctx context.Context, sessionID int64) (Session, error)
	DeleteSession(ctx context.Context, sessionID int64) error
	GetSession(ctx context.Context, sessionID int64) (Session, error)
}

// RunLocker is an optional SessionService capability used by Runner to
// serialize complete turns for the same session. Implementations should allow
// different session IDs to proceed concurrently and honor context cancellation
// while waiting.
type RunLocker interface {
	LockSession(ctx context.Context, sessionID int64) (unlock func(), err error)
}
