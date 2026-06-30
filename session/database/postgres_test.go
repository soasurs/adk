package database

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/event"
)

// Integration tests (require ADK_TEST_POSTGRES_DSN)
// ───────────────────────────────────────────

var postgresTestCounter atomic.Uint64

type postgresFixture struct {
	db      *sqlx.DB
	service session.SessionService
	prefix  string
	opts    []Option
}

func newPostgresFixture(t *testing.T) *postgresFixture {
	t.Helper()

	dsn := os.Getenv("ADK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ADK_TEST_POSTGRES_DSN not set")
	}

	db, err := sqlx.ConnectContext(t.Context(), "pgx", dsn)
	require.NoError(t, err)

	prefix := fmt.Sprintf("adk_pg_%d_", postgresTestCounter.Add(1))
	opts := []Option{WithTablePrefix(prefix)}
	require.NoError(t, InitSchema(t.Context(), db, opts...))

	service, err := NewDatabaseSessionService(db, opts...)
	require.NoError(t, err)

	f := &postgresFixture{
		db:      db,
		service: service,
		prefix:  prefix,
		opts:    opts,
	}
	t.Cleanup(func() {
		cleanupPostgresTables(t, db, prefix)
		assert.NoError(t, db.Close())
	})
	return f
}

func (f *postgresFixture) createSession(t *testing.T, sessionID string) session.Session {
	t.Helper()
	sess, err := f.service.CreateSession(t.Context(), session.CreateSessionRequest{SessionID: sessionID})
	require.NoError(t, err)
	return sess
}

func postgresEvent(id int64, content string) *event.Event {
	return &event.Event{
		EventID:   id,
		Role:      string(model.RoleAssistant),
		Content:   content,
		CreatedAt: 1_781_289_412_066 + id,
		UpdatedAt: 1_781_289_412_066 + id,
	}
}

func eventIDs(events []*event.Event) []int64 {
	ids := make([]int64, len(events))
	for i, ev := range events {
		ids[i] = ev.EventID
	}
	return ids
}

func TestPostgres_InitSchema(t *testing.T) {
	t.Run("is idempotent", func(t *testing.T) {
		f := newPostgresFixture(t)
		require.NoError(t, InitSchema(t.Context(), f.db, f.opts...))

		var versions []int
		err := f.db.SelectContext(
			t.Context(),
			&versions,
			"SELECT version FROM "+f.prefix+"schema_migrations ORDER BY version",
		)
		require.NoError(t, err)
		assert.Equal(t, []int{1}, versions)
	})

	t.Run("creates expected column types", func(t *testing.T) {
		f := newPostgresFixture(t)

		var rows []struct {
			Name     string `db:"column_name"`
			DataType string `db:"data_type"`
		}
		err := f.db.SelectContext(
			t.Context(),
			&rows,
			`
				SELECT column_name, data_type
				FROM information_schema.columns
				WHERE table_schema = current_schema()
					AND table_name = $1
			`,
			f.prefix+"events",
		)
		require.NoError(t, err)

		columns := make(map[string]string, len(rows))
		for _, row := range rows {
			columns[row.Name] = row.DataType
		}
		for _, name := range []string{
			"event_id",
			"prompt_tokens",
			"completion_tokens",
			"total_tokens",
			"created_at",
			"updated_at",
			"compacted_at",
			"deleted_at",
		} {
			assert.Equal(t, "bigint", columns[name], name)
		}
		assert.Equal(t, "text", columns["session_id"])
	})

	t.Run("supports custom table names", func(t *testing.T) {
		dsn := os.Getenv("ADK_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("ADK_TEST_POSTGRES_DSN not set")
		}
		db, err := sqlx.ConnectContext(t.Context(), "pgx", dsn)
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupPostgresTables(t, db, "adk_custom_")
			assert.NoError(t, db.Close())
		})

		opts := []Option{
			WithSessionsTable("adk_custom_sessions"),
			WithEventsTable("adk_custom_events"),
			WithMigrationsTable("adk_custom_schema_migrations"),
		}
		require.NoError(t, InitSchema(t.Context(), db, opts...))
		service, err := NewDatabaseSessionService(db, opts...)
		require.NoError(t, err)
		_, err = service.CreateSession(t.Context(), session.CreateSessionRequest{SessionID: "session-1"})
		require.NoError(t, err)
	})
}

func TestPostgres_DatabaseSessionService_CreateSession(t *testing.T) {
	t.Run("creates and returns an application-provided session id", func(t *testing.T) {
		f := newPostgresFixture(t)
		const sessionID = "external-session-1"

		sess, err := f.service.CreateSession(t.Context(), session.CreateSessionRequest{
			SessionID: sessionID,
			AppID:     "chat",
			UserID:    "user-1",
		})
		require.NoError(t, err)
		assert.Equal(t, sessionID, sess.GetSessionID())
		assert.Equal(t, "chat", sess.GetAppID())
		assert.Equal(t, "user-1", sess.GetUserID())
	})

	t.Run("rejects a duplicate session id", func(t *testing.T) {
		f := newPostgresFixture(t)
		_, err := f.service.CreateSession(t.Context(), session.CreateSessionRequest{SessionID: "session-1"})
		require.NoError(t, err)

		_, err = f.service.CreateSession(t.Context(), session.CreateSessionRequest{SessionID: "session-1"})
		assert.Error(t, err)
	})

	t.Run("creates independent sessions", func(t *testing.T) {
		f := newPostgresFixture(t)
		first := f.createSession(t, "session-1")
		second := f.createSession(t, "session-2")

		assert.Equal(t, "session-1", first.GetSessionID())
		assert.Equal(t, "session-2", second.GetSessionID())
	})
}

func TestPostgres_DatabaseSessionService_GetSession(t *testing.T) {
	t.Run("returns an existing session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, "session-1")

		sess, err := f.service.GetSession(t.Context(), "session-1")
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, "session-1", sess.GetSessionID())
	})

	t.Run("returns nil for a missing session", func(t *testing.T) {
		f := newPostgresFixture(t)

		sess, err := f.service.GetSession(t.Context(), "missing")
		require.NoError(t, err)
		assert.Nil(t, sess)
	})

	t.Run("returns nil for a deleted session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, "session-1")
		require.NoError(t, f.service.DeleteSession(t.Context(), "session-1"))

		sess, err := f.service.GetSession(t.Context(), "session-1")
		require.NoError(t, err)
		assert.Nil(t, sess)
	})
}

func TestPostgres_DatabaseSessionService_DeleteSession(t *testing.T) {
	t.Run("soft deletes an existing session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, "session-1")

		require.NoError(t, f.service.DeleteSession(t.Context(), "session-1"))

		var deletedAt int64
		err := f.db.GetContext(
			t.Context(),
			&deletedAt,
			"SELECT deleted_at FROM "+f.prefix+"sessions WHERE session_id = $1",
			"session-1",
		)
		require.NoError(t, err)
		assert.Positive(t, deletedAt)
	})

	t.Run("is a no-op for a missing session", func(t *testing.T) {
		f := newPostgresFixture(t)
		assert.NoError(t, f.service.DeleteSession(t.Context(), "missing"))
	})

	t.Run("does not delete another session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, "session-1")
		f.createSession(t, "session-2")

		require.NoError(t, f.service.DeleteSession(t.Context(), "session-1"))

		sess, err := f.service.GetSession(t.Context(), "session-2")
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, "session-2", sess.GetSessionID())
	})
}

func TestPostgres_DatabaseSession_CreateEvent(t *testing.T) {
	t.Run("round trips every persisted field", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		ev := &event.Event{
			EventID: 5_000_000_001,
			Author:  "researcher",
			Role:    string(model.RoleAssistant),
			Content: "answer",
			Parts: event.Parts{
				{Type: model.ContentPartTypeText, Text: "text"},
				{
					Type:        model.ContentPartTypeImageBase64,
					ImageBase64: "aW1hZ2U=",
					MIMEType:    "image/png",
				},
			},
			ReasoningContent: "reasoning",
			ToolCalls: event.ToolCalls{
				{
					ID:               "call-1",
					Name:             "lookup",
					Arguments:        `{"query":"weather"}`,
					ThoughtSignature: []byte{0x01, 0x02, 0xff},
				},
			},
			ToolCallID:       "parent-call",
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
			CreatedAt:        1_781_289_412_066,
			UpdatedAt:        1_781_289_412_067,
		}

		require.NoError(t, sess.CreateEvent(t.Context(), ev))
		events, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		require.Len(t, events, 1)

		got := events[0]
		assert.Equal(t, "session-1", got.SessionID)
		assert.Equal(t, ev.EventID, got.EventID)
		assert.Equal(t, ev.Author, got.Author)
		assert.Equal(t, ev.Role, got.Role)
		assert.Equal(t, ev.Content, got.Content)
		assert.Equal(t, ev.Parts, got.Parts)
		assert.Equal(t, ev.ReasoningContent, got.ReasoningContent)
		assert.Equal(t, ev.ToolCalls, got.ToolCalls)
		assert.Equal(t, ev.ToolCallID, got.ToolCallID)
		assert.Equal(t, ev.PromptTokens, got.PromptTokens)
		assert.Equal(t, ev.CompletionTokens, got.CompletionTokens)
		assert.Equal(t, ev.TotalTokens, got.TotalTokens)
		assert.Equal(t, ev.CreatedAt, got.CreatedAt)
		assert.Equal(t, ev.UpdatedAt, got.UpdatedAt)
	})

	t.Run("rejects a duplicate event id", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(1, "first")))

		err := sess.CreateEvent(t.Context(), postgresEvent(1, "duplicate"))
		assert.Error(t, err)
	})

	t.Run("assigns the owning session id", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-42")
		ev := postgresEvent(1, "event")
		ev.SessionID = "wrong-session"

		require.NoError(t, sess.CreateEvent(t.Context(), ev))
		assert.Equal(t, "session-42", ev.SessionID)
	})
}

func TestPostgres_DatabaseSession_DeleteEvent(t *testing.T) {
	t.Run("hides the deleted event", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(1, "first")))
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(2, "second")))

		require.NoError(t, sess.DeleteEvent(t.Context(), 1))
		events, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{2}, eventIDs(events))
	})

	t.Run("sets deleted_at instead of removing the row", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(1, "event")))

		require.NoError(t, sess.DeleteEvent(t.Context(), 1))

		var deletedAt int64
		err := f.db.GetContext(
			t.Context(),
			&deletedAt,
			"SELECT deleted_at FROM "+f.prefix+"events WHERE event_id = $1",
			1,
		)
		require.NoError(t, err)
		assert.Positive(t, deletedAt)
	})

	t.Run("is a no-op for a missing event", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		assert.NoError(t, sess.DeleteEvent(t.Context(), 999))
	})
}

func TestPostgres_DatabaseSession_GetEvents(t *testing.T) {
	t.Run("applies limit and offset", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		for id := int64(1); id <= 6; id++ {
			require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(id, "event")))
		}

		tests := []struct {
			name   string
			limit  int64
			offset int64
			want   []int64
		}{
			{name: "all", limit: 100, offset: 0, want: []int64{1, 2, 3, 4, 5, 6}},
			{name: "limit", limit: 2, offset: 0, want: []int64{1, 2}},
			{name: "offset", limit: 100, offset: 3, want: []int64{4, 5, 6}},
			{name: "limit and offset", limit: 2, offset: 2, want: []int64{3, 4}},
			{name: "past end", limit: 2, offset: 10, want: []int64{}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				events, err := sess.GetEvents(t.Context(), tc.limit, tc.offset)
				require.NoError(t, err)
				assert.Equal(t, tc.want, eventIDs(events))
			})
		}
	})

	t.Run("uses event id to break timestamp ties", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		for _, id := range []int64{3, 1, 2} {
			ev := postgresEvent(id, "event")
			ev.CreatedAt = 1000
			require.NoError(t, sess.CreateEvent(t.Context(), ev))
		}

		events, err := sess.GetEvents(t.Context(), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2, 3}, eventIDs(events))
	})

	t.Run("excludes deleted and compacted events", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		for id := int64(1); id <= 4; id++ {
			require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(id, "event")))
		}
		require.NoError(t, sess.DeleteEvent(t.Context(), 2))
		require.NoError(t, sess.CompactEvents(t.Context(), 3, postgresEvent(10, "summary")))

		events, err := sess.GetEvents(t.Context(), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, []int64{3, 4, 10}, eventIDs(events))
	})

	t.Run("isolates sessions", func(t *testing.T) {
		f := newPostgresFixture(t)
		first := f.createSession(t, "session-1")
		second := f.createSession(t, "session-2")
		require.NoError(t, first.CreateEvent(t.Context(), postgresEvent(1, "first")))
		require.NoError(t, second.CreateEvent(t.Context(), postgresEvent(2, "second")))

		firstEvents, err := first.GetEvents(t.Context(), 10, 0)
		require.NoError(t, err)
		secondEvents, err := second.GetEvents(t.Context(), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, []int64{1}, eventIDs(firstEvents))
		assert.Equal(t, []int64{2}, eventIDs(secondEvents))
	})
}

func TestPostgres_DatabaseSession_ListEvents(t *testing.T) {
	t.Run("returns every active event", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		for id := int64(1); id <= 5; id++ {
			require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(id, "event")))
		}

		events, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2, 3, 4, 5}, eventIDs(events))
	})

	t.Run("returns an empty slice for an empty session", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")

		events, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		assert.Empty(t, events)
		assert.NotNil(t, events)
	})
}

func TestPostgres_DatabaseSession_CompactEvents(t *testing.T) {
	t.Run("archives events before the split point", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		for id := int64(1); id <= 4; id++ {
			require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(id, "event")))
		}

		require.NoError(t, sess.CompactEvents(t.Context(), 3, postgresEvent(10, "summary")))

		active, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{3, 4, 10}, eventIDs(active))

		compacted, err := sess.(*databaseSession).ListCompactedEvents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2}, eventIDs(compacted))
	})

	t.Run("archives all events when split id is zero", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(1, "first")))
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(2, "second")))

		require.NoError(t, sess.CompactEvents(t.Context(), 0, postgresEvent(10, "summary")))

		active, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{10}, eventIDs(active))
	})

	t.Run("inserts a summary into an empty session", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")

		require.NoError(t, sess.CompactEvents(t.Context(), 0, postgresEvent(10, "summary")))

		active, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{10}, eventIDs(active))
	})

	t.Run("supports repeated compaction", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(1, "first")))
		require.NoError(t, sess.CompactEvents(t.Context(), 0, postgresEvent(10, "summary one")))
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(2, "second")))
		require.NoError(t, sess.CompactEvents(t.Context(), 0, postgresEvent(20, "summary two")))

		active, err := sess.ListEvents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{20}, eventIDs(active))
	})

	t.Run("rolls back archival when summary insertion fails", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, "session-1")
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(1, "first")))
		require.NoError(t, sess.CreateEvent(t.Context(), postgresEvent(2, "second")))

		err := sess.CompactEvents(t.Context(), 0, postgresEvent(2, "duplicate summary id"))
		require.Error(t, err)

		active, listErr := sess.ListEvents(t.Context())
		require.NoError(t, listErr)
		assert.Equal(t, []int64{1, 2}, eventIDs(active))
	})
}

func cleanupPostgresTables(t *testing.T, db *sqlx.DB, prefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `
		DROP TABLE IF EXISTS `+prefix+`events;
		DROP TABLE IF EXISTS `+prefix+`sessions;
		DROP TABLE IF EXISTS `+prefix+`schema_migrations;
	`)
	require.NoError(t, err)
}
