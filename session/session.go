package session

import (
	"context"

	"github.com/soasurs/adk/session/message"
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
	DeleteMessage(ctx context.Context, messageID int64) error
	// CompactMessages archives all active messages that precede splitMessageID and
	// inserts summaryMsg as the new first active message. If splitMessageID is 0,
	// all currently active messages are archived. The caller is responsible for
	// constructing both the split point and the summary message (e.g. via the
	// compaction package).
	CompactMessages(ctx context.Context, splitMessageID int64, summaryMsg *message.Message) error
}
