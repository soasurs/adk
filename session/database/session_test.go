package database

import (
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/internal/snowflake"
	"github.com/soasurs/adk/session/message"
)

func setupTestDB(t *testing.T) *sqlx.DB {
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE sessions (
			session_id INTEGER PRIMARY KEY,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			deleted_at INTEGER NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE messages (
			message_id        INTEGER PRIMARY KEY,
			session_id        INTEGER NOT NULL,
			role              TEXT    NOT NULL DEFAULT '',
			name              TEXT    NOT NULL DEFAULT '',
			content           TEXT    NOT NULL DEFAULT '',
			reasoning_content TEXT    NOT NULL DEFAULT '',
			tool_calls        TEXT    NOT NULL DEFAULT '[]',
			tool_call_id      TEXT    NOT NULL DEFAULT '',
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			created_at        INTEGER NOT NULL,
			updated_at        INTEGER NOT NULL,
			compacted_at      INTEGER NOT NULL DEFAULT 0,
			deleted_at        INTEGER NOT NULL
		)
	`)
	require.NoError(t, err)

	return db
}

func newTestMessage(id int64, content string) *message.Message {
	return &message.Message{
		MessageID: id,
		Content:   content,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
}

func TestDatabaseSession_CreateMessage(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)
	require.NotNil(t, session)

	msg := newTestMessage(1, "hello")
	err = session.CreateMessage(ctx, msg)
	assert.NoError(t, err)

	msgs, err := session.GetMessages(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(1), msgs[0].MessageID)
}

func TestDatabaseSession_DeleteMessage(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")

	require.NoError(t, session.CreateMessage(ctx, msg1))
	require.NoError(t, session.CreateMessage(ctx, msg2))
	require.NoError(t, session.CreateMessage(ctx, msg3))

	err = session.DeleteMessage(ctx, 2)
	assert.NoError(t, err)

	msgs, err := session.GetMessages(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)

	for _, m := range msgs {
		assert.NotEqual(t, int64(2), m.MessageID)
	}
}

func TestDatabaseSession_GetMessages(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	for i := int64(1); i <= 10; i++ {
		msg := newTestMessage(i, "msg")
		require.NoError(t, session.CreateMessage(ctx, msg))
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

func TestDatabaseSession_CompactMessages(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	msg1 := newTestMessage(1, "hello")
	msg2 := newTestMessage(2, "hi")
	msg3 := newTestMessage(3, "how are you")
	msg4 := newTestMessage(4, "fine")

	require.NoError(t, session.CreateMessage(ctx, msg1))
	require.NoError(t, session.CreateMessage(ctx, msg2))
	require.NoError(t, session.CreateMessage(ctx, msg3))
	require.NoError(t, session.CreateMessage(ctx, msg4))

	summaryMsg := newTestMessage(100, "summary")
	summaryMsg.Role = "system"

	// Archive msg1 and msg2; keep msg3 and msg4 as structured messages.
	err = session.CompactMessages(ctx, 3, summaryMsg)
	assert.NoError(t, err)

	// Active history: kept messages + summary (ordered by created_at ASC).
	msgs, err := session.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.Equal(t, int64(3), msgs[0].MessageID)
	assert.Equal(t, int64(4), msgs[1].MessageID)
	assert.Equal(t, int64(100), msgs[2].MessageID)

}

func TestDatabaseSession_CompactMessages_ArchiveAll(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	require.NoError(t, session.CreateMessage(ctx, newTestMessage(1, "hello")))
	require.NoError(t, session.CreateMessage(ctx, newTestMessage(2, "hi")))

	summaryMsg := newTestMessage(100, "summary")

	// splitMessageID=0 archives all.
	err = session.CompactMessages(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].MessageID)
}

func TestDatabaseSession_CompactMessages_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	summaryMsg := newTestMessage(100, "summary")

	// Compacting an empty session (splitMessageID=0) just inserts the summary.
	err = session.CompactMessages(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.GetMessages(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].MessageID)

}

func TestDatabaseSession_ListMessages(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	for i := int64(1); i <= 5; i++ {
		require.NoError(t, session.CreateMessage(ctx, newTestMessage(i, "msg")))
	}

	msgs, err := session.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 5)
}

func TestDatabaseSession_IsolatesMessagesBySession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := t.Context()
	s1, err := NewDatabaseSession(ctx, db, 1)
	require.NoError(t, err)
	s2, err := NewDatabaseSession(ctx, db, 2)
	require.NoError(t, err)

	require.NoError(t, s1.CreateMessage(ctx, newTestMessage(1, "session one")))
	require.NoError(t, s2.CreateMessage(ctx, newTestMessage(2, "session two")))

	msgs1, err := s1.ListMessages(ctx)
	require.NoError(t, err)
	require.Len(t, msgs1, 1)
	assert.Equal(t, int64(1), msgs1[0].SessionID)
	assert.Equal(t, "session one", msgs1[0].Content)

	msgs2, err := s2.ListMessages(ctx)
	require.NoError(t, err)
	require.Len(t, msgs2, 1)
	assert.Equal(t, int64(2), msgs2[0].SessionID)
	assert.Equal(t, "session two", msgs2[0].Content)
}

func TestDatabaseSession_CompactMessages_MultipleRounds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	require.NoError(t, sess.CreateMessage(ctx, newTestMessage(1, "a")))
	require.NoError(t, sess.CreateMessage(ctx, newTestMessage(2, "b")))

	// First compaction: archive all, insert summary1.
	err = sess.CompactMessages(ctx, 0, newTestMessage(10, "summary1"))
	require.NoError(t, err)

	require.NoError(t, sess.CreateMessage(ctx, newTestMessage(3, "c")))

	// Second compaction: archive summary1+c, insert summary2.
	err = sess.CompactMessages(ctx, 0, newTestMessage(20, "summary2"))
	require.NoError(t, err)

	// Active: only summary2.
	active, err := sess.ListMessages(ctx)
	assert.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, int64(20), active[0].MessageID)
}

func TestDatabaseSession_GetSessionID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	assert.Equal(t, sessionID, session.GetSessionID())
}
