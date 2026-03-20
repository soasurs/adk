package memory

import (
	"context"
	"sync"
	"time"

	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/message"
)

type memorySession struct {
	mu        sync.RWMutex
	sessionID int64
	messages  []*message.Message // all messages: active (compacted_at=0, deleted_at=0) + archived
}

func NewMemorySession(sessionID int64) session.Session {
	return &memorySession{
		sessionID: sessionID,
		messages:  make([]*message.Message, 0),
	}
}

func (s *memorySession) GetSessionID() int64 {
	return s.sessionID
}

func (s *memorySession) CreateMessage(ctx context.Context, msg *message.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	return nil
}

func (s *memorySession) DeleteMessage(ctx context.Context, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	for _, m := range s.messages {
		if m.MessageID == messageID && m.CompactedAt == 0 && m.DeletedAt == 0 {
			m.DeletedAt = now
			break
		}
	}
	return nil
}

func (s *memorySession) GetMessages(ctx context.Context, limit, offset int64) ([]*message.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	active := s.activeMessages()
	if offset >= int64(len(active)) {
		return []*message.Message{}, nil
	}
	end := offset + limit
	if end > int64(len(active)) {
		end = int64(len(active))
	}
	return active[offset:end], nil
}

func (s *memorySession) ListMessages(ctx context.Context) ([]*message.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeMessages(), nil
}

func (s *memorySession) ListCompactedMessages(ctx context.Context) ([]*message.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*message.Message, 0)
	for _, m := range s.messages {
		if m.CompactedAt > 0 && m.DeletedAt == 0 {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *memorySession) CompactMessages(ctx context.Context, splitMessageID int64, summaryMsg *message.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()

	// Determine the index of the first active message to keep.
	active := s.activeMessages()
	splitIdx := len(active) // default: archive all active messages
	if splitMessageID > 0 {
		for i, m := range active {
			if m.MessageID == splitMessageID {
				splitIdx = i
				break
			}
		}
	}

	// Set CompactedAt on active messages before the split point.
	for _, m := range active[:splitIdx] {
		m.CompactedAt = now
	}

	// Append the summary as a new active message.
	s.messages = append(s.messages, summaryMsg)
	return nil
}

// activeMessages returns all messages with CompactedAt == 0 and DeletedAt == 0,
// preserving insertion order. Must be called with s.mu held (read or write).
func (s *memorySession) activeMessages() []*message.Message {
	out := make([]*message.Message, 0, len(s.messages))
	for _, m := range s.messages {
		if m.CompactedAt == 0 && m.DeletedAt == 0 {
			out = append(out, m)
		}
	}
	return out
}
