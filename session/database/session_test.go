package database

import (
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/internal/snowflake"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session/event"
)

func setupTestDB(t *testing.T) *sqlx.DB {
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)

	err = InitSchema(t.Context(), db)
	require.NoError(t, err)

	return db
}

func newTestMessage(id int64, content string) *event.Event {
	return &event.Event{
		EventID:   id,
		Content:   content,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
}

func TestDatabaseSession_CreateEvent(t *testing.T) {
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
	err = session.CreateEvent(ctx, msg)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(1), msgs[0].EventID)
}

func TestDatabaseSession_DeleteEvent(t *testing.T) {
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

	require.NoError(t, session.CreateEvent(ctx, msg1))
	require.NoError(t, session.CreateEvent(ctx, msg2))
	require.NoError(t, session.CreateEvent(ctx, msg3))

	err = session.DeleteEvent(ctx, 2)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)

	for _, m := range msgs {
		assert.NotEqual(t, int64(2), m.EventID)
	}
}

func TestDatabaseSession_GetEvents(t *testing.T) {
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
		require.NoError(t, session.CreateEvent(ctx, msg))
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

func TestDatabaseSession_GetEvents_StableOrder(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, 1)
	require.NoError(t, err)

	const createdAt = int64(1234)
	for _, id := range []int64{3, 1, 2} {
		msg := newTestMessage(id, "msg")
		msg.CreatedAt = createdAt
		require.NoError(t, sess.CreateEvent(ctx, msg))
	}

	msgs, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, []int64{1, 2, 3}, []int64{
		msgs[0].EventID,
		msgs[1].EventID,
		msgs[2].EventID,
	})
}

func TestDatabaseSession_ToolCallThoughtSignature_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, 1)
	require.NoError(t, err)

	msg := newTestMessage(1, "")
	msg.Role = string(model.RoleAssistant)
	msg.ToolCalls = event.ToolCalls{
		{
			ID:               "call-1",
			Name:             "lookup",
			Arguments:        `{"query":"weather"}`,
			ThoughtSignature: []byte{0x01, 0x02, 0xff},
		},
	}
	require.NoError(t, sess.CreateEvent(ctx, msg))

	msgs, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].ToolCalls, 1)
	assert.Equal(t, msg.ToolCalls[0], msgs[0].ToolCalls[0])
}

func TestDatabaseSession_CompactEvents(t *testing.T) {
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

	require.NoError(t, session.CreateEvent(ctx, msg1))
	require.NoError(t, session.CreateEvent(ctx, msg2))
	require.NoError(t, session.CreateEvent(ctx, msg3))
	require.NoError(t, session.CreateEvent(ctx, msg4))

	summaryMsg := newTestMessage(100, "summary")
	summaryMsg.Role = "system"

	// Archive msg1 and msg2; keep msg3 and msg4 as structured messages.
	err = session.CompactEvents(ctx, 3, summaryMsg)
	assert.NoError(t, err)

	// Active history: kept messages + summary (ordered by created_at ASC).
	msgs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.Equal(t, int64(3), msgs[0].EventID)
	assert.Equal(t, int64(4), msgs[1].EventID)
	assert.Equal(t, int64(100), msgs[2].EventID)

}

func TestDatabaseSession_CompactEvents_ArchiveAll(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	require.NoError(t, session.CreateEvent(ctx, newTestMessage(1, "hello")))
	require.NoError(t, session.CreateEvent(ctx, newTestMessage(2, "hi")))

	summaryMsg := newTestMessage(100, "summary")

	// splitEventID=0 archives all.
	err = session.CompactEvents(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].EventID)
}

func TestDatabaseSession_CompactEvents_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	summaryMsg := newTestMessage(100, "summary")

	// Compacting an empty session (splitEventID=0) just inserts the summary.
	err = session.CompactEvents(ctx, 0, summaryMsg)
	assert.NoError(t, err)

	msgs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(100), msgs[0].EventID)

}

func TestDatabaseSession_ListEvents(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	for i := int64(1); i <= 5; i++ {
		require.NoError(t, session.CreateEvent(ctx, newTestMessage(i, "msg")))
	}

	msgs, err := session.ListEvents(ctx)
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

	require.NoError(t, s1.CreateEvent(ctx, newTestMessage(1, "session one")))
	require.NoError(t, s2.CreateEvent(ctx, newTestMessage(2, "session two")))

	msgs1, err := s1.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, msgs1, 1)
	assert.Equal(t, int64(1), msgs1[0].SessionID)
	assert.Equal(t, "session one", msgs1[0].Content)

	msgs2, err := s2.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, msgs2, 1)
	assert.Equal(t, int64(2), msgs2[0].SessionID)
	assert.Equal(t, "session two", msgs2[0].Content)
}

func TestDatabaseSession_CompactEvents_MultipleRounds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	require.NoError(t, sess.CreateEvent(ctx, newTestMessage(1, "a")))
	require.NoError(t, sess.CreateEvent(ctx, newTestMessage(2, "b")))

	// First compaction: archive all, insert summary1.
	err = sess.CompactEvents(ctx, 0, newTestMessage(10, "summary1"))
	require.NoError(t, err)

	require.NoError(t, sess.CreateEvent(ctx, newTestMessage(3, "c")))

	// Second compaction: archive summary1+c, insert summary2.
	err = sess.CompactEvents(ctx, 0, newTestMessage(20, "summary2"))
	require.NoError(t, err)

	// Active: only summary2.
	active, err := sess.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, int64(20), active[0].EventID)
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

// TestDatabaseSession_Parts_RoundTrip verifies that ContentParts are written to the
// database and read back intact, preserving all fields.
func TestDatabaseSession_Parts_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, sessionID)
	require.NoError(t, err)

	parts := event.Parts{
		{Type: model.ContentPartTypeText, Text: "what is in this image?"},
		{
			Type:        model.ContentPartTypeImageURL,
			ImageURL:    "https://example.com/photo.jpg",
			ImageDetail: model.ImageDetailHigh,
		},
		{
			Type:        model.ContentPartTypeImageBase64,
			ImageBase64: "iVBORw0KGgo=",
			MIMEType:    "image/png",
		},
	}
	msg := &event.Event{
		EventID:   1,
		Role:      string(model.RoleUser),
		Parts:     parts,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}

	require.NoError(t, sess.CreateEvent(ctx, msg))

	stored, err := sess.GetEvents(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, stored, 1)

	got := stored[0].Parts
	require.Len(t, got, 3)
	assert.Equal(t, model.ContentPartTypeText, got[0].Type)
	assert.Equal(t, "what is in this image?", got[0].Text)
	assert.Equal(t, model.ContentPartTypeImageURL, got[1].Type)
	assert.Equal(t, "https://example.com/photo.jpg", got[1].ImageURL)
	assert.Equal(t, model.ImageDetailHigh, got[1].ImageDetail)
	assert.Equal(t, model.ContentPartTypeImageBase64, got[2].Type)
	assert.Equal(t, "iVBORw0KGgo=", got[2].ImageBase64)
	assert.Equal(t, "image/png", got[2].MIMEType)

	// Verify round-trip through ToModel.
	modelEvent := stored[0].ToModel()
	require.Len(t, modelEvent.Content.Parts, 3)
	assert.Equal(t, "what is in this image?", modelEvent.Content.Parts[0].Text)
}
