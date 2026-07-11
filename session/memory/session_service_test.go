package memory

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adksession "github.com/soasurs/adk/session"
)

func testSessionRequest(sessionID string) adksession.CreateSessionRequest {
	return adksession.CreateSessionRequest{SessionID: sessionID}
}

func TestMemorySessionService_CreateSession(t *testing.T) {
	service := NewMemorySessionService()
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

func TestMemorySessionService_CreateSession_Multiple(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	s1, err := service.CreateSession(ctx, testSessionRequest("session-1"))
	assert.NoError(t, err)
	assert.NotNil(t, s1)
	assert.Equal(t, "session-1", s1.GetSessionID())

	s2, err := service.CreateSession(ctx, testSessionRequest("session-2"))
	assert.NoError(t, err)
	assert.NotNil(t, s2)
	assert.Equal(t, "session-2", s2.GetSessionID())
}

func TestMemorySessionService_GetSession(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	_, err := service.CreateSession(ctx, adksession.CreateSessionRequest{
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

func TestMemorySessionService_GetSession_NotFound(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	s, err := service.GetSession(ctx, "missing")
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestMemorySessionService_ListSessions(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	create := func(id, appID, userID string, createdAt int64) {
		t.Helper()
		sess, err := service.CreateSession(ctx, adksession.CreateSessionRequest{
			SessionID: id,
			AppID:     appID,
			UserID:    userID,
		})
		require.NoError(t, err)
		stored := sess.(*memorySession)
		stored.createdAt = createdAt
	}

	create("session-b", "chat", "user-1", 100)
	create("session-a", "chat", "user-1", 100)
	create("session-c", "chat", "user-1", 300)
	create("other-user", "chat", "user-2", 500)
	create("other-app", "admin", "user-1", 600)

	tests := []struct {
		name string
		req  adksession.ListSessionsRequest
		want []string
	}{
		{
			name: "defaults to creation time descending with stable session id order",
			req:  adksession.ListSessionsRequest{AppID: "chat", UserID: "user-1"},
			want: []string{"session-c", "session-a", "session-b"},
		},
		{
			name: "sorts by session id ascending",
			req: adksession.ListSessionsRequest{
				AppID: "chat", UserID: "user-1",
				SortBy: adksession.SessionSortBySessionID, SortOrder: adksession.SortAscending,
			},
			want: []string{"session-a", "session-b", "session-c"},
		},
		{
			name: "sorts by created_at ascending explicitly",
			req: adksession.ListSessionsRequest{
				AppID: "chat", UserID: "user-1",
				SortBy: adksession.SessionSortByCreatedAt, SortOrder: adksession.SortAscending,
			},
			want: []string{"session-a", "session-b", "session-c"},
		},
		{
			name: "applies limit and offset",
			req: adksession.ListSessionsRequest{
				AppID: "chat", UserID: "user-1", Limit: 1, Offset: 1,
			},
			want: []string{"session-a"},
		},
		{
			name: "returns empty slice when no sessions match",
			req:  adksession.ListSessionsRequest{AppID: "nonexistent", UserID: "user-1"},
			want: []string{},
		},
		{
			name: "returns empty slice when offset exceeds count",
			req:  adksession.ListSessionsRequest{AppID: "chat", UserID: "user-1", Offset: 10},
			want: []string{},
		},
		{
			name: "handles maximum limit without overflow",
			req: adksession.ListSessionsRequest{
				AppID: "chat", UserID: "user-1", Limit: math.MaxInt64, Offset: 1,
			},
			want: []string{"session-a", "session-b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sessions, err := service.ListSessions(ctx, tc.req)
			require.NoError(t, err)
			ids := make([]string, len(sessions))
			for i, sess := range sessions {
				ids[i] = sess.GetSessionID()
			}
			assert.Equal(t, tc.want, ids)
		})
	}
}

func TestMemorySessionService_DeleteSession(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	_, err := service.CreateSession(ctx, testSessionRequest("session-1"))
	assert.NoError(t, err)

	err = service.DeleteSession(ctx, "session-1")
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, "session-1")
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestMemorySessionService_DeleteSession_NotFound(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	err := service.DeleteSession(ctx, "missing")
	assert.NoError(t, err)
}

func TestMemorySessionService_FullWorkflow(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	s1, err := service.CreateSession(ctx, testSessionRequest("session-1"))
	assert.NoError(t, err)

	s2, err := service.CreateSession(ctx, testSessionRequest("session-2"))
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
