package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
