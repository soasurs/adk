package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemorySessionService_CreateSession(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	s, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, int64(1), s.GetSessionID())
}

func TestMemorySessionService_CreateSession_Multiple(t *testing.T) {
	service := NewMemorySessionService()
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

func TestMemorySessionService_GetSession(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	_, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, int64(1), s.GetSessionID())
}

func TestMemorySessionService_GetSession_NotFound(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	s, err := service.GetSession(ctx, 999)
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestMemorySessionService_DeleteSession(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	_, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)

	err = service.DeleteSession(ctx, 1)
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestMemorySessionService_DeleteSession_NotFound(t *testing.T) {
	service := NewMemorySessionService()
	ctx := t.Context()

	err := service.DeleteSession(ctx, 999)
	assert.NoError(t, err)
}

func TestMemorySessionService_FullWorkflow(t *testing.T) {
	service := NewMemorySessionService()
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
