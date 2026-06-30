package database

import (
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adksession "github.com/soasurs/adk/session"
)

func TestSQLite_DatabaseSessionService_CreateSession(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, adksession.CreateSessionRequest{
		SessionID: "session-1",
		AppID:     "chat",
		UserID:    "user-1",
	})
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, "session-1", s.GetSessionID())
	assert.Equal(t, "chat", s.GetAppID())
	assert.Equal(t, "user-1", s.GetUserID())
}

func TestSQLite_DatabaseSessionService_CreateSession_Multiple(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s1, err := service.CreateSession(ctx, newTestSessionRequest("session-1"))
	assert.NoError(t, err)
	assert.NotNil(t, s1)
	assert.Equal(t, "session-1", s1.GetSessionID())

	s2, err := service.CreateSession(ctx, newTestSessionRequest("session-2"))
	assert.NoError(t, err)
	assert.NotNil(t, s2)
	assert.Equal(t, "session-2", s2.GetSessionID())
}

func TestSQLite_DatabaseSessionService_GetSession(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	_, err = service.CreateSession(ctx, adksession.CreateSessionRequest{
		SessionID: "session-1",
		AppID:     "chat",
		UserID:    "user-1",
	})
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, "session-1")
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, "session-1", s.GetSessionID())
	assert.Equal(t, "chat", s.GetAppID())
	assert.Equal(t, "user-1", s.GetUserID())
}

func TestSQLite_DatabaseSessionService_GetSession_NotFound(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.GetSession(ctx, "missing")
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestSQLite_DatabaseSessionService_DeleteSession(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	_, err = service.CreateSession(ctx, newTestSessionRequest("session-1"))
	assert.NoError(t, err)

	err = service.DeleteSession(ctx, "session-1")
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, "session-1")
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestSQLite_DatabaseSessionService_DeleteSession_NotFound(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	err = service.DeleteSession(ctx, "missing")
	assert.NoError(t, err)
}

func TestSQLite_DatabaseSessionService_FullWorkflow(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s1, err := service.CreateSession(ctx, newTestSessionRequest("session-1"))
	assert.NoError(t, err)

	s2, err := service.CreateSession(ctx, newTestSessionRequest("session-2"))
	assert.NoError(t, err)

	gotS1, err := service.GetSession(ctx, "session-1")
	assert.NoError(t, err)
	assert.Equal(t, s1.GetSessionID(), gotS1.GetSessionID())

	gotS2, err := service.GetSession(ctx, "session-2")
	assert.NoError(t, err)
	assert.Equal(t, s2.GetSessionID(), gotS2.GetSessionID())

	err = service.DeleteSession(ctx, "session-1")
	assert.NoError(t, err)

	gotS1AfterDelete, err := service.GetSession(ctx, "session-1")
	assert.NoError(t, err)
	assert.Nil(t, gotS1AfterDelete)

	gotS2AfterDelete, err := service.GetSession(ctx, "session-2")
	assert.NoError(t, err)
	assert.NotNil(t, gotS2AfterDelete)
	assert.Equal(t, "session-2", gotS2AfterDelete.GetSessionID())
}

func TestSQLite_DatabaseSessionService_WithStringID(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-external-1"

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, newTestSessionRequest(sessionID))
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

func setupSQLiteTestDBWithPrefix(t *testing.T, prefix string) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)

	err = InitSchema(t.Context(), db, WithTablePrefix(prefix))
	require.NoError(t, err)

	return db
}

func TestSQLite_DatabaseSessionService_WithTablePrefix(t *testing.T) {
	const prefix = "myapp_"
	db := setupSQLiteTestDBWithPrefix(t, prefix)
	defer db.Close()

	service, err := NewDatabaseSessionService(db, WithTablePrefix(prefix))
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, newTestSessionRequest("session-1"))
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, "session-1", s.GetSessionID())

	got, err := service.GetSession(ctx, "session-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "session-1", got.GetSessionID())

	err = service.DeleteSession(ctx, "session-1")
	assert.NoError(t, err)

	gotAfterDelete, err := service.GetSession(ctx, "session-1")
	assert.NoError(t, err)
	assert.Nil(t, gotAfterDelete)
}

func TestSQLite_DatabaseSessionService_WithSessionsTable(t *testing.T) {
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	defer db.Close()

	err = InitSchema(t.Context(), db, WithSessionsTable("custom_sessions"))
	require.NoError(t, err)

	service, err := NewDatabaseSessionService(db, WithSessionsTable("custom_sessions"))
	require.NoError(t, err)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, newTestSessionRequest("session-42"))
	require.NoError(t, err)
	assert.Equal(t, "session-42", s.GetSessionID())
}

func TestSQLite_DatabaseSessionService_InvalidTableName(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	tests := []struct {
		name string
		opts []Option
	}{
		{"empty sessions table", []Option{WithSessionsTable("")}},
		{"empty events table", []Option{WithEventsTable("")}},
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
