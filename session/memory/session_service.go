package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/soasurs/adk/internal/sessionlock"
	"github.com/soasurs/adk/session"
)

type memorySessionService struct {
	mu       sync.RWMutex
	sessions []session.Session
	runLocks *sessionlock.Locker[session.RunLockKey]
}

func NewMemorySessionService() session.SessionService {
	return &memorySessionService{
		sessions: make([]session.Session, 0),
		runLocks: sessionlock.New[session.RunLockKey](),
	}
}

func (ss *memorySessionService) LockRun(ctx context.Context, key session.RunLockKey) (func(), error) {
	return ss.runLocks.Lock(ctx, key)
}

func (ss *memorySessionService) CreateSession(ctx context.Context, req session.CreateSessionRequest) (session.Session, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	session := NewMemorySession(req)
	ss.sessions = append(ss.sessions, session)
	return session, nil
}

func (ss *memorySessionService) DeleteSession(ctx context.Context, sessionID string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for i := 0; i < len(ss.sessions); i++ {
		if ss.sessions[i].GetSessionID() == sessionID {
			ss.sessions = slices.Delete(ss.sessions, i, i+1)
			break
		}
	}
	return nil
}

func (ss *memorySessionService) GetSession(ctx context.Context, sessionID string) (session.Session, error) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	i := slices.IndexFunc(ss.sessions, func(e session.Session) bool { return e.GetSessionID() == sessionID })
	if i == -1 {
		return nil, nil
	}
	return ss.sessions[i], nil
}

func (ss *memorySessionService) ListSessions(ctx context.Context, req session.ListSessionsRequest) ([]session.Session, error) {
	req, err := req.Normalize()
	if err != nil {
		return nil, err
	}

	ss.mu.RLock()
	sessions := make([]session.Session, 0, len(ss.sessions))
	for _, sess := range ss.sessions {
		if sess.GetAppID() == req.AppID && sess.GetUserID() == req.UserID {
			sessions = append(sessions, sess)
		}
	}
	ss.mu.RUnlock()

	slices.SortFunc(sessions, func(a, b session.Session) int {
		var n int
		switch req.SortBy {
		case session.SessionSortByCreatedAt:
			n = cmp.Compare(a.GetCreatedAt(), b.GetCreatedAt())
		case session.SessionSortBySessionID:
			n = cmp.Compare(a.GetSessionID(), b.GetSessionID())
		}
		if n == 0 {
			n = cmp.Compare(a.GetSessionID(), b.GetSessionID())
			return n
		}
		if req.SortOrder == session.SortDescending {
			return -n
		}
		return n
	})

	if req.Offset >= int64(len(sessions)) {
		return []session.Session{}, nil
	}
	end := int64(len(sessions))
	if req.Limit < end-req.Offset {
		end = req.Offset + req.Limit
	}
	return sessions[req.Offset:end], nil
}
