// Package otel maps ADK trace spans to OpenTelemetry spans.
package otel

import (
	"context"
	"fmt"

	adktrace "github.com/soasurs/adk/trace"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	defaultInstrumentationName = "github.com/soasurs/adk"
	defaultSpanName            = "adk.operation"
)

// AttributeFunc maps an ADK trace event to OpenTelemetry span attributes.
type AttributeFunc func(adktrace.Event) []attribute.KeyValue

// Option configures an OpenTelemetry ADK tracer.
type Option func(*config)

// WithTracerProvider configures the TracerProvider used to create spans.
func WithTracerProvider(provider oteltrace.TracerProvider) Option {
	return func(c *config) {
		if provider != nil {
			c.provider = provider
		}
	}
}

// WithInstrumentationName configures the OTel instrumentation scope name.
func WithInstrumentationName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.instrumentationName = name
		}
	}
}

// WithInstrumentationVersion configures the OTel instrumentation scope version.
func WithInstrumentationVersion(version string) Option {
	return func(c *config) {
		c.instrumentationVersion = version
	}
}

// WithAttributeFunc replaces the default ADK-to-OTel attribute mapper.
func WithAttributeFunc(fn AttributeFunc) Option {
	return func(c *config) {
		if fn != nil {
			c.attributeFunc = fn
		}
	}
}

// WithExtraAttributeFunc appends attributes from fn after the default mapper.
func WithExtraAttributeFunc(fn AttributeFunc) Option {
	return func(c *config) {
		if fn != nil {
			c.extraAttributeFuncs = append(c.extraAttributeFuncs, fn)
		}
	}
}

// WithSensitiveAttributes includes session, user, and app identifiers in span
// attributes. They are omitted by default to avoid leaking tenant or end-user
// identifiers into telemetry backends.
func WithSensitiveAttributes(enabled bool) Option {
	return func(c *config) {
		c.sensitiveAttributes = enabled
	}
}

type config struct {
	provider               oteltrace.TracerProvider
	instrumentationName    string
	instrumentationVersion string
	attributeFunc          AttributeFunc
	extraAttributeFuncs    []AttributeFunc
	sensitiveAttributes    bool
}

// NewTracer creates an ADK trace.Tracer backed by OpenTelemetry spans.
func NewTracer(opts ...Option) adktrace.Tracer {
	cfg := &config{
		provider:            otelapi.GetTracerProvider(),
		instrumentationName: defaultInstrumentationName,
		attributeFunc:       defaultAttributes,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	tracerOpts := []oteltrace.TracerOption(nil)
	if cfg.instrumentationVersion != "" {
		tracerOpts = append(tracerOpts, oteltrace.WithInstrumentationVersion(cfg.instrumentationVersion))
	}
	return &tracer{
		cfg:    cfg,
		tracer: cfg.provider.Tracer(cfg.instrumentationName, tracerOpts...),
	}
}

type tracer struct {
	cfg    *config
	tracer oteltrace.Tracer
}

func (t *tracer) Start(ctx context.Context, event adktrace.Event) (context.Context, adktrace.Span) {
	opts := []oteltrace.SpanStartOption{
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(t.attrs(event)...),
	}
	if !event.Time.IsZero() {
		opts = append(opts, oteltrace.WithTimestamp(event.Time))
	}
	ctx, span := t.tracer.Start(ctx, spanName(event), opts...)
	return ctx, &spanWrapper{cfg: t.cfg, span: span, kind: event.Kind}
}

type spanWrapper struct {
	cfg  *config
	span oteltrace.Span
	kind adktrace.Kind
}

func (s *spanWrapper) AddEvent(_ context.Context, event adktrace.Event) {
	if event.Kind == "" {
		event.Kind = s.kind
	}
	opts := []oteltrace.EventOption{oteltrace.WithAttributes(s.attrs(event)...)}
	if !event.Time.IsZero() {
		opts = append(opts, oteltrace.WithTimestamp(event.Time))
	}
	s.span.AddEvent(spanName(event), opts...)
}

func (s *spanWrapper) End(_ context.Context, event adktrace.Event) {
	if event.Kind == "" {
		event.Kind = s.kind
	}
	if attrs := s.attrs(event); len(attrs) > 0 {
		s.span.SetAttributes(attrs...)
	}
	if event.Err != nil {
		s.span.RecordError(event.Err)
		s.span.SetStatus(codes.Error, event.Err.Error())
	} else if event.ToolOutcome == adktrace.ToolOutcomeFailure {
		s.span.SetStatus(codes.Error, "adk operation reported an error")
	}
	opts := []oteltrace.SpanEndOption(nil)
	if !event.Time.IsZero() {
		opts = append(opts, oteltrace.WithTimestamp(event.Time))
	}
	s.span.End(opts...)
}

func (s *spanWrapper) attrs(event adktrace.Event) []attribute.KeyValue {
	return collectAttributes(s.cfg, event)
}

func (t *tracer) attrs(event adktrace.Event) []attribute.KeyValue {
	return collectAttributes(t.cfg, event)
}

func collectAttributes(cfg *config, event adktrace.Event) []attribute.KeyValue {
	attrs := cfg.attributeFunc(event)
	if cfg.sensitiveAttributes {
		attrs = appendSensitiveAttributes(attrs, event)
	}
	for _, fn := range cfg.extraAttributeFuncs {
		attrs = append(attrs, fn(event)...)
	}
	return attrs
}

func spanName(event adktrace.Event) string {
	if event.Kind != "" {
		return string(event.Kind)
	}
	return defaultSpanName
}

func defaultAttributes(event adktrace.Event) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 24)
	addString := func(key, value string) {
		if value != "" {
			attrs = append(attrs, attribute.String(key, value))
		}
	}
	addInt := func(key string, value int) {
		if value != 0 {
			attrs = append(attrs, attribute.Int(key, value))
		}
	}
	addInt64 := func(key string, value int64) {
		if value != 0 {
			attrs = append(attrs, attribute.Int64(key, value))
		}
	}
	addToolIndex := func() {
		if event.ToolIndex != 0 || event.Kind == adktrace.KindToolCall || event.ToolName != "" || event.ToolCallID != "" {
			attrs = append(attrs, attribute.Int("adk.tool.index", event.ToolIndex))
		}
	}

	addString("adk.kind", string(event.Kind))
	addString("adk.run_id", event.RunID)
	addString("adk.turn_id", event.TurnID)
	addString("adk.agent.name", event.AgentName)
	addString("adk.model", event.Model)
	addInt("adk.iteration", event.Iteration)
	if event.Stream {
		attrs = append(attrs, attribute.Bool("adk.stream", true))
	}
	addInt64("adk.event.id", event.EventID)
	addString("adk.event.author", event.EventAuthor)
	addString("adk.event.role", string(event.EventRole))
	addInt("adk.event.count", event.EventCount)
	if event.Partial {
		attrs = append(attrs, attribute.Bool("adk.event.partial", true))
	}
	addString("adk.tool.name", event.ToolName)
	addString("adk.tool.call_id", event.ToolCallID)
	addToolIndex()
	addString("adk.finish_reason", string(event.FinishReason))
	addInt64("adk.usage.prompt_tokens", event.PromptTokens)
	addInt64("adk.usage.completion_tokens", event.CompletionTokens)
	addInt64("adk.usage.total_tokens", event.TotalTokens)
	addInt("adk.partial_responses", event.PartialResponses)
	if event.StoppedEarly {
		attrs = append(attrs, attribute.Bool("adk.stopped_early", true))
	}
	addString("adk.tool.outcome", string(event.ToolOutcome))
	for key, value := range event.Attributes {
		if attr, ok := attributeValue("adk.attr."+key, value); ok {
			attrs = append(attrs, attr)
		}
	}
	return attrs
}

func appendSensitiveAttributes(attrs []attribute.KeyValue, event adktrace.Event) []attribute.KeyValue {
	if event.SessionID != "" {
		attrs = append(attrs, attribute.String("adk.session_id", event.SessionID))
	}
	if event.AppID != "" {
		attrs = append(attrs, attribute.String("adk.app_id", event.AppID))
	}
	if event.UserID != "" {
		attrs = append(attrs, attribute.String("adk.user_id", event.UserID))
	}
	return attrs
}

func attributeValue(key string, value any) (attribute.KeyValue, bool) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return attribute.KeyValue{}, false
		}
		return attribute.String(key, v), true
	case bool:
		return attribute.Bool(key, v), true
	case int:
		if v == 0 {
			return attribute.KeyValue{}, false
		}
		return attribute.Int(key, v), true
	case int64:
		if v == 0 {
			return attribute.KeyValue{}, false
		}
		return attribute.Int64(key, v), true
	case float64:
		if v == 0 {
			return attribute.KeyValue{}, false
		}
		return attribute.Float64(key, v), true
	case fmt.Stringer:
		return attribute.String(key, v.String()), true
	default:
		if value == nil {
			return attribute.KeyValue{}, false
		}
		return attribute.String(key, fmt.Sprint(value)), true
	}
}
