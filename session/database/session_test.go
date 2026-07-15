package database

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	adksession "github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/event"
)

func setupSQLiteTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)

	err = InitSchema(t.Context(), db)
	require.NoError(t, err)

	return db
}

func newTestEvent(id int64, content string) *event.Event {
	return &event.Event{
		EventID:   id,
		Content:   content,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
}

func newTestSessionRequest(sessionID string) adksession.CreateSessionRequest {
	return adksession.CreateSessionRequest{SessionID: sessionID}
}

func TestSQLite_DatabaseSession_CreateEvent(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)
	require.NotNil(t, session)

	ev := newTestEvent(1, "hello")
	ev.TurnID = "turn-1"
	err = session.CreateEvent(ctx, ev)
	assert.NoError(t, err)

	evs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, evs, 1)
	assert.Equal(t, int64(1), evs[0].EventID)
	assert.Equal(t, "turn-1", evs[0].TurnID)
	assert.Positive(t, session.GetCreatedAt())
}

func TestSQLite_InitSchema_RemovesSessionUpdatedAt(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	var names []string
	require.NoError(t, db.SelectContext(t.Context(), &names, "SELECT name FROM pragma_table_info('sessions')"))
	assert.NotContains(t, names, "updated_at")
}

func TestSQLite_DatabaseSession_TurnLifecycle(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sess, err := NewDatabaseSession(t.Context(), db, newTestSessionRequest("session-1"))
	require.NoError(t, err)
	turns := sess.(adksession.TurnStore)
	require.NoError(t, turns.BeginTurn(t.Context(), adksession.Turn{
		ID:        "turn-1",
		Status:    adksession.TurnRunning,
		StartedAt: 1,
	}))
	require.NoError(t, turns.FinalizeTurn(t.Context(), "turn-1", adksession.TurnOutcome{
		Status: adksession.TurnFailed,
		Reason: adksession.TurnReasonAgentError,
		Failure: &adksession.TurnFailure{
			Code:    "provider_unavailable",
			Message: "provider is temporarily unavailable",
			Stage:   adksession.TurnFailureStageProvider,
		},
	}))

	turn, err := turns.GetTurn(t.Context(), "turn-1")
	require.NoError(t, err)
	require.NotNil(t, turn)
	assert.Equal(t, adksession.TurnFailed, turn.Status)
	assert.Equal(t, adksession.TurnReasonAgentError, turn.Reason)
	require.NotNil(t, turn.Failure)
	assert.Equal(t, "provider_unavailable", turn.Failure.Code)
	assert.Equal(t, "provider is temporarily unavailable", turn.Failure.Message)
	assert.Equal(t, adksession.TurnFailureStageProvider, turn.Failure.Stage)
	assert.Positive(t, turn.FinishedAt)
	assert.ErrorIs(t, turns.FinalizeTurn(t.Context(), "turn-1", adksession.TurnOutcome{
		Status: adksession.TurnCompleted,
	}), adksession.ErrTurnStateConflict)
}

func TestSQLite_InitSchema_BackfillsDurableTurns(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sess, err := NewDatabaseSession(t.Context(), db, newTestSessionRequest("session-1"))
	require.NoError(t, err)
	ev := newTestEvent(1, "legacy")
	ev.TurnID = "turn-legacy"
	require.NoError(t, sess.CreateEvent(t.Context(), ev))

	_, err = db.ExecContext(t.Context(), "DROP TABLE turns")
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "DELETE FROM schema_migrations WHERE version >= 7")
	require.NoError(t, err)
	require.NoError(t, InitSchema(t.Context(), db))

	turns := sess.(adksession.TurnStore)
	turn, err := turns.GetTurn(t.Context(), "turn-legacy")
	require.NoError(t, err)
	require.NotNil(t, turn)
	assert.Equal(t, adksession.TurnCompleted, turn.Status)
	assert.Nil(t, turn.Failure)
	assert.Equal(t, ev.CreatedAt, turn.StartedAt)
	assert.Equal(t, ev.UpdatedAt, turn.FinishedAt)
}

func TestSQLite_InitSchema_AddsTurnIDToExistingSchema(t *testing.T) {
	db, err := sqlx.Connect("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := t.Context()
	tables := migrationTables{
		sessions:   defaultSessionsTable,
		events:     defaultEventsTable,
		turns:      defaultTurnsTable,
		migrations: defaultMigrationsTable,
	}
	_, err = db.ExecContext(ctx, createMigrationsTableSQL(tables.migrations))
	require.NoError(t, err)
	for _, stmt := range migrationV1SQL(tables) {
		_, err = db.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, recordMigrationSQL(tables.migrations), 1)
	require.NoError(t, err)
	for _, stmt := range migrationV2SQL(tables) {
		_, err = db.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, recordMigrationSQL(tables.migrations), 2)
	require.NoError(t, err)

	_, err = db.ExecContext(
		ctx,
		`
			INSERT INTO sessions (
				session_id,
				app_id,
				user_id,
				created_at,
				updated_at,
				deleted_at
			)
			VALUES ('legacy-session', '', '', 1, 1, 0)
		`,
	)
	require.NoError(t, err)
	_, err = db.ExecContext(
		ctx,
		`
			INSERT INTO events (
				event_id,
				session_id,
				author,
				role,
				text,
				reasoning_text,
				tool_calls,
				tool_result,
				tool_call_id,
				finish_reason,
				parts,
				prompt_tokens,
				completion_tokens,
				total_tokens,
				created_at,
				updated_at,
				compacted_at,
				deleted_at
			)
			VALUES (
				1,
				'legacy-session',
				'user',
				'user',
				'legacy',
				'',
				'[]',
				'',
				'',
				'',
				'[]',
				0,
				0,
				0,
				1,
				1,
				0,
				0
			)
		`,
	)
	require.NoError(t, err)

	require.NoError(t, InitSchema(ctx, db))

	var versions []int
	err = db.SelectContext(ctx, &versions, "SELECT version FROM schema_migrations ORDER BY version")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8}, versions)
	var eventColumnNames []string
	require.NoError(t, db.SelectContext(ctx, &eventColumnNames, "SELECT name FROM pragma_table_info('events')"))
	assert.Contains(t, eventColumnNames, "archived_at")
	assert.NotContains(t, eventColumnNames, "compacted_at")

	service, err := NewDatabaseSessionService(db)
	require.NoError(t, err)
	sess, err := service.GetSession(ctx, "legacy-session")
	require.NoError(t, err)
	require.NotNil(t, sess)

	events, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "", events[0].TurnID)

	ev := newTestEvent(2, "new")
	ev.TurnID = "turn-new"
	require.NoError(t, sess.CreateEvent(ctx, ev))

	events, err = sess.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "turn-new", events[1].TurnID)
}

func TestSQLite_DatabaseSession_UsageDetails_RoundTrip(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, newTestSessionRequest("session-1"))
	require.NoError(t, err)

	expectedDetails := &model.TokenUsageDetails{
		CachedPromptTokens:        12,
		CacheCreationPromptTokens: 3,
		CacheReadPromptTokens:     9,
		ReasoningTokens:           4,
		ToolUsePromptTokens:       5,
		AudioPromptTokens:         6,
		AudioCompletionTokens:     7,
		AcceptedPredictionTokens:  8,
		RejectedPredictionTokens:  2,
	}
	ev := event.FromModel(model.Event{
		ID:        1,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
		Author:    "assistant",
		Content: model.Content{
			Role:    model.RoleAssistant,
			Content: "answer",
		},
		Usage: &model.TokenUsage{
			PromptTokens:     20,
			CompletionTokens: 10,
			TotalTokens:      30,
			Details:          expectedDetails,
		},
	})
	require.NoError(t, sess.CreateEvent(ctx, ev))

	events, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)

	got := events[0].ToModel()
	require.NotNil(t, got.Usage)
	assert.Equal(t, int64(20), got.Usage.PromptTokens)
	assert.Equal(t, int64(10), got.Usage.CompletionTokens)
	assert.Equal(t, int64(30), got.Usage.TotalTokens)
	require.NotNil(t, got.Usage.Details)
	assert.Equal(t, expectedDetails, got.Usage.Details)
}

func TestSQLite_DatabaseSession_DeleteEvent(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	ev1 := newTestEvent(1, "hello")
	ev2 := newTestEvent(2, "hi")
	ev3 := newTestEvent(3, "how are you")

	require.NoError(t, session.CreateEvent(ctx, ev1))
	require.NoError(t, session.CreateEvent(ctx, ev2))
	require.NoError(t, session.CreateEvent(ctx, ev3))

	err = session.DeleteEvent(ctx, 2)
	assert.NoError(t, err)

	evs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, evs, 2)

	for _, ev := range evs {
		assert.NotEqual(t, int64(2), ev.EventID)
	}
}

func TestSQLite_DatabaseSession_GetEvents(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	for i := int64(1); i <= 10; i++ {
		ev := newTestEvent(i, "ev")
		require.NoError(t, session.CreateEvent(ctx, ev))
	}

	t.Run("get all", func(t *testing.T) {
		evs, err := session.GetEvents(ctx, 100, 0)
		assert.NoError(t, err)
		assert.Len(t, evs, 10)
	})

	t.Run("with limit", func(t *testing.T) {
		evs, err := session.GetEvents(ctx, 5, 0)
		assert.NoError(t, err)
		assert.Len(t, evs, 5)
	})

	t.Run("with offset", func(t *testing.T) {
		evs, err := session.GetEvents(ctx, 5, 3)
		assert.NoError(t, err)
		assert.Len(t, evs, 5)
		assert.Equal(t, int64(4), evs[0].EventID)
	})

	t.Run("limit and offset", func(t *testing.T) {
		evs, err := session.GetEvents(ctx, 3, 2)
		assert.NoError(t, err)
		assert.Len(t, evs, 3)
		assert.Equal(t, int64(3), evs[0].EventID)
	})
}

func TestSQLite_DatabaseSession_GetEvents_StableOrder(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, newTestSessionRequest("session-1"))
	require.NoError(t, err)

	const createdAt = int64(1234)
	for _, id := range []int64{3, 1, 2} {
		ev := newTestEvent(id, "ev")
		ev.CreatedAt = createdAt
		require.NoError(t, sess.CreateEvent(ctx, ev))
	}

	evs, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, evs, 3)
	assert.Equal(t, []int64{1, 2, 3}, []int64{
		evs[0].EventID,
		evs[1].EventID,
		evs[2].EventID,
	})
}

func TestSQLite_DatabaseSession_ToolCallThoughtSignature_RoundTrip(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, newTestSessionRequest("session-1"))
	require.NoError(t, err)

	ev := newTestEvent(1, "")
	ev.Role = string(model.RoleAssistant)
	ev.ToolCalls = event.ToolCalls{
		{
			ID:               "call-1",
			Name:             "lookup",
			Arguments:        json.RawMessage(`{"query":"weather"}`),
			ThoughtSignature: []byte{0x01, 0x02, 0xff},
		},
	}
	require.NoError(t, sess.CreateEvent(ctx, ev))

	evs, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, evs, 1)
	require.Len(t, evs[0].ToolCalls, 1)
	assert.Equal(t, ev.ToolCalls[0], evs[0].ToolCalls[0])
}

func TestSQLite_DatabaseSession_ArchiveEventsBefore(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	ev1 := newTestEvent(1, "hello")
	ev2 := newTestEvent(2, "hi")
	ev3 := newTestEvent(3, "how are you")
	ev4 := newTestEvent(4, "fine")

	require.NoError(t, session.CreateEvent(ctx, ev1))
	require.NoError(t, session.CreateEvent(ctx, ev2))
	require.NoError(t, session.CreateEvent(ctx, ev3))
	require.NoError(t, session.CreateEvent(ctx, ev4))

	// Archive ev1 and ev2; keep ev3 and ev4 active.
	err = session.ArchiveEventsBefore(ctx, 3)
	assert.NoError(t, err)

	evs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, evs, 2)
	assert.Equal(t, int64(3), evs[0].EventID)
	assert.Equal(t, int64(4), evs[1].EventID)

	archived, err := session.ListArchivedEvents(ctx)
	assert.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, []int64{archived[0].EventID, archived[1].EventID})
}

func TestSQLite_DatabaseSession_ArchiveEventsBefore_ArchiveAll(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	require.NoError(t, session.CreateEvent(ctx, newTestEvent(1, "hello")))
	require.NoError(t, session.CreateEvent(ctx, newTestEvent(2, "hi")))

	// eventID=0 archives all.
	err = session.ArchiveEventsBefore(ctx, 0)
	assert.NoError(t, err)

	evs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Empty(t, evs)
}

func TestSQLite_DatabaseSession_ArchiveEventsBefore_Empty(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	err = session.ArchiveEventsBefore(ctx, 0)
	assert.NoError(t, err)

	evs, err := session.GetEvents(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Empty(t, evs)
}

func TestSQLite_DatabaseSession_ListEvents(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	for i := int64(1); i <= 5; i++ {
		require.NoError(t, session.CreateEvent(ctx, newTestEvent(i, "ev")))
	}

	evs, err := session.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, evs, 5)
}

func TestSQLite_DatabaseSession_IsolatesEventsBySession(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	ctx := t.Context()
	s1, err := NewDatabaseSession(ctx, db, newTestSessionRequest("session-1"))
	require.NoError(t, err)
	s2, err := NewDatabaseSession(ctx, db, newTestSessionRequest("session-2"))
	require.NoError(t, err)

	require.NoError(t, s1.CreateEvent(ctx, newTestEvent(1, "session one")))
	require.NoError(t, s2.CreateEvent(ctx, newTestEvent(2, "session two")))

	evs1, err := s1.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, evs1, 1)
	assert.Equal(t, "session-1", evs1[0].SessionID)
	assert.Equal(t, "session one", evs1[0].Content)

	evs2, err := s2.ListEvents(ctx)
	require.NoError(t, err)
	require.Len(t, evs2, 1)
	assert.Equal(t, "session-2", evs2[0].SessionID)
	assert.Equal(t, "session two", evs2[0].Content)
}

func TestSQLite_DatabaseSession_ArchiveEventsBefore_MultipleRounds(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	require.NoError(t, sess.CreateEvent(ctx, newTestEvent(1, "a")))
	require.NoError(t, sess.CreateEvent(ctx, newTestEvent(2, "b")))

	err = sess.ArchiveEventsBefore(ctx, 0)
	require.NoError(t, err)

	require.NoError(t, sess.CreateEvent(ctx, newTestEvent(3, "c")))

	err = sess.ArchiveEventsBefore(ctx, 0)
	require.NoError(t, err)

	active, err := sess.ListEvents(ctx)
	assert.NoError(t, err)
	assert.Empty(t, active)

	archived, err := sess.ListArchivedEvents(ctx)
	assert.NoError(t, err)
	assert.Len(t, archived, 3)
}

func TestSQLite_DatabaseSession_ArchiveEventsBefore_MissingBoundary(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, newTestSessionRequest("session-1"))
	require.NoError(t, err)
	require.NoError(t, sess.CreateEvent(ctx, newTestEvent(1, "a")))

	err = sess.ArchiveEventsBefore(ctx, 99)

	assert.ErrorIs(t, err, adksession.ErrArchiveBoundaryNotFound)
	active, listErr := sess.ListEvents(ctx)
	require.NoError(t, listErr)
	assert.Len(t, active, 1)
}

func TestSQLite_DatabaseSession_ArchiveEventsBefore_UsesConversationOrder(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, newTestSessionRequest("session-1"))
	require.NoError(t, err)
	for _, tc := range []struct {
		id        int64
		createdAt int64
	}{{id: 100, createdAt: 1}, {id: 1, createdAt: 2}, {id: 50, createdAt: 3}} {
		ev := newTestEvent(tc.id, "event")
		ev.CreatedAt = tc.createdAt
		require.NoError(t, sess.CreateEvent(ctx, ev))
	}

	require.NoError(t, sess.ArchiveEventsBefore(ctx, 1))
	active, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 50}, []int64{active[0].EventID, active[1].EventID})
	archived, err := sess.ListArchivedEvents(ctx)
	require.NoError(t, err)
	require.Len(t, archived, 1)
	assert.Equal(t, int64(100), archived[0].EventID)
}

func TestSQLite_DatabaseSession_GetSessionID(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
	require.NoError(t, err)

	assert.Equal(t, sessionID, session.GetSessionID())
}

func TestSQLite_DatabaseSession_GetAppAndUserID(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	ctx := t.Context()
	session, err := NewDatabaseSession(ctx, db, adksession.CreateSessionRequest{
		SessionID: "session-1",
		AppID:     "chat",
		UserID:    "user-1",
	})
	require.NoError(t, err)

	assert.Equal(t, "chat", session.GetAppID())
	assert.Equal(t, "user-1", session.GetUserID())
}

// TestSQLite_DatabaseSession_Parts_RoundTrip verifies that ContentParts are written to the
// database and read back intact, preserving all fields.
func TestSQLite_DatabaseSession_Parts_RoundTrip(t *testing.T) {
	db := setupSQLiteTestDB(t)
	defer db.Close()

	sessionID := "session-1"

	ctx := t.Context()
	sess, err := NewDatabaseSession(ctx, db, newTestSessionRequest(sessionID))
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
	ev := &event.Event{
		EventID:   1,
		Role:      string(model.RoleUser),
		Parts:     parts,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}

	require.NoError(t, sess.CreateEvent(ctx, ev))

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
