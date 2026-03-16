// Package compaction provides utilities for sliding-window session compaction.
// It works with the Session.CompactMessages method to archive old messages and
// keep context length and token costs under control.
package compaction

import (
	"context"
	"fmt"
	"time"

	snowflaker "github.com/soasurs/adk/internal/snowflake"
	"github.com/soasurs/adk/session/message"
)

// Config controls when and how sliding-window compaction is triggered.
type Config struct {
	// MaxTokens triggers compaction when the sum of TotalTokens across all active
	// messages exceeds this value. Zero disables token-based triggering.
	MaxTokens int64
	// KeepRecentRounds is the number of most-recent conversation rounds preserved
	// verbatim inside the compaction summary. A round starts at each user message
	// and includes all subsequent messages until the next user message. Older
	// rounds are passed to the Summarizer. Zero or negative compacts all messages.
	KeepRecentRounds int
}

// Summarizer converts a slice of older messages into a plain-text summary string
// that is embedded into the resulting compaction summary message.
type Summarizer func(ctx context.Context, msgs []*message.Message) (string, error)

// ShouldCompact reports whether the active messages satisfy the trigger conditions
// defined by cfg. It estimates the current context window size by inspecting the
// most recent assistant message's PromptTokens, which is the actual input-token
// count reported by the LLM for the last call and therefore the best available
// proxy for how large the context has grown.
func ShouldCompact(msgs []*message.Message, cfg Config) bool {
	if cfg.MaxTokens <= 0 {
		return false
	}
	// Walk backwards to find the most recent message with usage data.
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].PromptTokens > 0 {
			return msgs[i].PromptTokens > cfg.MaxTokens
		}
	}
	return false
}

// NewSlidingWindowCompactor returns a compactor function that splits msgs into
// the portion to archive and the portion to keep, calls summarizer on the
// archived portion, and returns the split point and a summary message.
//
// splitMessageID is the MessageID of the first message to keep; the caller
// should pass it to Session.CompactMessages together with summaryMsg. If
// there are no messages to archive (e.g. fewer rounds than
// cfg.KeepRecentRounds), the function returns (0, nil, nil) and the caller
// should skip the CompactMessages call.
func NewSlidingWindowCompactor(cfg Config, summarizer Summarizer) (func(context.Context, []*message.Message) (int64, *message.Message, error), error) {
	sf, err := snowflaker.New()
	if err != nil {
		return nil, fmt.Errorf("compaction: init snowflake: %w", err)
	}

	return func(ctx context.Context, msgs []*message.Message) (int64, *message.Message, error) {
		toArchive, toKeep := splitByRounds(msgs, cfg.KeepRecentRounds)

		if len(toArchive) == 0 {
			// Nothing to archive; caller should skip CompactMessages.
			return 0, nil, nil
		}

		summary, err := summarizer(ctx, toArchive)
		if err != nil {
			return 0, nil, fmt.Errorf("compaction: summarize: %w", err)
		}

		now := time.Now().UnixMilli()
		summaryMsg := &message.Message{
			MessageID: sf.Generate().Int64(),
			Role:      "system",
			Content:   summary,
			CreatedAt: now,
			UpdatedAt: now,
		}

		var splitMessageID int64
		if len(toKeep) > 0 {
			splitMessageID = toKeep[0].MessageID
		}

		return splitMessageID, summaryMsg, nil
	}, nil
}

// splitByRounds splits msgs into (toArchive, toKeep) by counting conversation
// rounds from the end. keepRounds <= 0 archives all messages.
// A round starts at each user message; the split point is the index of the
// keepRounds-th user message counted from the end.
func splitByRounds(msgs []*message.Message, keepRounds int) (toArchive, toKeep []*message.Message) {
	if keepRounds <= 0 {
		return msgs, nil
	}

	// Walk backwards and find the start index of the keepRounds-th user message.
	roundsSeen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			roundsSeen++
			if roundsSeen == keepRounds {
				return msgs[:i], msgs[i:]
			}
		}
	}

	// Fewer rounds available than requested: keep everything.
	return nil, msgs
}
