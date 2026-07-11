package session

import (
	"context"
	"fmt"
)

const defaultListSessionsLimit int64 = 50

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

// SessionSortField identifies a supported field for ordering listed sessions.
type SessionSortField string

const (
	// SessionSortByCreatedAt orders sessions by creation time.
	SessionSortByCreatedAt SessionSortField = "created_at"
	// SessionSortBySessionID orders sessions by their application-provided IDs.
	SessionSortBySessionID SessionSortField = "session_id"
)

// SortOrder identifies the direction used to order listed sessions.
type SortOrder string

const (
	// SortAscending orders values from smallest to largest.
	SortAscending SortOrder = "asc"
	// SortDescending orders values from largest to smallest.
	SortDescending SortOrder = "desc"
)

// ListSessionsRequest filters, paginates, and orders sessions.
type ListSessionsRequest struct {
	// AppID restricts results to sessions owned by this application or tenant.
	AppID string
	// UserID restricts results to sessions owned by this end user.
	UserID string
	// Limit is the maximum number of sessions to return. Zero uses the default of 50.
	Limit int64
	// Offset is the number of ordered sessions to skip.
	Offset int64
	// SortBy is the primary sort field. Empty defaults to SessionSortByCreatedAt.
	SortBy SessionSortField
	// SortOrder is the sort direction. Empty defaults to SortDescending.
	SortOrder SortOrder
}

// Normalize validates the request and fills its documented defaults. Custom
// SessionService implementations should call Normalize before listing.
func (r ListSessionsRequest) Normalize() (ListSessionsRequest, error) {
	if r.Limit < 0 {
		return ListSessionsRequest{}, fmt.Errorf("session: list sessions: limit must not be negative")
	}
	if r.Offset < 0 {
		return ListSessionsRequest{}, fmt.Errorf("session: list sessions: offset must not be negative")
	}
	if r.Limit == 0 {
		r.Limit = defaultListSessionsLimit
	}
	if r.SortBy == "" {
		r.SortBy = SessionSortByCreatedAt
	}
	switch r.SortBy {
	case SessionSortByCreatedAt, SessionSortBySessionID:
	default:
		return ListSessionsRequest{}, fmt.Errorf("session: list sessions: unsupported sort field %q", r.SortBy)
	}
	if r.SortOrder == "" {
		r.SortOrder = SortDescending
	}
	switch r.SortOrder {
	case SortAscending, SortDescending:
	default:
		return ListSessionsRequest{}, fmt.Errorf("session: list sessions: unsupported sort order %q", r.SortOrder)
	}
	return r, nil
}

// SessionService creates, deletes, lists, and retrieves sessions by ID.
type SessionService interface {
	CreateSession(ctx context.Context, req CreateSessionRequest) (Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	GetSession(ctx context.Context, sessionID string) (Session, error)
	ListSessions(ctx context.Context, req ListSessionsRequest) ([]Session, error)
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
