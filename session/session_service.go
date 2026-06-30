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

// RunLockKey identifies the conversation turn protected by a run lock.
type RunLockKey struct {
	// AppID identifies the application or tenant that owns the session.
	AppID string
	// UserID identifies the end user that owns the session.
	UserID string
	// SessionID is the application-provided session identifier.
	SessionID string
}

// RunScopedLocker is an optional SessionService capability used by Runner to
// serialize complete turns for the same app/user/session identity.
// Implementations should allow different identities to proceed concurrently
// and honor context cancellation while waiting.
type RunScopedLocker interface {
	LockRun(ctx context.Context, key RunLockKey) (unlock func(), err error)
}
