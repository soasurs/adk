package trace

import (
	"context"
	"log/slog"
	"time"
)

// NewSlogTracer returns a Tracer that logs span start, events, and end records
// through slog.
func NewSlogTracer(logger *slog.Logger) Tracer {
	if logger == nil {
		logger = slog.Default()
	}
	return &slogTracer{logger: logger}
}

type slogTracer struct {
	logger *slog.Logger
}

func (t *slogTracer) Start(ctx context.Context, event Event) (context.Context, Span) {
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	t.logger.LogAttrs(ctx, slog.LevelInfo, "adk span start", slogAttrs(event)...)
	return ctx, &slogSpan{
		logger: t.logger,
		kind:   event.Kind,
		start:  event.Time,
	}
}

type slogSpan struct {
	logger *slog.Logger
	kind   Kind
	start  time.Time
}

func (s *slogSpan) AddEvent(ctx context.Context, event Event) {
	if event.Kind == "" {
		event.Kind = s.kind
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "adk span event", slogAttrs(event)...)
}

func (s *slogSpan) End(ctx context.Context, event Event) {
	if event.Kind == "" {
		event.Kind = s.kind
	}
	if event.Duration == 0 && !s.start.IsZero() {
		event.Duration = time.Since(s.start)
	}
	attrs := slogAttrs(event)
	if event.Err != nil {
		attrs = append(attrs, slog.String("error", event.Err.Error()))
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "adk span end", attrs...)
}

func slogAttrs(event Event) []slog.Attr {
	attrs := make([]slog.Attr, 0, 24)
	addString := func(key, value string) {
		if value != "" {
			attrs = append(attrs, slog.String(key, value))
		}
	}
	addInt := func(key string, value int) {
		if value != 0 {
			attrs = append(attrs, slog.Int(key, value))
		}
	}
	addInt64 := func(key string, value int64) {
		if value != 0 {
			attrs = append(attrs, slog.Int64(key, value))
		}
	}

	addString("kind", string(event.Kind))
	addString("run_id", event.RunID)
	addString("turn_id", event.TurnID)
	addString("session_id", event.SessionID)
	addString("app_id", event.AppID)
	addString("user_id", event.UserID)
	addString("agent", event.AgentName)
	addString("model", event.Model)
	addInt("iteration", event.Iteration)
	if event.Stream {
		attrs = append(attrs, slog.Bool("stream", true))
	}
	addInt64("event_id", event.EventID)
	addString("event_author", event.EventAuthor)
	addString("event_role", string(event.EventRole))
	addInt("event_count", event.EventCount)
	if event.Partial {
		attrs = append(attrs, slog.Bool("partial", true))
	}
	addString("tool", event.ToolName)
	addString("tool_call_id", event.ToolCallID)
	addInt("tool_index", event.ToolIndex)
	addString("finish_reason", string(event.FinishReason))
	addInt64("prompt_tokens", event.PromptTokens)
	addInt64("completion_tokens", event.CompletionTokens)
	addInt64("total_tokens", event.TotalTokens)
	addInt("partial_responses", event.PartialResponses)
	if event.StoppedEarly {
		attrs = append(attrs, slog.Bool("stopped_early", true))
	}
	if event.IsError {
		attrs = append(attrs, slog.Bool("is_error", true))
	}
	if event.Duration > 0 {
		attrs = append(attrs, slog.Duration("duration", event.Duration))
	}
	for key, value := range event.Attributes {
		attrs = append(attrs, slog.Any("attr."+key, value))
	}
	return attrs
}
