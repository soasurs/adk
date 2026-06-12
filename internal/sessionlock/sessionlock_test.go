package sessionlock

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocker_ContextCancellation(t *testing.T) {
	locker := New()
	unlock, err := locker.Lock(t.Context(), 1)
	require.NoError(t, err)
	defer unlock()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = locker.Lock(ctx, 1)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestLocker_DifferentSessionsProceedIndependently(t *testing.T) {
	locker := New()
	unlockFirst, err := locker.Lock(t.Context(), 1)
	require.NoError(t, err)
	defer unlockFirst()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	unlockSecond, err := locker.Lock(ctx, 2)
	require.NoError(t, err)
	unlockSecond()
}

func TestLocker_UnlockIsIdempotent(t *testing.T) {
	locker := New()
	unlock, err := locker.Lock(t.Context(), 1)
	require.NoError(t, err)

	unlock()
	unlock()

	nextUnlock, err := locker.Lock(t.Context(), 1)
	require.NoError(t, err)
	nextUnlock()
}
