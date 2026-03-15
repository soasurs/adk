package agent

import (
	"context"
	"iter"

	"github.com/soasurs/adk/model"
)

type Agent interface {
	Name() string
	Description() string
	// Run executes the agent with the given conversation messages and yields each
	// Event as it is produced. Partial events (Event.Partial=true) carry streaming
	// text fragments for real-time display; complete events (Event.Partial=false)
	// carry fully assembled messages (assistant replies, tool results, etc.).
	// The caller iterates until the sequence ends or breaks early.
	Run(ctx context.Context, messages []model.Message) iter.Seq2[*model.Event, error]
}
