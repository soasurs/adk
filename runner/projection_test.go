package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
	sessionevent "github.com/soasurs/adk/session/event"
	memorysession "github.com/soasurs/adk/session/memory"
)

func TestDefaultProjector_FailedClosedTurn(t *testing.T) {
	input := ProjectionInput{
		Turns: []*session.Turn{{
			ID:        "turn-1",
			SessionID: "session-1",
			Status:    session.TurnFailed,
			Reason:    session.TurnReasonAgentError,
			Failure: &session.TurnFailure{
				Code:    "provider_unavailable",
				Message: "display-only detail",
				Stage:   session.TurnFailureStageProvider,
			},
		}},
		Events: persistedProjectionEvents(
			model.Event{ID: 1, SessionID: "session-1", TurnID: "turn-1", Content: model.Content{Role: model.RoleUser, Content: "work"}},
			model.Event{ID: 2, SessionID: "session-1", TurnID: "turn-1", Content: model.Content{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "call-1", Name: "read"}},
			}},
			model.Event{ID: 3, SessionID: "session-1", TurnID: "turn-1", Content: model.Content{
				Role:       model.RoleTool,
				ToolCallID: "call-1",
				Content:    "result",
			}},
			model.Event{ID: 4, SessionID: "session-1", TurnID: "turn-1", Content: model.Content{Role: model.RoleAssistant, Content: "attempted"}},
		),
	}

	projected, err := NewDefaultProjector().Project(t.Context(), input)
	require.NoError(t, err)
	require.Len(t, projected, 5)
	assert.Equal(t, "attempted", projected[3].Content.Content)
	assert.Contains(t, projected[4].Content.Content, "provider_unavailable")
	assert.NotContains(t, projected[4].Content.Content, "display-only detail")
	assert.NoError(t, ValidateToolProtocol(projected))
}

func TestDefaultProjector_DanglingToolCallCutsUnsafeSuffix(t *testing.T) {
	input := ProjectionInput{
		Turns: []*session.Turn{{
			ID:        "turn-1",
			SessionID: "session-1",
			Status:    session.TurnInterrupted,
			Reason:    session.TurnReasonAbandoned,
		}},
		Events: persistedProjectionEvents(
			model.Event{ID: 1, SessionID: "session-1", TurnID: "turn-1", Content: model.Content{Role: model.RoleUser, Content: "work"}},
			model.Event{ID: 2, SessionID: "session-1", TurnID: "turn-1", Content: model.Content{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "call-1", Name: "write"}},
			}},
			model.Event{ID: 3, SessionID: "session-1", TurnID: "turn-1", Content: model.Content{Role: model.RoleAssistant, Content: "unsafe suffix"}},
		),
	}

	projected, err := NewDefaultProjector().Project(t.Context(), input)
	require.NoError(t, err)
	require.Len(t, projected, 2)
	assert.Equal(t, "work", projected[0].Content.Content)
	assert.Contains(t, projected[1].Content.Content, "abandoned")
}

func TestDefaultProjector_FailsClosedForMalformedCompletedTurn(t *testing.T) {
	input := ProjectionInput{
		Turns: []*session.Turn{{ID: "turn-1", Status: session.TurnCompleted}},
		Events: persistedProjectionEvents(model.Event{
			ID:     1,
			TurnID: "turn-1",
			Content: model.Content{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "call-1", Name: "write"}},
			},
		}),
	}

	_, err := NewDefaultProjector().Project(t.Context(), input)
	assert.ErrorIs(t, err, ErrToolExecutionUnknown)
}

func TestDefaultProjector_RunningAndLegacyTurns(t *testing.T) {
	input := ProjectionInput{
		Turns: []*session.Turn{{ID: "running", Status: session.TurnRunning}},
		Events: persistedProjectionEvents(
			model.Event{ID: 1, Content: model.Content{Role: model.RoleUser, Content: "legacy"}},
			model.Event{ID: 2, TurnID: "running", Content: model.Content{Role: model.RoleUser, Content: "hidden"}},
		),
	}

	projected, err := NewDefaultProjector().Project(t.Context(), input)
	require.NoError(t, err)
	require.Len(t, projected, 1)
	assert.Equal(t, "legacy", projected[0].Content.Content)
}

func TestRunner_CustomProjectorStillValidatesToolProtocol(t *testing.T) {
	a := staticAgent(model.Content{Role: model.RoleAssistant, Content: "must not run"})
	const sessionID = "session-1"
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(t.Context(), session.CreateSessionRequest{SessionID: sessionID})
	require.NoError(t, err)
	projector := ProjectorFunc(func(_ context.Context, _ ProjectionInput) ([]model.Event, error) {
		return []model.Event{{
			ID:        99,
			SessionID: sessionID,
			TurnID:    "projected",
			Content: model.Content{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "call-1", Name: "unsafe"}},
			},
		}}, nil
	})
	r, err := New(a, svc, WithProjector(projector))
	require.NoError(t, err)

	_, runErr := collectRun(t, r, sessionID, model.Content{Content: "continue"})
	assert.ErrorIs(t, runErr, ErrToolExecutionUnknown)
	assert.Empty(t, a.capturedMessages)
}

func persistedProjectionEvents(events ...model.Event) []*sessionevent.Event {
	stored := make([]*sessionevent.Event, 0, len(events))
	for _, event := range events {
		stored = append(stored, sessionevent.FromModel(event))
	}
	return stored
}
