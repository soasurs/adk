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
	"github.com/soasurs/adk/session/message"
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

func (f *postgresFixture) createSession(t *testing.T, sessionID int64) session.Session {
	t.Helper()
	sess, err := f.service.CreateSession(t.Context(), sessionID)
	require.NoError(t, err)
	return sess
}

func postgresMessage(id int64, content string) *message.Message {
	return &message.Message{
		MessageID: id,
		Role:      string(model.RoleAssistant),
		Content:   content,
		CreatedAt: 1_781_289_412_066 + id,
		UpdatedAt: 1_781_289_412_066 + id,
	}
}

func messageIDs(messages []*message.Message) []int64 {
	ids := make([]int64, len(messages))
	for i, msg := range messages {
		ids[i] = msg.MessageID
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

	t.Run("creates bigint columns", func(t *testing.T) {
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
			f.prefix+"messages",
		)
		require.NoError(t, err)

		columns := make(map[string]string, len(rows))
		for _, row := range rows {
			columns[row.Name] = row.DataType
		}
		for _, name := range []string{
			"message_id",
			"session_id",
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
			WithMessagesTable("adk_custom_messages"),
			WithMigrationsTable("adk_custom_schema_migrations"),
		}
		require.NoError(t, InitSchema(t.Context(), db, opts...))
		service, err := NewDatabaseSessionService(db, opts...)
		require.NoError(t, err)
		_, err = service.CreateSession(t.Context(), 1)
		require.NoError(t, err)
	})
}

func TestPostgres_DatabaseSessionService_CreateSession(t *testing.T) {
	t.Run("creates and returns a 64 bit session id", func(t *testing.T) {
		f := newPostgresFixture(t)
		const sessionID = int64(5_000_000_000)

		sess, err := f.service.CreateSession(t.Context(), sessionID)
		require.NoError(t, err)
		assert.Equal(t, sessionID, sess.GetSessionID())
	})

	t.Run("rejects a duplicate session id", func(t *testing.T) {
		f := newPostgresFixture(t)
		_, err := f.service.CreateSession(t.Context(), 1)
		require.NoError(t, err)

		_, err = f.service.CreateSession(t.Context(), 1)
		assert.Error(t, err)
	})

	t.Run("creates independent sessions", func(t *testing.T) {
		f := newPostgresFixture(t)
		first := f.createSession(t, 1)
		second := f.createSession(t, 2)

		assert.Equal(t, int64(1), first.GetSessionID())
		assert.Equal(t, int64(2), second.GetSessionID())
	})
}

func TestPostgres_DatabaseSessionService_GetSession(t *testing.T) {
	t.Run("returns an existing session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, 1)

		sess, err := f.service.GetSession(t.Context(), 1)
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, int64(1), sess.GetSessionID())
	})

	t.Run("returns nil for a missing session", func(t *testing.T) {
		f := newPostgresFixture(t)

		sess, err := f.service.GetSession(t.Context(), 999)
		require.NoError(t, err)
		assert.Nil(t, sess)
	})

	t.Run("returns nil for a deleted session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, 1)
		require.NoError(t, f.service.DeleteSession(t.Context(), 1))

		sess, err := f.service.GetSession(t.Context(), 1)
		require.NoError(t, err)
		assert.Nil(t, sess)
	})
}

func TestPostgres_DatabaseSessionService_DeleteSession(t *testing.T) {
	t.Run("soft deletes an existing session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, 1)

		require.NoError(t, f.service.DeleteSession(t.Context(), 1))

		var deletedAt int64
		err := f.db.GetContext(
			t.Context(),
			&deletedAt,
			"SELECT deleted_at FROM "+f.prefix+"sessions WHERE session_id = $1",
			1,
		)
		require.NoError(t, err)
		assert.Positive(t, deletedAt)
	})

	t.Run("is a no-op for a missing session", func(t *testing.T) {
		f := newPostgresFixture(t)
		assert.NoError(t, f.service.DeleteSession(t.Context(), 999))
	})

	t.Run("does not delete another session", func(t *testing.T) {
		f := newPostgresFixture(t)
		f.createSession(t, 1)
		f.createSession(t, 2)

		require.NoError(t, f.service.DeleteSession(t.Context(), 1))

		sess, err := f.service.GetSession(t.Context(), 2)
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, int64(2), sess.GetSessionID())
	})
}

func TestPostgres_DatabaseSession_CreateMessage(t *testing.T) {
	t.Run("round trips every persisted field", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		msg := &message.Message{
			MessageID: 5_000_000_001,
			Role:      string(model.RoleAssistant),
			Name:      "researcher",
			Content:   "answer",
			Parts: message.Parts{
				{Type: model.ContentPartTypeText, Text: "text"},
				{
					Type:        model.ContentPartTypeImageBase64,
					ImageBase64: "aW1hZ2U=",
					MIMEType:    "image/png",
				},
			},
			ReasoningContent: "reasoning",
			ToolCalls: message.ToolCalls{
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

		require.NoError(t, sess.CreateMessage(t.Context(), msg))
		messages, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		require.Len(t, messages, 1)

		got := messages[0]
		assert.Equal(t, int64(1), got.SessionID)
		assert.Equal(t, msg.MessageID, got.MessageID)
		assert.Equal(t, msg.Role, got.Role)
		assert.Equal(t, msg.Name, got.Name)
		assert.Equal(t, msg.Content, got.Content)
		assert.Equal(t, msg.Parts, got.Parts)
		assert.Equal(t, msg.ReasoningContent, got.ReasoningContent)
		assert.Equal(t, msg.ToolCalls, got.ToolCalls)
		assert.Equal(t, msg.ToolCallID, got.ToolCallID)
		assert.Equal(t, msg.PromptTokens, got.PromptTokens)
		assert.Equal(t, msg.CompletionTokens, got.CompletionTokens)
		assert.Equal(t, msg.TotalTokens, got.TotalTokens)
		assert.Equal(t, msg.CreatedAt, got.CreatedAt)
		assert.Equal(t, msg.UpdatedAt, got.UpdatedAt)
	})

	t.Run("rejects a duplicate message id", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(1, "first")))

		err := sess.CreateMessage(t.Context(), postgresMessage(1, "duplicate"))
		assert.Error(t, err)
	})

	t.Run("assigns the owning session id", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 42)
		msg := postgresMessage(1, "message")
		msg.SessionID = 999

		require.NoError(t, sess.CreateMessage(t.Context(), msg))
		assert.Equal(t, int64(42), msg.SessionID)
	})
}

func TestPostgres_DatabaseSession_DeleteMessage(t *testing.T) {
	t.Run("hides the deleted message", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(1, "first")))
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(2, "second")))

		require.NoError(t, sess.DeleteMessage(t.Context(), 1))
		messages, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{2}, messageIDs(messages))
	})

	t.Run("sets deleted_at instead of removing the row", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(1, "message")))

		require.NoError(t, sess.DeleteMessage(t.Context(), 1))

		var deletedAt int64
		err := f.db.GetContext(
			t.Context(),
			&deletedAt,
			"SELECT deleted_at FROM "+f.prefix+"messages WHERE message_id = $1",
			1,
		)
		require.NoError(t, err)
		assert.Positive(t, deletedAt)
	})

	t.Run("is a no-op for a missing message", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		assert.NoError(t, sess.DeleteMessage(t.Context(), 999))
	})
}

func TestPostgres_DatabaseSession_GetMessages(t *testing.T) {
	t.Run("applies limit and offset", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		for id := int64(1); id <= 6; id++ {
			require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(id, "message")))
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
				messages, err := sess.GetMessages(t.Context(), tc.limit, tc.offset)
				require.NoError(t, err)
				assert.Equal(t, tc.want, messageIDs(messages))
			})
		}
	})

	t.Run("uses message id to break timestamp ties", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		for _, id := range []int64{3, 1, 2} {
			msg := postgresMessage(id, "message")
			msg.CreatedAt = 1000
			require.NoError(t, sess.CreateMessage(t.Context(), msg))
		}

		messages, err := sess.GetMessages(t.Context(), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2, 3}, messageIDs(messages))
	})

	t.Run("excludes deleted and compacted messages", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		for id := int64(1); id <= 4; id++ {
			require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(id, "message")))
		}
		require.NoError(t, sess.DeleteMessage(t.Context(), 2))
		require.NoError(t, sess.CompactMessages(t.Context(), 3, postgresMessage(10, "summary")))

		messages, err := sess.GetMessages(t.Context(), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, []int64{3, 4, 10}, messageIDs(messages))
	})

	t.Run("isolates sessions", func(t *testing.T) {
		f := newPostgresFixture(t)
		first := f.createSession(t, 1)
		second := f.createSession(t, 2)
		require.NoError(t, first.CreateMessage(t.Context(), postgresMessage(1, "first")))
		require.NoError(t, second.CreateMessage(t.Context(), postgresMessage(2, "second")))

		firstMessages, err := first.GetMessages(t.Context(), 10, 0)
		require.NoError(t, err)
		secondMessages, err := second.GetMessages(t.Context(), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, []int64{1}, messageIDs(firstMessages))
		assert.Equal(t, []int64{2}, messageIDs(secondMessages))
	})
}

func TestPostgres_DatabaseSession_ListMessages(t *testing.T) {
	t.Run("returns every active message", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		for id := int64(1); id <= 5; id++ {
			require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(id, "message")))
		}

		messages, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2, 3, 4, 5}, messageIDs(messages))
	})

	t.Run("returns an empty slice for an empty session", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)

		messages, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		assert.Empty(t, messages)
		assert.NotNil(t, messages)
	})
}

func TestPostgres_DatabaseSession_CompactMessages(t *testing.T) {
	t.Run("archives messages before the split point", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		for id := int64(1); id <= 4; id++ {
			require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(id, "message")))
		}

		require.NoError(t, sess.CompactMessages(t.Context(), 3, postgresMessage(10, "summary")))

		active, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{3, 4, 10}, messageIDs(active))

		compacted, err := sess.(*databaseSession).ListCompactedMessages(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2}, messageIDs(compacted))
	})

	t.Run("archives all messages when split id is zero", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(1, "first")))
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(2, "second")))

		require.NoError(t, sess.CompactMessages(t.Context(), 0, postgresMessage(10, "summary")))

		active, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{10}, messageIDs(active))
	})

	t.Run("inserts a summary into an empty session", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)

		require.NoError(t, sess.CompactMessages(t.Context(), 0, postgresMessage(10, "summary")))

		active, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{10}, messageIDs(active))
	})

	t.Run("supports repeated compaction", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(1, "first")))
		require.NoError(t, sess.CompactMessages(t.Context(), 0, postgresMessage(10, "summary one")))
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(2, "second")))
		require.NoError(t, sess.CompactMessages(t.Context(), 0, postgresMessage(20, "summary two")))

		active, err := sess.ListMessages(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int64{20}, messageIDs(active))
	})

	t.Run("rolls back archival when summary insertion fails", func(t *testing.T) {
		f := newPostgresFixture(t)
		sess := f.createSession(t, 1)
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(1, "first")))
		require.NoError(t, sess.CreateMessage(t.Context(), postgresMessage(2, "second")))

		err := sess.CompactMessages(t.Context(), 0, postgresMessage(2, "duplicate summary id"))
		require.Error(t, err)

		active, listErr := sess.ListMessages(t.Context())
		require.NoError(t, listErr)
		assert.Equal(t, []int64{1, 2}, messageIDs(active))
	})
}

func cleanupPostgresTables(t *testing.T, db *sqlx.DB, prefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `
		DROP TABLE IF EXISTS `+prefix+`messages;
		DROP TABLE IF EXISTS `+prefix+`sessions;
		DROP TABLE IF EXISTS `+prefix+`schema_migrations;
	`)
	require.NoError(t, err)
}
