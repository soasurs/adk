package session

import (
	"context"

	"soasurs.dev/soasurs/adk/session/message"
)

type Session interface {
	GetSessionID() int64
	CreateMessage(ctx context.Context, message *message.Message) error
	// GetMessages returns a paginated slice of active (non-compacted, non-deleted) messages
	// sorted by created_at ASC.
	GetMessages(ctx context.Context, limit, offset int64) ([]*message.Message, error)
	// ListMessages returns all active (non-compacted, non-deleted) messages sorted by
	// created_at ASC. Use this instead of GetMessages when the full history is needed.
	ListMessages(ctx context.Context) ([]*message.Message, error)
	// ListCompactedMessages returns all messages that were archived by a prior
	// CompactMessages call, sorted by created_at ASC.
	ListCompactedMessages(ctx context.Context) ([]*message.Message, error)
	DeleteMessage(ctx context.Context, messageID int64) error
	CompactMessages(ctx context.Context, compactor func(context.Context, []*message.Message) (*message.Message, error)) error
}
