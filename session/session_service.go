package session

import "context"

// CreateSessionRequest describes the session identity and ownership metadata
// recorded when a session is created.
type CreateSessionRequest struct {
	// SessionID is the application-provided session identifier. It must be unique
	// in the session store.
	SessionID string
	// AppID identifies the application or tenant that owns the session.
	AppID string
	// UserID identifies the end user that owns the session.
	UserID string
}

// SessionService creates, deletes, and retrieves sessions by ID.
type SessionService interface {
	CreateSession(ctx context.Context, req CreateSessionRequest) (Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	GetSession(ctx context.Context, sessionID string) (Session, error)
}

// RunLocker is an optional SessionService capability used by Runner to
// serialize complete turns for the same session. Implementations should allow
// different session IDs to proceed concurrently and honor context cancellation
// while waiting.
type RunLocker interface {
	LockSession(ctx context.Context, sessionID string) (unlock func(), err error)
}
