package session

import (
	"context"

	"github.com/soasurs/adk/session/event"
)

// Session stores the durable event ledger and ownership metadata for one
// conversation thread.
type Session interface {
	// GetSessionID returns the application-provided session identifier.
	GetSessionID() string
	// GetAppID returns the application or tenant that owns the session.
	GetAppID() string
	// GetUserID returns the end user that owns the session.
	GetUserID() string
	// GetCreatedAt returns the Unix millisecond timestamp when the session was created.
	GetCreatedAt() int64
	CreateEvent(ctx context.Context, event *event.Event) error
	// GetEvents returns a paginated slice of active (non-archived, non-deleted) events
	// sorted by created_at ASC.
	GetEvents(ctx context.Context, limit, offset int64) ([]*event.Event, error)
	// ListEvents returns all active (non-archived, non-deleted) events sorted by
	// created_at ASC. Use this instead of GetEvents when the full history is needed.
	ListEvents(ctx context.Context) ([]*event.Event, error)
	// ListTurns returns all active events grouped by TurnID. Turns are ordered
	// by the earliest event's created_at ASC. Within each turn, events follow
	// the same ordering as ListEvents. Turns with an empty TurnID are grouped
	// together as a single turn.
	ListTurns(ctx context.Context) ([]*Turn, error)
	DeleteEvent(ctx context.Context, eventID int64) error
	// ListArchivedEvents returns all archived, non-deleted events sorted by
	// created_at ASC with event_id as a stable tiebreaker.
	ListArchivedEvents(ctx context.Context) ([]*event.Event, error)
	// ListArchivedTurns is like ListTurns but returns archived events grouped
	// by TurnID.
	ListArchivedTurns(ctx context.Context) ([]*Turn, error)
	// ArchiveEventsBefore archives all active events that precede eventID in
	// conversation order. eventID remains active. Zero archives all active
	// events. A non-zero eventID must identify an active event in this session.
	// Archiving is idempotent and never creates or deletes events.
	ArchiveEventsBefore(ctx context.Context, eventID int64) error
}
