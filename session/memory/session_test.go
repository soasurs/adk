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

func TestMemorySession_ListTurns_Empty(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	turns, err := sess.ListTurns(ctx)
	assert.NoError(t, err)
	assert.Empty(t, turns)
}

func TestMemorySession_ListTurns_SingleTurn(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "hello")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "world")
	ev2.TurnID = "turn-1"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))

	turns, err := sess.ListTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, "turn-1", turns[0].TurnID)
	require.Len(t, turns[0].Events, 2)
	assert.Equal(t, int64(1), turns[0].Events[0].EventID)
	assert.Equal(t, int64(2), turns[0].Events[1].EventID)
}

func TestMemorySession_ListTurns_MultipleTurns(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = "turn-1"
	ev3 := newTestMessage(3, "c")
	ev3.TurnID = "turn-2"
	ev4 := newTestMessage(4, "d")
	ev4.TurnID = "turn-2"
	ev5 := newTestMessage(5, "e")
	ev5.TurnID = "turn-2"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))
	require.NoError(t, sess.CreateEvent(ctx, ev3))
	require.NoError(t, sess.CreateEvent(ctx, ev4))
	require.NoError(t, sess.CreateEvent(ctx, ev5))

	turns, err := sess.ListTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 2)

	assert.Equal(t, "turn-1", turns[0].TurnID)
	assert.Len(t, turns[0].Events, 2)
	assert.Equal(t, int64(1), turns[0].Events[0].EventID)
	assert.Equal(t, int64(2), turns[0].Events[1].EventID)

	assert.Equal(t, "turn-2", turns[1].TurnID)
	require.Len(t, turns[1].Events, 3)
	assert.Equal(t, int64(3), turns[1].Events[0].EventID)
	assert.Equal(t, int64(4), turns[1].Events[1].EventID)
	assert.Equal(t, int64(5), turns[1].Events[2].EventID)
}

func TestMemorySession_ListTurns_EmptyTurnID(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = ""
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = ""
	ev3 := newTestMessage(3, "c")
	ev3.TurnID = "turn-1"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))
	require.NoError(t, sess.CreateEvent(ctx, ev3))

	turns, err := sess.ListTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 2)

	assert.Equal(t, "", turns[0].TurnID)
	assert.Len(t, turns[0].Events, 2)

	assert.Equal(t, "turn-1", turns[1].TurnID)
	assert.Len(t, turns[1].Events, 1)
}

func TestMemorySession_ListTurns_NonContiguousTurns(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = "turn-2"
	ev3 := newTestMessage(3, "c")
	ev3.TurnID = "turn-1"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))
	require.NoError(t, sess.CreateEvent(ctx, ev3))

	turns, err := sess.ListTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 3)
	assert.Equal(t, "turn-1", turns[0].TurnID)
	assert.Equal(t, "turn-2", turns[1].TurnID)
	assert.Equal(t, "turn-1", turns[2].TurnID)
}

func TestMemorySession_ListTurns_ExcludesArchived(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = "turn-1"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))

	require.NoError(t, sess.ArchiveEventsBefore(ctx, 0))

	turns, err := sess.ListTurns(ctx)
	require.NoError(t, err)
	assert.Empty(t, turns)
}

func TestMemorySession_ListTurns_ExcludesDeleted(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = "turn-1"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))

	require.NoError(t, sess.DeleteEvent(ctx, 1))

	turns, err := sess.ListTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Len(t, turns[0].Events, 1)
	assert.Equal(t, int64(2), turns[0].Events[0].EventID)
}

func TestMemorySession_ListArchivedTurns_Empty(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	turns, err := sess.ListArchivedTurns(ctx)
	assert.NoError(t, err)
	assert.Empty(t, turns)
}

func TestMemorySession_ListArchivedTurns(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = "turn-1"
	ev3 := newTestMessage(3, "c")
	ev3.TurnID = "turn-2"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))
	require.NoError(t, sess.CreateEvent(ctx, ev3))

	require.NoError(t, sess.ArchiveEventsBefore(ctx, 3))

	turns, err := sess.ListArchivedTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, "turn-1", turns[0].TurnID)
	require.Len(t, turns[0].Events, 2)
	assert.Equal(t, int64(1), turns[0].Events[0].EventID)
	assert.Equal(t, int64(2), turns[0].Events[1].EventID)
}

func TestMemorySession_ListArchivedTurns_MultipleTurns(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = "turn-2"
	ev3 := newTestMessage(3, "c")
	ev3.TurnID = "turn-3"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))
	require.NoError(t, sess.CreateEvent(ctx, ev3))

	require.NoError(t, sess.ArchiveEventsBefore(ctx, 0))

	turns, err := sess.ListArchivedTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 3)
	assert.Equal(t, "turn-1", turns[0].TurnID)
	assert.Equal(t, "turn-2", turns[1].TurnID)
	assert.Equal(t, "turn-3", turns[2].TurnID)
}

func TestMemorySession_ListArchivedTurns_ExcludesActive(t *testing.T) {
	sess := NewMemorySession(newMemorySessionRequest("session-1"))
	ctx := t.Context()

	ev1 := newTestMessage(1, "a")
	ev1.TurnID = "turn-1"
	ev2 := newTestMessage(2, "b")
	ev2.TurnID = "turn-2"
	require.NoError(t, sess.CreateEvent(ctx, ev1))
	require.NoError(t, sess.CreateEvent(ctx, ev2))

	require.NoError(t, sess.ArchiveEventsBefore(ctx, 2))

	turns, err := sess.ListArchivedTurns(ctx)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, "turn-1", turns[0].TurnID)
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
