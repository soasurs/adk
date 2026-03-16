package compaction

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/session/message"
)

func newTestMsg(id int64, role, content string, promptTokens int64) *message.Message {
	return &message.Message{
		MessageID:    id,
		Role:         role,
		Content:      content,
		PromptTokens: promptTokens,
	}
}

// ---------------------------------------------------------------------------
// ShouldCompact
// ---------------------------------------------------------------------------

func TestShouldCompact(t *testing.T) {
	// Simulate 3 messages; only the last assistant message has PromptTokens set.
	msgs := []*message.Message{
		newTestMsg(1, "user", "hi", 0),
		newTestMsg(2, "assistant", "hello", 300),
		newTestMsg(3, "user", "how are you", 0),
		newTestMsg(4, "assistant", "fine", 450),
	}

	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{
			name: "below token threshold",
			cfg:  Config{MaxTokens: 1000},
			want: false,
		},
		{
			name: "exceeds token threshold",
			cfg:  Config{MaxTokens: 400},
			want: true,
		},
		{
			name: "max tokens zero disables trigger",
			cfg:  Config{},
			want: false,
		},
		{
			name: "token threshold exactly at limit",
			cfg:  Config{MaxTokens: 450},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ShouldCompact(msgs, tt.cfg))
		})
	}
}

func TestShouldCompact_EmptyMessages(t *testing.T) {
	assert.False(t, ShouldCompact([]*message.Message{}, Config{MaxTokens: 100}))
}

func TestShouldCompact_NoAssistantMessage(t *testing.T) {
	// No assistant messages yet → no usage data → should not trigger.
	msgs := []*message.Message{
		newTestMsg(1, "user", "hi", 0),
	}
	assert.False(t, ShouldCompact(msgs, Config{MaxTokens: 1}))
}

// ---------------------------------------------------------------------------
// splitByRounds
// ---------------------------------------------------------------------------

func TestSplitByRounds(t *testing.T) {
	// 3 rounds: [u1,a1] [u2,a2] [u3,a3]
	msgs := []*message.Message{
		newTestMsg(1, "user", "u1", 0),
		newTestMsg(2, "assistant", "a1", 0),
		newTestMsg(3, "user", "u2", 0),
		newTestMsg(4, "assistant", "a2", 0),
		newTestMsg(5, "user", "u3", 0),
		newTestMsg(6, "assistant", "a3", 0),
	}

	tests := []struct {
		name           string
		keepRounds     int
		wantArchiveIDs []int64
		wantKeepIDs    []int64
	}{
		{
			name:           "keep 1 round",
			keepRounds:     1,
			wantArchiveIDs: []int64{1, 2, 3, 4},
			wantKeepIDs:    []int64{5, 6},
		},
		{
			name:           "keep 2 rounds",
			keepRounds:     2,
			wantArchiveIDs: []int64{1, 2},
			wantKeepIDs:    []int64{3, 4, 5, 6},
		},
		{
			name:           "keep 3 rounds equals all",
			keepRounds:     3,
			wantArchiveIDs: []int64{},
			wantKeepIDs:    []int64{1, 2, 3, 4, 5, 6},
		},
		{
			name:           "keep more rounds than available",
			keepRounds:     10,
			wantArchiveIDs: []int64{},
			wantKeepIDs:    []int64{1, 2, 3, 4, 5, 6},
		},
		{
			name:           "keep 0 rounds archives all",
			keepRounds:     0,
			wantArchiveIDs: []int64{1, 2, 3, 4, 5, 6},
			wantKeepIDs:    []int64{},
		},
		{
			name:           "negative rounds archives all",
			keepRounds:     -1,
			wantArchiveIDs: []int64{1, 2, 3, 4, 5, 6},
			wantKeepIDs:    []int64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive, keep := splitByRounds(msgs, tt.keepRounds)
			assert.Equal(t, tt.wantArchiveIDs, msgIDs(archive))
			assert.Equal(t, tt.wantKeepIDs, msgIDs(keep))
		})
	}
}

func TestSplitByRounds_EmptyMessages(t *testing.T) {
	archive, keep := splitByRounds([]*message.Message{}, 2)
	assert.Empty(t, archive)
	assert.Empty(t, keep)
}

func TestSplitByRounds_RoundWithToolCalls(t *testing.T) {
	// Simulate a round that includes tool messages: user → assistant(tool_call) → tool → assistant
	msgs := []*message.Message{
		newTestMsg(1, "user", "old question", 0),
		newTestMsg(2, "assistant", "old answer", 0),
		newTestMsg(3, "user", "call a tool", 0),
		newTestMsg(4, "assistant", "", 0), // assistant requests tool call
		newTestMsg(5, "tool", "tool result", 0),
		newTestMsg(6, "assistant", "final answer", 0),
	}

	// Keep 1 round: everything from the last user message onwards.
	archive, keep := splitByRounds(msgs, 1)
	assert.Equal(t, []int64{1, 2}, msgIDs(archive))
	assert.Equal(t, []int64{3, 4, 5, 6}, msgIDs(keep))
}

// ---------------------------------------------------------------------------
// NewSlidingWindowCompactor
// ---------------------------------------------------------------------------

func TestNewSlidingWindowCompactor_SplitsRounds(t *testing.T) {
	// 2 rounds: [user "a", assistant "b"] [user "c", assistant "d"]
	msgs := []*message.Message{
		newTestMsg(1, "user", "a", 0),
		newTestMsg(2, "assistant", "b", 0),
		newTestMsg(3, "user", "c", 0),
		newTestMsg(4, "assistant", "d", 0),
	}

	var archived []*message.Message
	summarizer := func(_ context.Context, m []*message.Message) (string, error) {
		archived = m
		return "old summary", nil
	}

	// Keep 1 round → archive round 1, keep round 2.
	compactor, err := NewSlidingWindowCompactor(Config{KeepRecentRounds: 1}, summarizer)
	require.NoError(t, err)

	splitID, summaryMsg, err := compactor(t.Context(), msgs)
	require.NoError(t, err)

	// Archived: msg 1 and 2.
	require.Len(t, archived, 2)
	assert.Equal(t, int64(1), archived[0].MessageID)
	assert.Equal(t, int64(2), archived[1].MessageID)

	// splitID points to first kept message.
	assert.Equal(t, int64(3), splitID)

	assert.Equal(t, "system", summaryMsg.Role)
	assert.Equal(t, "old summary", summaryMsg.Content)
	assert.NotZero(t, summaryMsg.MessageID)
	assert.NotZero(t, summaryMsg.CreatedAt)
	assert.NotZero(t, summaryMsg.UpdatedAt)
}

func TestNewSlidingWindowCompactor_KeepRoundsExceedsAvailable(t *testing.T) {
	// Only 1 round available; request to keep 10 rounds → nothing archived.
	msgs := []*message.Message{
		newTestMsg(1, "user", "hi", 0),
		newTestMsg(2, "assistant", "hello", 0),
	}

	var summarizerCalled bool
	summarizer := func(_ context.Context, _ []*message.Message) (string, error) {
		summarizerCalled = true
		return "", nil
	}

	compactor, err := NewSlidingWindowCompactor(Config{KeepRecentRounds: 10}, summarizer)
	require.NoError(t, err)

	splitID, summaryMsg, err := compactor(t.Context(), msgs)
	require.NoError(t, err)

	// Nothing to archive: summarizer must not be called, result is nil.
	assert.False(t, summarizerCalled)
	assert.Zero(t, splitID)
	assert.Nil(t, summaryMsg)
}

func TestNewSlidingWindowCompactor_KeepRoundsZero(t *testing.T) {
	msgs := []*message.Message{
		newTestMsg(1, "user", "a", 0),
		newTestMsg(2, "assistant", "b", 0),
	}

	var archived []*message.Message
	summarizer := func(_ context.Context, m []*message.Message) (string, error) {
		archived = m
		return "full summary", nil
	}

	// KeepRecentRounds=0 archives everything; splitID=0 (no messages to keep).
	compactor, err := NewSlidingWindowCompactor(Config{KeepRecentRounds: 0}, summarizer)
	require.NoError(t, err)

	splitID, summaryMsg, err := compactor(t.Context(), msgs)
	require.NoError(t, err)

	assert.Len(t, archived, 2)
	assert.Zero(t, splitID)
	require.NotNil(t, summaryMsg)
	assert.Equal(t, "full summary", summaryMsg.Content)
}

func TestNewSlidingWindowCompactor_SummarizerError(t *testing.T) {
	msgs := []*message.Message{
		newTestMsg(1, "user", "a", 0),
		newTestMsg(2, "assistant", "b", 0),
		newTestMsg(3, "user", "c", 0),
	}

	summarizer := func(_ context.Context, _ []*message.Message) (string, error) {
		return "", errors.New("llm unavailable")
	}

	compactor, err := NewSlidingWindowCompactor(Config{KeepRecentRounds: 1}, summarizer)
	require.NoError(t, err)

	_, _, err = compactor(t.Context(), msgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "llm unavailable")
}

func TestNewSlidingWindowCompactor_EmptyMessages(t *testing.T) {
	summarizer := func(_ context.Context, _ []*message.Message) (string, error) {
		return "", nil
	}

	compactor, err := NewSlidingWindowCompactor(Config{KeepRecentRounds: 5}, summarizer)
	require.NoError(t, err)

	splitID, summaryMsg, err := compactor(t.Context(), []*message.Message{})
	require.NoError(t, err)

	// Empty input: nothing to archive.
	assert.Zero(t, splitID)
	assert.Nil(t, summaryMsg)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func msgIDs(msgs []*message.Message) []int64 {
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.MessageID
	}
	return ids
}
