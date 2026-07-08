package otel

import (
	"context"
	"testing"

	adktrace "github.com/soasurs/adk/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type recordingOTelProvider struct {
	noop.TracerProvider
	span *recordingOTelSpan
}

func (p *recordingOTelProvider) Tracer(string, ...oteltrace.TracerOption) oteltrace.Tracer {
	return &recordingOTelTracer{provider: p}
}

type recordingOTelTracer struct {
	noop.Tracer
	provider *recordingOTelProvider
}

func (t *recordingOTelTracer) Start(ctx context.Context, _ string, _ ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	span := &recordingOTelSpan{}
	t.provider.span = span
	return ctx, span
}

type recordingOTelSpan struct {
	noop.Span
	statusCode        codes.Code
	statusDescription string
}

func (s *recordingOTelSpan) SetStatus(code codes.Code, description string) {
	s.statusCode = code
	s.statusDescription = description
}

func TestNewTracer_NoopProviderDoesNotPanic(t *testing.T) {
	tracer := NewTracer()

	ctx, span := tracer.Start(t.Context(), adktrace.Event{
		Kind:      adktrace.KindRunnerRun,
		RunID:     "run-1",
		SessionID: "session-1",
	})
	span.AddEvent(ctx, adktrace.Event{Kind: adktrace.KindLLMCall})
	span.End(ctx, adktrace.Event{Kind: adktrace.KindRunnerRun})
}

func TestNewTracer_ExtraAttributeFunc(t *testing.T) {
	called := false
	tracer := NewTracer(WithExtraAttributeFunc(func(event adktrace.Event) []attribute.KeyValue {
		called = true
		return []attribute.KeyValue{attribute.String("test.kind", string(event.Kind))}
	}))

	_, span := tracer.Start(t.Context(), adktrace.Event{Kind: adktrace.KindToolCall})
	span.End(t.Context(), adktrace.Event{Kind: adktrace.KindToolCall})

	assert.True(t, called)
}

func TestDefaultAttributes_IncludesZeroToolIndexForToolCalls(t *testing.T) {
	attrs := defaultAttributes(adktrace.Event{
		Kind:      adktrace.KindToolCall,
		ToolName:  "lookup",
		ToolIndex: 0,
	})

	assert.Contains(t, attrs, attribute.Int("adk.tool.index", 0))
}

func TestNewTracer_IsErrorSetsOTelStatus(t *testing.T) {
	provider := new(recordingOTelProvider)
	tracer := NewTracer(WithTracerProvider(provider))

	_, span := tracer.Start(t.Context(), adktrace.Event{Kind: adktrace.KindToolCall})
	span.End(t.Context(), adktrace.Event{
		Kind:    adktrace.KindToolCall,
		IsError: true,
	})

	require.NotNil(t, provider.span)
	assert.Equal(t, codes.Error, provider.span.statusCode)
	assert.Equal(t, "adk operation reported an error", provider.span.statusDescription)
}
