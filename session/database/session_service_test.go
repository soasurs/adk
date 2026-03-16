package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/internal/snowflake"
)

func TestDatabaseSessionService_CreateSession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service := NewDatabaseSessionService(db)
	ctx := t.Context()

	s, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, int64(1), s.GetSessionID())
}

func TestDatabaseSessionService_CreateSession_Multiple(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service := NewDatabaseSessionService(db)
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

	service := NewDatabaseSessionService(db)
	ctx := t.Context()

	_, err := service.CreateSession(ctx, 1)
	assert.NoError(t, err)

	s, err := service.GetSession(ctx, 1)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, int64(1), s.GetSessionID())
}

func TestDatabaseSessionService_GetSession_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service := NewDatabaseSessionService(db)
	ctx := t.Context()

	s, err := service.GetSession(ctx, 999)
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestDatabaseSessionService_DeleteSession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service := NewDatabaseSessionService(db)
	ctx := t.Context()

	_, err := service.CreateSession(ctx, 1)
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

	service := NewDatabaseSessionService(db)
	ctx := t.Context()

	err := service.DeleteSession(ctx, 999)
	assert.NoError(t, err)
}

func TestDatabaseSessionService_FullWorkflow(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	service := NewDatabaseSessionService(db)
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

	service := NewDatabaseSessionService(db)
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
