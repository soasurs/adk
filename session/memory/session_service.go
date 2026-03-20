package memory

import (
	"context"
	"slices"
	"sync"

	"github.com/soasurs/adk/session"
)

type memorySessionService struct {
	mu       sync.RWMutex
	sessions []session.Session
}

func NewMemorySessionService() session.SessionService {
	return &memorySessionService{sessions: make([]session.Session, 0)}
}

func (ss *memorySessionService) CreateSession(ctx context.Context, sessionID int64) (session.Session, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	session := NewMemorySession(sessionID)
	ss.sessions = append(ss.sessions, session)
	return session, nil
}

func (ss *memorySessionService) DeleteSession(ctx context.Context, sessionID int64) error {
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

func (ss *memorySessionService) GetSession(ctx context.Context, sessionID int64) (session.Session, error) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	i := slices.IndexFunc(ss.sessions, func(e session.Session) bool { return e.GetSessionID() == sessionID })
	if i == -1 {
		return nil, nil
	}
	return ss.sessions[i], nil
}
