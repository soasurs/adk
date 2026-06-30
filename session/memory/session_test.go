package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/soasurs/adk/internal/snowflake"
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

func TestMemorySession_CreateEvent(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := t.Context()

	msg := newTestMessage(1, "hello")
	err = session.CreateEvent(ctx, msg)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(1), msgs[0].EventID)
}

func TestMemorySession_DeleteEvent(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := t.Context()

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")

	session.CreateEvent(ctx, msg1)
	session.CreateEvent(ctx, msg2)
	session.CreateEvent(ctx, msg3)

	err = session.DeleteEvent(ctx, 2)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)

	for _, m := range msgs {
		assert.NotEqual(t, int64(2), m.EventID)
	}
}

func TestMemorySession_DeleteEvent_NotFound(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := t.Context()

	msg := newTestMessage(1, "hello")
	session.CreateEvent(ctx, msg)

	err = session.DeleteEvent(ctx, 999)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
}

func TestMemorySession_GetEvents(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
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
	sess := NewMemorySession(1)
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

func TestMemorySession_CompactEvents(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := t.Context()

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")
	msg4 := newTestMessage(4, "fine")

	session.CreateEvent(ctx, msg1)
	session.CreateEvent(ctx, msg2)
	session.CreateEvent(ctx, msg3)
	session.CreateEvent(ctx, msg4)

	summaryMsg := newTestMessage(100, "summary")

	// Archive msg1 and msg2; keep msg3 and msg4 as structured messages.
	err = session.CompactEvents(ctx, 3, summaryMsg)
	assert.NoError(t, err)

	// Active history: kept messages + summary appended.
	msgs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.Equal(t, int64(3), msgs[0].EventID)
	assert.Equal(t, int64(4), msgs[1].EventID)
	assert.Equal(t, int64(100), msgs[2].EventID)
}

func TestMemorySession_CompactEvents_ArchiveAll(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := t.Context()

	session.CreateEvent(ctx, newTestMessage(1, "hello"))
	session.CreateEvent(ctx, newTestMessage(2, "hi"))

	summaryMsg := newTestMessage(100, "summary")

	// splitEventID=0 archives everything.
	err = session.CompactEvents(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].EventID)
}

func TestMemorySession_CompactEvents_Empty(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := t.Context()

	summaryMsg := newTestMessage(100, "summary")

	// Compacting an empty session (splitEventID=0) just inserts the summary.
	err = session.CompactEvents(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].EventID)

}

func TestMemorySession_ListEvents(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	sess := NewMemorySession(sessionID)
	ctx := t.Context()

	for i := int64(1); i <= 5; i++ {
		sess.CreateEvent(ctx, newTestMessage(i, "msg"))
	}

	msgs, err := sess.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 5)
}

func TestMemorySession_CompactEvents_MultipleRounds(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	sess := NewMemorySession(sessionID)
	ctx := t.Context()

	sess.CreateEvent(ctx, newTestMessage(1, "a"))
	sess.CreateEvent(ctx, newTestMessage(2, "b"))

	// First compaction: archive all (splitEventID=0), insert summary1.
	err = sess.CompactEvents(ctx, 0, newTestMessage(10, "summary1"))
	assert.NoError(t, err)

	sess.CreateEvent(ctx, newTestMessage(3, "c"))

	// Second compaction: archive summary1+c, insert summary2.
	err = sess.CompactEvents(ctx, 0, newTestMessage(20, "summary2"))
	assert.NoError(t, err)

	// Active: only summary2.
	active, err := sess.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, int64(20), active[0].EventID)
}
