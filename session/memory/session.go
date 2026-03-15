package memory

import (
	"context"
	"slices"
	"time"

	"soasurs.dev/soasurs/adk/session"
	"soasurs.dev/soasurs/adk/session/message"
)

type memorySession struct {
	sessionID         int64
	messages          []*message.Message // active messages
	compactedMessages []*message.Message // archived by CompactMessages
}

func NewMemorySession(sessionID int64) session.Session {
	return &memorySession{
		sessionID:         sessionID,
		messages:          make([]*message.Message, 0),
		compactedMessages: make([]*message.Message, 0),
	}
}

func (s *memorySession) GetSessionID() int64 {
	return s.sessionID
}

func (s *memorySession) CreateMessage(ctx context.Context, message *message.Message) error {
	s.messages = append(s.messages, message)
	return nil
}

func (s *memorySession) DeleteMessage(ctx context.Context, messageID int64) error {
	for i := 0; i < len(s.messages); i++ {
		if s.messages[i].MessageID == messageID {
			s.messages = slices.Delete(s.messages, i, i+1)
			break
		}
	}
	return nil
}

func (s *memorySession) GetMessages(ctx context.Context, limit, offset int64) ([]*message.Message, error) {
	if offset >= int64(len(s.messages)) {
		return []*message.Message{}, nil
	}

	end := offset + limit
	if end > int64(len(s.messages)) {
		end = int64(len(s.messages))
	}

	return s.messages[offset:end], nil
}

func (s *memorySession) ListMessages(ctx context.Context) ([]*message.Message, error) {
	out := make([]*message.Message, len(s.messages))
	copy(out, s.messages)
	return out, nil
}

func (s *memorySession) ListCompactedMessages(ctx context.Context) ([]*message.Message, error) {
	out := make([]*message.Message, len(s.compactedMessages))
	copy(out, s.compactedMessages)
	return out, nil
}

func (s *memorySession) CompactMessages(ctx context.Context, compactor func(context.Context, []*message.Message) (*message.Message, error)) error {
	summary, err := compactor(ctx, s.messages)
	if err != nil {
		return err
	}

	// Archive current active messages with a compaction timestamp.
	now := time.Now().UnixMilli()
	for _, m := range s.messages {
		m.CompactedAt = now
		s.compactedMessages = append(s.compactedMessages, m)
	}

	s.messages = []*message.Message{summary}
	return nil
}
