package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/soasurs/adk/internal/snowflake"
	"github.com/soasurs/adk/session/message"
)

func newTestMessage(id int64, content string) *message.Message {
	return &message.Message{
		MessageID: id,
		Content:   content,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
}

func TestMemorySession_CreateMessage(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := context.Background()

	msg := newTestMessage(1, "hello")
	err = session.CreateMessage(ctx, msg)
	assert.NoError(t, err)

	msgs, err := session.GetMessages(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(1), msgs[0].MessageID)
}

func TestMemorySession_DeleteMessage(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := context.Background()

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")

	session.CreateMessage(ctx, msg1)
	session.CreateMessage(ctx, msg2)
	session.CreateMessage(ctx, msg3)

	err = session.DeleteMessage(ctx, 2)
	assert.NoError(t, err)

	msgs, err := session.GetMessages(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)

	for _, m := range msgs {
		assert.NotEqual(t, int64(2), m.MessageID)
	}
}

func TestMemorySession_DeleteMessage_NotFound(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := context.Background()

	msg := newTestMessage(1, "hello")
	session.CreateMessage(ctx, msg)

	err = session.DeleteMessage(ctx, 999)
	assert.NoError(t, err)

	msgs, err := session.GetMessages(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
}

func TestMemorySession_GetMessages(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := context.Background()

	for i := int64(1); i <= 10; i++ {
		msg := newTestMessage(i, "msg")
		session.CreateMessage(ctx, msg)
	}

	t.Run("get all", func(t *testing.T) {
		msgs, err := session.GetMessages(ctx, 100, 0)
		assert.NoError(t, err)
		assert.Len(t, msgs, 10)
	})

	t.Run("with limit", func(t *testing.T) {
		msgs, err := session.GetMessages(ctx, 5, 0)
		assert.NoError(t, err)
		assert.Len(t, msgs, 5)
	})

	t.Run("with offset", func(t *testing.T) {
		msgs, err := session.GetMessages(ctx, 5, 3)
		assert.NoError(t, err)
		assert.Len(t, msgs, 5)
		assert.Equal(t, int64(4), msgs[0].MessageID)
	})

	t.Run("limit and offset", func(t *testing.T) {
		msgs, err := session.GetMessages(ctx, 3, 2)
		assert.NoError(t, err)
		assert.Len(t, msgs, 3)
		assert.Equal(t, int64(3), msgs[0].MessageID)
	})
}

func TestMemorySession_CompactMessages(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := context.Background()

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")
	msg4 := newTestMessage(4, "fine")

	session.CreateMessage(ctx, msg1)
	session.CreateMessage(ctx, msg2)
	session.CreateMessage(ctx, msg3)
	session.CreateMessage(ctx, msg4)

	summaryMsg := newTestMessage(100, "summary")

	// Archive msg1 and msg2; keep msg3 and msg4 as structured messages.
	err = session.CompactMessages(ctx, 3, summaryMsg)
	assert.NoError(t, err)

	// Active history: kept messages + summary appended.
	msgs, err := session.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.Equal(t, int64(3), msgs[0].MessageID)
	assert.Equal(t, int64(4), msgs[1].MessageID)
	assert.Equal(t, int64(100), msgs[2].MessageID)
}

func TestMemorySession_CompactMessages_ArchiveAll(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := context.Background()

	session.CreateMessage(ctx, newTestMessage(1, "hello"))
	session.CreateMessage(ctx, newTestMessage(2, "hi"))

	summaryMsg := newTestMessage(100, "summary")

	// splitMessageID=0 archives everything.
	err = session.CompactMessages(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].MessageID)
}

func TestMemorySession_CompactMessages_Empty(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	session := NewMemorySession(sessionID)
	ctx := context.Background()

	summaryMsg := newTestMessage(100, "summary")

	// Compacting an empty session (splitMessageID=0) just inserts the summary.
	err = session.CompactMessages(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.GetMessages(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].MessageID)

}

func TestMemorySession_ListMessages(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	sess := NewMemorySession(sessionID)
	ctx := context.Background()

	for i := int64(1); i <= 5; i++ {
		sess.CreateMessage(ctx, newTestMessage(i, "msg"))
	}

	msgs, err := sess.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 5)
}

func TestMemorySession_CompactMessages_MultipleRounds(t *testing.T) {
	snowflaker, err := snowflake.New()
	assert.Nil(t, err)
	sessionID := snowflaker.Generate().Int64()

	sess := NewMemorySession(sessionID)
	ctx := context.Background()

	sess.CreateMessage(ctx, newTestMessage(1, "a"))
	sess.CreateMessage(ctx, newTestMessage(2, "b"))

	// First compaction: archive all (splitMessageID=0), insert summary1.
	err = sess.CompactMessages(ctx, 0, newTestMessage(10, "summary1"))
	assert.NoError(t, err)

	sess.CreateMessage(ctx, newTestMessage(3, "c"))

	// Second compaction: archive summary1+c, insert summary2.
	err = sess.CompactMessages(ctx, 0, newTestMessage(20, "summary2"))
	assert.NoError(t, err)

	// Active: only summary2.
	active, err := sess.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, int64(20), active[0].MessageID)
}
