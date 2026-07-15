package memory

import (
	"cmp"
	"context"
	"fmt"
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
	turns     map[string]*session.Turn
}

func NewMemorySession(req session.CreateSessionRequest) session.Session {
	now := time.Now().UnixMilli()
	return &memorySession{
		sessionID: req.SessionID,
		appID:     req.AppID,
		userID:    req.UserID,
		createdAt: now,
		events:    make([]*event.Event, 0),
		turns:     make(map[string]*session.Turn),
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

func (s *memorySession) BeginTurn(_ context.Context, turn session.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn.ID == "" {
		return fmt.Errorf("memory: begin turn: turn ID is empty")
	}
	if turn.SessionID != "" && turn.SessionID != s.sessionID {
		return fmt.Errorf("memory: begin turn: session ID %q does not match %q", turn.SessionID, s.sessionID)
	}
	if turn.Status != session.TurnRunning {
		return fmt.Errorf("memory: begin turn: status must be %q", session.TurnRunning)
	}
	if _, exists := s.turns[turn.ID]; exists {
		return fmt.Errorf("memory: begin turn %q: already exists", turn.ID)
	}
	turn.SessionID = s.sessionID
	turn.Reason = ""
	turn.Failure = nil
	turn.FinishedAt = 0
	s.turns[turn.ID] = cloneTurn(&turn)
	return nil
}

func (s *memorySession) FinalizeTurn(_ context.Context, turnID string, outcome session.TurnOutcome) error {
	if err := outcome.Validate(); err != nil {
		return err
	}
	return s.finalizeTurn(turnID, outcome)
}

func (s *memorySession) GetTurn(_ context.Context, turnID string) (*session.Turn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	turn := s.turns[turnID]
	if turn == nil {
		return nil, nil
	}
	return cloneTurn(turn), nil
}

func (s *memorySession) ListTurns(_ context.Context) ([]*session.Turn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	turns := make([]*session.Turn, 0, len(s.turns))
	for _, turn := range s.turns {
		turns = append(turns, cloneTurn(turn))
	}
	slices.SortFunc(turns, func(a, b *session.Turn) int {
		if n := cmp.Compare(a.StartedAt, b.StartedAt); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return turns, nil
}

func (s *memorySession) InterruptRunningTurns(_ context.Context, reason session.TurnReason) error {
	if err := (session.TurnOutcome{Status: session.TurnInterrupted, Reason: reason}).Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	for _, turn := range s.turns {
		if turn.Status == session.TurnRunning {
			turn.Status = session.TurnInterrupted
			turn.Reason = reason
			turn.Failure = nil
			turn.FinishedAt = now
		}
	}
	return nil
}

func (s *memorySession) finalizeTurn(turnID string, outcome session.TurnOutcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.turns[turnID]
	if turn == nil {
		return &session.TurnNotFoundError{TurnID: turnID}
	}
	if turn.Status != session.TurnRunning {
		return &session.TurnStateConflictError{TurnID: turnID, Status: turn.Status}
	}
	turn.Status = outcome.Status
	turn.Reason = outcome.Reason
	turn.Failure = cloneTurnFailure(outcome.Failure)
	turn.FinishedAt = time.Now().UnixMilli()
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

func cloneTurn(turn *session.Turn) *session.Turn {
	if turn == nil {
		return nil
	}
	out := *turn
	out.Failure = cloneTurnFailure(turn.Failure)
	return &out
}

func cloneTurnFailure(failure *session.TurnFailure) *session.TurnFailure {
	if failure == nil {
		return nil
	}
	out := *failure
	return &out
}
