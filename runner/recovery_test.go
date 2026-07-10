package runner

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
)

func TestFindUnknownToolExecution(t *testing.T) {
	toolCalls := func(turnID string, eventID int64, calls ...model.ToolCall) model.Event {
		return model.Event{
			ID:     eventID,
			TurnID: turnID,
			Content: model.Content{
				Role:      model.RoleAssistant,
				ToolCalls: calls,
			},
		}
	}
	toolResult := func(turnID, callID string) model.Event {
		return model.Event{
			TurnID: turnID,
			Content: model.Content{
				Role:       model.RoleTool,
				ToolCallID: callID,
			},
		}
	}

	tests := []struct {
		name        string
		events      []model.Event
		wantEventID int64
		wantCallIDs []string
	}{
		{
			name: "all calls have results",
			events: []model.Event{
				toolCalls("turn-1", 10,
					model.ToolCall{ID: "call-1", Name: "first"},
					model.ToolCall{ID: "call-2", Name: "second"},
				),
				toolResult("turn-1", "call-1"),
				toolResult("turn-1", "call-2"),
				{Content: model.Content{Role: model.RoleAssistant, Content: "done"}},
			},
		},
		{
			name: "one result is missing",
			events: []model.Event{
				toolCalls("turn-1", 20,
					model.ToolCall{ID: "call-1", Name: "first"},
					model.ToolCall{ID: "call-2", Name: "second"},
				),
				toolResult("turn-1", "call-1"),
			},
			wantEventID: 20,
			wantCallIDs: []string{"call-2"},
		},
		{
			name: "same call ID in a later group does not close it",
			events: []model.Event{
				toolCalls("turn-1", 30, model.ToolCall{ID: "call-1", Name: "first"}),
				{TurnID: "turn-2", Content: model.Content{Role: model.RoleUser, Content: "continue"}},
				toolCalls("turn-2", 31, model.ToolCall{ID: "call-1", Name: "second"}),
				toolResult("turn-2", "call-1"),
			},
			wantEventID: 30,
			wantCallIDs: []string{"call-1"},
		},
		{
			name: "result from another turn does not match",
			events: []model.Event{
				toolCalls("turn-1", 40, model.ToolCall{ID: "call-1", Name: "first"}),
				toolResult("turn-2", "call-1"),
			},
			wantEventID: 40,
			wantCallIDs: []string{"call-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unknown := findUnknownToolExecution("session-1", tt.events)
			if tt.wantEventID == 0 {
				assert.Nil(t, unknown)
				return
			}

			require.NotNil(t, unknown)
			assert.Equal(t, "session-1", unknown.SessionID)
			assert.Equal(t, tt.wantEventID, unknown.EventID)
			callIDs := make([]string, len(unknown.ToolCalls))
			for i, call := range unknown.ToolCalls {
				callIDs[i] = call.ID
			}
			assert.Equal(t, tt.wantCallIDs, callIDs)
		})
	}
}

func TestRunner_Run_BlocksWhenToolExecutionIsUnknown(t *testing.T) {
	var runCount atomic.Int32
	a := &mockAgent{
		name:        "tool-agent",
		description: "leaves one tool call without a result",
		runFunc: func(_ context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				runCount.Add(1)
				if !yield(&model.Event{
					Content: model.Content{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{ID: "call-1", Name: "first"},
							{ID: "call-2", Name: "second"},
						},
					},
				}, nil) {
					return
				}
				yield(&model.Event{
					Content: model.Content{
						Role:       model.RoleTool,
						ToolCallID: "call-1",
						Content:    "done",
					},
				}, nil)
			}
		},
	}
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, model.Content{Content: "first turn"})
	require.NoError(t, err)

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	before, err := sess.ListEvents(t.Context())
	require.NoError(t, err)
	require.Len(t, before, 3)

	msgs, runErr := collectRun(t, r, sessionID, model.Content{Content: "must not persist"})
	assert.Empty(t, msgs)
	assert.ErrorIs(t, runErr, ErrToolExecutionUnknown)
	var unknown *ToolExecutionUnknownError
	require.True(t, errors.As(runErr, &unknown))
	assert.Equal(t, sessionID, unknown.SessionID)
	assert.Equal(t, before[1].TurnID, unknown.TurnID)
	assert.Equal(t, before[1].EventID, unknown.EventID)
	require.Len(t, unknown.ToolCalls, 1)
	assert.Equal(t, "call-2", unknown.ToolCalls[0].ID)
	assert.Equal(t, int32(1), runCount.Load(), "agent must not run again")

	after, err := sess.ListEvents(t.Context())
	require.NoError(t, err)
	assert.Equal(t, before, after, "blocked run must leave the session unchanged")
}

func TestRunner_Run_EarlyBreakAfterToolCallsBlocksNextRun(t *testing.T) {
	var runCount atomic.Int32
	a := &mockAgent{
		name:        "tool-agent",
		description: "yields one tool call and its result",
		runFunc: func(_ context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				runCount.Add(1)
				if !yield(&model.Event{
					Content: model.Content{
						Role:      model.RoleAssistant,
						ToolCalls: []model.ToolCall{{ID: "call-1", Name: "side-effect"}},
					},
				}, nil) {
					return
				}
				yield(&model.Event{
					Content: model.Content{
						Role:       model.RoleTool,
						ToolCallID: "call-1",
						Content:    "done",
					},
				}, nil)
			}
		},
	}
	r, sessionID := newRunnerWithSession(t, a)

	for event, err := range r.Run(t.Context(), sessionID, model.Content{Content: "first turn"}) {
		require.NoError(t, err)
		assert.Equal(t, model.RoleAssistant, event.Content.Role)
		break
	}

	_, runErr := collectRun(t, r, sessionID, model.Content{Content: "second turn"})
	assert.ErrorIs(t, runErr, ErrToolExecutionUnknown)
	assert.Equal(t, int32(1), runCount.Load(), "agent must not run again")

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	events, err := sess.ListEvents(t.Context())
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, string(model.RoleUser), events[0].Role)
	assert.Equal(t, string(model.RoleAssistant), events[1].Role)
}

func TestRunner_Run_ContinuesWhenToolCallsHaveDurableResults(t *testing.T) {
	a := staticAgent(
		model.Content{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{{ID: "call-1", Name: "lookup"}},
		},
		model.Content{
			Role:       model.RoleTool,
			ToolCallID: "call-1",
			Content:    "result",
		},
		model.Content{Role: model.RoleAssistant, Content: "done"},
	)
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, model.Content{Content: "first turn"})
	require.NoError(t, err)
	_, err = collectRun(t, r, sessionID, model.Content{Content: "second turn"})
	require.NoError(t, err)

	// The second run receives the complete first turn plus its new user event.
	require.Len(t, a.capturedMessages, 5)
	assert.Equal(t, model.RoleUser, a.capturedMessages[4].Content.Role)
	assert.Equal(t, "second turn", a.capturedMessages[4].Content.Content)
}
