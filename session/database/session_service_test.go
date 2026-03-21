package database

import (
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/internal/snowflake"
)

func TestDatabaseSessionService_CreateSession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, int64(1), s.GetSessionID())
}

func TestDatabaseSessionService_CreateSession_Multiple(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s1, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)
	assert.NotNil(t, s1)
	assert.Equal(t, int64(1), s1.GetSessionID())

	s2, err := service.CreateSession(ctx, 2)
	assert.NoError(t, err)
	assert.NotNil(t, s2)
	assert.Equal(t, int64(2), s2.GetSessionID())
}

func TestDatabaseSessionService_GetSession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	_, err = service.CreateSession(ctx, 1)
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, int64(1), s.GetSessionID())
}

func TestDatabaseSessionService_GetSession_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.GetSession(ctx, 999)
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestDatabaseSessionService_DeleteSession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	_, err = service.CreateSession(ctx, 1)
	assert.NoError(t, err)

	err = service.DeleteSession(ctx, 1)
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestDatabaseSessionService_DeleteSession_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	err = service.DeleteSession(ctx, 999)
	assert.NoError(t, err)
}

func TestDatabaseSessionService_FullWorkflow(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s1, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)

	s2, err := service.CreateSession(ctx, 2)
	assert.NoError(t, err)

	gotS1, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.Equal(t, s1.GetSessionID(), gotS1.GetSessionID())

	gotS2, err := service.GetSession(ctx, 2)
	assert.NoError(t, err)
	assert.Equal(t, s2.GetSessionID(), gotS2.GetSessionID())

	err = service.DeleteSession(ctx, 1)
	assert.NoError(t, err)

	gotS1AfterDelete, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.Nil(t, gotS1AfterDelete)

	gotS2AfterDelete, err := service.GetSession(ctx, 2)
	assert.NoError(t, err)
	assert.NotNil(t, gotS2AfterDelete)
	assert.Equal(t, int64(2), gotS2AfterDelete.GetSessionID())
}

func TestDatabaseSessionService_WithSnowflakeID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snowflaker, err := snowflake.New()
	require.NoError(t, err)
	sessionID := snowflaker.Generate().Int64()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, sessionID)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, sessionID, s.GetSessionID())

	gotS, err := service.GetSession(ctx, sessionID)
	assert.NoError(t, err)
	assert.NotNil(t, gotS)
	assert.Equal(t, sessionID, gotS.GetSessionID())

	err = service.DeleteSession(ctx, sessionID)
	assert.NoError(t, err)

	gotSAfterDelete, err := service.GetSession(ctx, sessionID)
	assert.NoError(t, err)
	assert.Nil(t, gotSAfterDelete)
}

func setupTestDBWithPrefix(t *testing.T, prefix string) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE ` + prefix + `sessions (
		session_id INTEGER PRIMARY KEY,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		deleted_at INTEGER NOT NULL
	)`)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE ` + prefix + `messages (
		message_id        INTEGER PRIMARY KEY,
		session_id        INTEGER NOT NULL,
		role              TEXT    NOT NULL DEFAULT '',
		name              TEXT    NOT NULL DEFAULT '',
		content           TEXT    NOT NULL DEFAULT '',
		reasoning_content TEXT    NOT NULL DEFAULT '',
		tool_calls        TEXT    NOT NULL DEFAULT '[]',
		tool_call_id      TEXT    NOT NULL DEFAULT '',
		parts             TEXT    NOT NULL DEFAULT '[]',
		prompt_tokens     INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens      INTEGER NOT NULL DEFAULT 0,
		created_at        INTEGER NOT NULL,
		updated_at        INTEGER NOT NULL,
		compacted_at      INTEGER NOT NULL DEFAULT 0,
		deleted_at        INTEGER NOT NULL
	)`)
	require.NoError(t, err)

	return db
}

func TestDatabaseSessionService_WithTablePrefix(t *testing.T) {
	const prefix = "myapp_"
	db := setupTestDBWithPrefix(t, prefix)
	defer db.Close()

	service, err := NewDatabaseSessionService(db, WithTablePrefix(prefix))
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, int64(1), s.GetSessionID())

	got, err := service.GetSession(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(1), got.GetSessionID())

	err = service.DeleteSession(ctx, 1)
	assert.NoError(t, err)

	gotAfterDelete, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.Nil(t, gotAfterDelete)
}

func TestDatabaseSessionService_WithSessionsTable(t *testing.T) {
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE custom_sessions (
		session_id INTEGER PRIMARY KEY,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		deleted_at INTEGER NOT NULL
	)`)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE messages (
		message_id        INTEGER PRIMARY KEY,
		session_id        INTEGER NOT NULL,
		role              TEXT    NOT NULL DEFAULT '',
		name              TEXT    NOT NULL DEFAULT '',
		content           TEXT    NOT NULL DEFAULT '',
		reasoning_content TEXT    NOT NULL DEFAULT '',
		tool_calls        TEXT    NOT NULL DEFAULT '[]',
		tool_call_id      TEXT    NOT NULL DEFAULT '',
		parts             TEXT    NOT NULL DEFAULT '[]',
		prompt_tokens     INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens      INTEGER NOT NULL DEFAULT 0,
		created_at        INTEGER NOT NULL,
		updated_at        INTEGER NOT NULL,
		compacted_at      INTEGER NOT NULL DEFAULT 0,
		deleted_at        INTEGER NOT NULL
	)`)
	require.NoError(t, err)

	service, err := NewDatabaseSessionService(db, WithSessionsTable("custom_sessions"))
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), s.GetSessionID())
}

func TestDatabaseSessionService_InvalidTableName(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tests := []struct {
		name string
		opts []Option
	}{
		{"empty sessions table", []Option{WithSessionsTable("")}},
		{"empty messages table", []Option{WithMessagesTable("")}},
		{"spaces in name", []Option{WithSessionsTable("my sessions")}},
		{"SQL injection attempt", []Option{WithSessionsTable("sessions; DROP TABLE sessions")}},
		{"starts with digit", []Option{WithSessionsTable("1sessions")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDatabaseSessionService(db, tc.opts...)
			assert.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidTableName)
			var invalidNameErr *InvalidTableNameError
			require.True(t, errors.As(err, &invalidNameErr))
		})
	}
}
