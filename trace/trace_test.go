package trace

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingTracer struct {
	starts []Event
	events []Event
	ends   []Event
}

func (t *recordingTracer) Start(ctx context.Context, event Event) (context.Context, Span) {
	t.starts = append(t.starts, event)
	return ctx, &recordingSpan{tracer: t}
}

type recordingSpan struct {
	tracer *recordingTracer
}

func (s *recordingSpan) AddEvent(_ context.Context, event Event) {
	s.tracer.events = append(s.tracer.events, event)
}

func (s *recordingSpan) End(_ context.Context, event Event) {
	s.tracer.ends = append(s.tracer.ends, event)
}

func TestContextTracerAndRunInfo(t *testing.T) {
	rec := new(recordingTracer)
	info := RunInfo{
		RunID:     "run-1",
		TurnID:    "turn-1",
		SessionID: "session-1",
		AppID:     "app-1",
		UserID:    "user-1",
	}
	ctx := ContextWithRunInfo(ContextWithTracer(t.Context(), rec), info)

	_, span := Start(ctx, Event{Kind: KindRunnerRun})
	span.AddEvent(ctx, Event{Kind: KindLLMCall})
	span.End(ctx, Event{Kind: KindRunnerRun})

	require.Len(t, rec.starts, 1)
	assert.Equal(t, KindRunnerRun, rec.starts[0].Kind)
	assert.Equal(t, "run-1", rec.starts[0].RunID)
	assert.Equal(t, "turn-1", rec.starts[0].TurnID)
	assert.Equal(t, "session-1", rec.starts[0].SessionID)
	require.Len(t, rec.events, 1)
	assert.Equal(t, KindLLMCall, rec.events[0].Kind)
	assert.Equal(t, "run-1", rec.events[0].RunID)
	assert.Equal(t, "turn-1", rec.events[0].TurnID)
	assert.Equal(t, "session-1", rec.events[0].SessionID)
	require.Len(t, rec.ends, 1)
	assert.Equal(t, KindRunnerRun, rec.ends[0].Kind)
	assert.Equal(t, "run-1", rec.ends[0].RunID)
	assert.Equal(t, "turn-1", rec.ends[0].TurnID)
	assert.Equal(t, "session-1", rec.ends[0].SessionID)
}

func TestDiscardTracer_Noop(t *testing.T) {
	ctx, span := DiscardTracer{}.Start(t.Context(), Event{Kind: KindRunnerRun})
	span.AddEvent(ctx, Event{Kind: KindLLMCall})
	span.End(ctx, Event{Kind: KindRunnerRun})
}

func TestNewSlogTracer_LogsSpan(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	tracer := NewSlogTracer(logger)

	ctx, span := tracer.Start(t.Context(), Event{Kind: KindToolCall, ToolName: "echo"})
	span.AddEvent(ctx, Event{Kind: KindToolCall, Attributes: map[string]any{"phase": "middle"}})
	span.End(ctx, Event{Kind: KindToolCall})

	output := buf.String()
	assert.Contains(t, output, "adk span start")
	assert.Contains(t, output, "kind=adk.tool.call")
	assert.Contains(t, output, "tool=echo")
	assert.Contains(t, output, "adk span end")
}
