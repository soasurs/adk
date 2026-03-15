package agent

import (
	"context"
	"iter"

	"soasurs.dev/soasurs/adk/model"
)

type Agent interface {
	Name() string
	Description() string
	// Run executes the agent with the given conversation messages and yields each
	// message as it is produced (assistant replies, tool results, etc.).
	// The caller iterates until the sequence ends or breaks early.
	Run(ctx context.Context, messages []model.Message) iter.Seq2[model.Message, error]
}
