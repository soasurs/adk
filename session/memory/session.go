package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/event"
)

type memorySession struct {
	mu        sync.RWMutex
	sessionID string
	appID     string
	userID    string
	createdAt int64
	events    []*event.Event // all events: active (archived_at=0, deleted_at=0) + archived
}

func NewMemorySession(req session.CreateSessionRequest) session.Session {
	now := time.Now().UnixMilli()
	return &memorySession{
		sessionID: req.SessionID,
		appID:     req.AppID,
		userID:    req.UserID,
		createdAt: now,
		events:    make([]*event.Event, 0),
	}
}

func (s *memorySession) GetSessionID() string {
	return s.sessionID
}

func (s *memorySession) GetAppID() string {
	return s.appID
}

func (s *memorySession) GetUserID() string {
	return s.userID
}

func (s *memorySession) GetCreatedAt() int64 {
	return s.createdAt
}

func (s *memorySession) CreateEvent(ctx context.Context, ev *event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, cloneEvent(ev))
	return nil
}

func (s *memorySession) DeleteEvent(ctx context.Context, eventID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	for _, ev := range s.events {
		if ev.EventID == eventID && ev.DeletedAt == 0 {
			ev.DeletedAt = now
			break
		}
	}
	return nil
}

func (s *memorySession) GetEvents(ctx context.Context, limit, offset int64) ([]*event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	active := s.activeEvents()
	if offset >= int64(len(active)) {
		return []*event.Event{}, nil
	}
	end := min(offset+limit, int64(len(active)))
	return active[offset:end], nil
}

func (s *memorySession) ListEvents(ctx context.Context) ([]*event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeEvents(), nil
}

func (s *memorySession) ListArchivedEvents(ctx context.Context) ([]*event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*event.Event, 0)
	for _, ev := range s.events {
		if ev.ArchivedAt > 0 && ev.DeletedAt == 0 {
			out = append(out, cloneEvent(ev))
		}
	}
	sortEvents(out)
	return out, nil
}

func (s *memorySession) ArchiveEventsBefore(ctx context.Context, eventID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()

	active := s.activeEvents()
	archiveEnd := len(active)
	if eventID != 0 {
		archiveEnd = -1
		for i, ev := range active {
			if ev.EventID == eventID {
				archiveEnd = i
				break
			}
		}
		if archiveEnd < 0 {
			return &session.ArchiveBoundaryNotFoundError{EventID: eventID}
		}
	}

	for _, activeEvent := range active[:archiveEnd] {
		for _, stored := range s.events {
			if stored.EventID == activeEvent.EventID {
				stored.ArchivedAt = now
				break
			}
		}
	}
	return nil
}

// activeEvents returns all events with ArchivedAt == 0 and DeletedAt == 0,
// preserving insertion order. Must be called with s.mu held (read or write).
func (s *memorySession) activeEvents() []*event.Event {
	out := make([]*event.Event, 0, len(s.events))
	for _, ev := range s.events {
		if ev.ArchivedAt == 0 && ev.DeletedAt == 0 {
			out = append(out, cloneEvent(ev))
		}
	}
	sortEvents(out)
	return out
}

func sortEvents(events []*event.Event) {
	slices.SortFunc(events, func(a, b *event.Event) int {
		if n := cmp.Compare(a.CreatedAt, b.CreatedAt); n != 0 {
			return n
		}
		return cmp.Compare(a.EventID, b.EventID)
	})
}

func cloneEvent(ev *event.Event) *event.Event {
	if ev == nil {
		return nil
	}
	out := *ev
	out.Parts = append(event.Parts(nil), ev.Parts...)
	out.ToolCalls = append(event.ToolCalls(nil), ev.ToolCalls...)
	return &out
}
