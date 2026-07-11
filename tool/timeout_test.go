package tool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/tool"
)

// stubTool is a minimal Tool that sleeps for the given duration before returning.
type stubTool struct {
	sleep time.Duration
}

func (s *stubTool) Definition() tool.Definition {
	return tool.Definition{Name: "stub", Description: "stub", InputSchema: &jsonschema.Schema{}}
}

func (s *stubTool) Run(ctx context.Context, _ tool.Call) (*tool.Result, error) {
	select {
	case <-time.After(s.sleep):
		return &tool.Result{Content: "ok"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestWithTimeout_ZeroOrNegative_ReturnsOriginal(t *testing.T) {
	inner := &stubTool{}
	assert.Same(t, inner, tool.WithTimeout(inner, 0).(*stubTool))
	assert.Same(t, inner, tool.WithTimeout(inner, -time.Second).(*stubTool))
}

func TestWithTimeout_DefinitionPassthrough(t *testing.T) {
	inner := &stubTool{}
	wrapped := tool.WithTimeout(inner, time.Second)
	assert.Equal(t, inner.Definition(), wrapped.Definition())
}

func TestWithTimeout_CompletesBeforeDeadline(t *testing.T) {
	inner := &stubTool{sleep: 10 * time.Millisecond}
	wrapped := tool.WithTimeout(inner, 500*time.Millisecond)

	result, err := wrapped.Run(context.Background(), tool.Call{ID: "id1", Arguments: []byte("{}")})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Content)
}

func TestWithTimeout_ExceedsDeadline(t *testing.T) {
	inner := &stubTool{sleep: 500 * time.Millisecond}
	wrapped := tool.WithTimeout(inner, 20*time.Millisecond)

	_, err := wrapped.Run(context.Background(), tool.Call{ID: "id1", Arguments: []byte("{}")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected DeadlineExceeded, got %v", err)
}

func TestWithTimeout_ParentContextCancelled(t *testing.T) {
	inner := &stubTool{sleep: 500 * time.Millisecond}
	wrapped := tool.WithTimeout(inner, 10*time.Second) // long tool timeout

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := wrapped.Run(ctx, tool.Call{ID: "id1", Arguments: []byte("{}")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected Canceled, got %v", err)
}

func TestWithTimeout_ShorterParentDeadlineWins(t *testing.T) {
	inner := &stubTool{sleep: 500 * time.Millisecond}
	// tool timeout is long, but parent ctx deadline is short
	wrapped := tool.WithTimeout(inner, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := wrapped.Run(ctx, tool.Call{ID: "id1", Arguments: []byte("{}")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected DeadlineExceeded, got %v", err)
}
