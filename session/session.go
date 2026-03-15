package session

import (
	"context"

	"soasurs.dev/soasurs/adk/session/message"
)

type Session interface {
	GetSessionID() int64
	CreateMessage(ctx context.Context, message *message.Message) error
	GetMessages(ctx context.Context, limit, offset int64) ([]*message.Message, error)
	DeleteMessage(ctx context.Context, messageID int64) error
	CompactMessages(ctx context.Context, compactor func(context.Context, []*message.Message) (*message.Message, error)) error
}
