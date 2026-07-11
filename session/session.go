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
	// GetEvents returns a paginated slice of active (non-compacted, non-deleted) events
	// sorted by created_at ASC.
	GetEvents(ctx context.Context, limit, offset int64) ([]*event.Event, error)
	// ListEvents returns all active (non-compacted, non-deleted) events sorted by
	// created_at ASC. Use this instead of GetEvents when the full history is needed.
	ListEvents(ctx context.Context) ([]*event.Event, error)
	DeleteEvent(ctx context.Context, eventID int64) error
	// CompactEvents archives all active events that precede splitEventID and
	// inserts summaryEvent as the new first active event. If splitEventID is 0,
	// all currently active events are archived. The caller is responsible for
	// constructing both the split point and the summary event (e.g. via the
	// compaction package).
	CompactEvents(ctx context.Context, splitEventID int64, summaryEvent *event.Event) error
}
