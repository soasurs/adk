package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adksession "github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/event"
)

func newTestMessage(id int64, content string) *event.Event {
	return &event.Event{
		EventID:   id,
		Content:   content,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
}

func newMemorySessionRequest(sessionID string) adksession.CreateSessionRequest {
	return adksession.CreateSessionRequest{SessionID: sessionID}
}

func TestMemorySession_CreateEvent(t *testing.T) {
	sessionID := "session-1"

	session := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	msg := newTestMessage(1, "hello")
	err := session.CreateEvent(ctx, msg)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(1), msgs[0].EventID)
	assert.Positive(t, session.GetCreatedAt())
}

func TestMemorySession_DeleteEvent(t *testing.T) {
	sessionID := "session-1"

	session := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")

	session.CreateEvent(ctx, msg1)
	session.CreateEvent(ctx, msg2)
	session.CreateEvent(ctx, msg3)

	err := session.DeleteEvent(ctx, 2)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)

	for _, m := range msgs {
		assert.NotEqual(t, int64(2), m.EventID)
	}
}

func TestMemorySession_DeleteEvent_NotFound(t *testing.T) {
	sessionID := "session-1"

	session := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	msg := newTestMessage(1, "hello")
	session.CreateEvent(ctx, msg)

	err := session.DeleteEvent(ctx, 999)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
}

func TestMemorySession_GetEvents(t *testing.T) {
	sessionID := "session-1"

	session := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	for i := int64(1); i <= 10; i++ {
		msg := newTestMessage(i, "msg")
		session.CreateEvent(ctx, msg)
	}

	t.Run("get all", func(t *testing.T) {
		msgs, err := session.GetEvents(ctx, 100, 0)
		assert.NoError(t, err)
		assert.Len(t, msgs, 10)
	})

	t.Run("with limit", func(t *testing.T) {
		msgs, err := session.GetEvents(ctx, 5, 0)
		assert.NoError(t, err)
		assert.Len(t, msgs, 5)
	})

	t.Run("with offset", func(t *testing.T) {
		msgs, err := session.GetEvents(ctx, 5, 3)
		assert.NoError(t, err)
		assert.Len(t, msgs, 5)
		assert.Equal(t, int64(4), msgs[0].EventID)
	})

	t.Run("limit and offset", func(t *testing.T) {
		msgs, err := session.GetEvents(ctx, 3, 2)
		assert.NoError(t, err)
		assert.Len(t, msgs, 3)
		assert.Equal(t, int64(3), msgs[0].EventID)
	})
}

func TestMemorySession_GetEvents_StableOrder(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	const createdAt = int64(1234)
	for _, id := range []int64{3, 1, 2} {
		msg := newTestMessage(id, "msg")
		msg.CreatedAt = createdAt
		assert.NoError(t, sess.CreateEvent(ctx, msg))
	}

	msgs, err := sess.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Equal(t, []int64{1, 2, 3}, []int64{
		msgs[0].EventID,
		msgs[1].EventID,
		msgs[2].EventID,
	})
}

func TestMemorySession_ArchiveEventsBefore(t *testing.T) {
	sessionID := "session-1"

	session := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")
	msg4 := newTestMessage(4, "fine")

	session.CreateEvent(ctx, msg1)
	session.CreateEvent(ctx, msg2)
	session.CreateEvent(ctx, msg3)
	session.CreateEvent(ctx, msg4)

	// Archive msg1 and msg2; keep msg3 and msg4 active.
	err := session.ArchiveEventsBefore(ctx, 3)
	assert.NoError(t, err)
	assert.NoError(t, session.ArchiveEventsBefore(ctx, 3), "archival should be idempotent")

	msgs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.Equal(t, int64(3), msgs[0].EventID)
	assert.Equal(t, int64(4), msgs[1].EventID)

	archived, err := session.ListArchivedEvents(ctx)
	assert.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, []int64{archived[0].EventID, archived[1].EventID})
}

func TestMemorySession_ArchiveEventsBefore_ArchiveAll(t *testing.T) {
	sessionID := "session-1"

	session := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	session.CreateEvent(ctx, newTestMessage(1, "hello"))
	session.CreateEvent(ctx, newTestMessage(2, "hi"))

	// eventID=0 archives everything.
	err := session.ArchiveEventsBefore(ctx, 0)
	assert.NoError(t, err)

	msgs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Empty(t, msgs)

	archived, err := session.ListArchivedEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, archived, 2)
}

func TestMemorySession_ArchiveEventsBefore_Empty(t *testing.T) {
	sessionID := "session-1"

	session := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	err := session.ArchiveEventsBefore(ctx, 0)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestMemorySession_ListEvents(t *testing.T) {
	sessionID := "session-1"

	sess := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	for i := int64(1); i <= 5; i++ {
		sess.CreateEvent(ctx, newTestMessage(i, "msg"))
	}

	msgs, err := sess.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 5)
}

func TestMemorySession_ArchiveEventsBefore_MultipleRounds(t *testing.T) {
	sessionID := "session-1"

	sess := NewMemorySession(newMemorySessionRequest(sessionID))
	ctx := t.Context()

	sess.CreateEvent(ctx, newTestMessage(1, "a"))
	sess.CreateEvent(ctx, newTestMessage(2, "b"))

	err := sess.ArchiveEventsBefore(ctx, 0)
	assert.NoError(t, err)

	sess.CreateEvent(ctx, newTestMessage(3, "c"))

	err = sess.ArchiveEventsBefore(ctx, 0)
	assert.NoError(t, err)

	active, err := sess.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Empty(t, active)

	archived, err := sess.ListArchivedEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, archived, 3)
}

func TestMemorySession_ArchiveEventsBefore_MissingBoundary(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()
	assert.NoError(t, sess.CreateEvent(ctx, newTestMessage(1, "a")))

	err := sess.ArchiveEventsBefore(ctx, 99)

	assert.ErrorIs(t, err, adksession.ErrArchiveBoundaryNotFound)
	var boundaryErr *adksession.ArchiveBoundaryNotFoundError
	assert.ErrorAs(t, err, &boundaryErr)
	assert.Equal(t, int64(99), boundaryErr.EventID)
	active, listErr := sess.ListEvents(ctx)
	assert.NoError(t, listErr)
	assert.Len(t, active, 1)
}

func TestMemorySession_TurnLifecycle(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	turns := sess.(adksession.TurnStore)
	ctx := t.Context()

	require.NoError(t, turns.BeginTurn(ctx, adksession.Turn{
		ID:        "turn-1",
		SessionID: "session-1",
		Status:    adksession.TurnRunning,
		StartedAt: 1,
	}))
	require.NoError(t, turns.FinalizeTurn(ctx, "turn-1", adksession.TurnOutcome{
		Status: adksession.TurnCompleted,
	}))

	turn, err := turns.GetTurn(ctx, "turn-1")
	require.NoError(t, err)
	require.NotNil(t, turn)
	assert.Equal(t, adksession.TurnCompleted, turn.Status)
	assert.Positive(t, turn.FinishedAt)
	assert.ErrorIs(t, turns.FinalizeTurn(ctx, "turn-1", adksession.TurnOutcome{
		Status: adksession.TurnFailed,
		Reason: adksession.TurnReasonAgentError,
	}), adksession.ErrTurnStateConflict)
}

func TestMemorySession_InterruptRunningTurns(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	turns := sess.(adksession.TurnStore)
	ctx := t.Context()
	require.NoError(t, turns.BeginTurn(ctx, adksession.Turn{
		ID:        "turn-1",
		Status:    adksession.TurnRunning,
		StartedAt: 1,
	}))

	require.NoError(t, turns.InterruptRunningTurns(ctx, adksession.TurnReasonAbandoned))
	turn, err := turns.GetTurn(ctx, "turn-1")
	require.NoError(t, err)
	require.NotNil(t, turn)
	assert.Equal(t, adksession.TurnInterrupted, turn.Status)
	assert.Equal(t, adksession.TurnReasonAbandoned, turn.Reason)
}

func TestMemorySession_TurnFailureIsCloned(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	turns := sess.(adksession.TurnStore)
	ctx := t.Context()
	require.NoError(t, turns.BeginTurn(ctx, adksession.Turn{
		ID:        "turn-1",
		Status:    adksession.TurnRunning,
		StartedAt: 1,
	}))
	failure := &adksession.TurnFailure{
		Code:    "provider_unavailable",
		Message: "safe",
		Stage:   adksession.TurnFailureStageProvider,
	}
	require.NoError(t, turns.FinalizeTurn(ctx, "turn-1", adksession.TurnOutcome{
		Status:  adksession.TurnFailed,
		Reason:  adksession.TurnReasonAgentError,
		Failure: failure,
	}))
	failure.Message = "mutated"

	turn, err := turns.GetTurn(ctx, "turn-1")
	require.NoError(t, err)
	require.NotNil(t, turn.Failure)
	assert.Equal(t, "safe", turn.Failure.Message)
	turn.Failure.Message = "mutated again"
	turn, err = turns.GetTurn(ctx, "turn-1")
	require.NoError(t, err)
	assert.Equal(t, "safe", turn.Failure.Message)
}
