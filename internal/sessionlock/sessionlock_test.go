package sessionlock

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocker_ContextCancellation(t *testing.T) {
	locker := New[string]()
	unlock, err := locker.Lock(t.Context(), "session-1")
	require.NoError(t, err)
	defer unlock()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = locker.Lock(ctx, "session-1")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestLocker_DifferentSessionsProceedIndependently(t *testing.T) {
	locker := New[string]()
	unlockFirst, err := locker.Lock(t.Context(), "session-1")
	require.NoError(t, err)
	defer unlockFirst()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	unlockSecond, err := locker.Lock(ctx, "session-2")
	require.NoError(t, err)
	unlockSecond()
}

func TestLocker_UnlockIsIdempotent(t *testing.T) {
	locker := New[string]()
	unlock, err := locker.Lock(t.Context(), "session-1")
	require.NoError(t, err)

	unlock()
	unlock()

	nextUnlock, err := locker.Lock(t.Context(), "session-1")
	require.NoError(t, err)
	nextUnlock()
}
