package trace

import "context"

type tracerContextKey struct{}
type runInfoContextKey struct{}

// ContextWithTracer returns a child context that carries tracer.
func ContextWithTracer(ctx context.Context, tracer Tracer) context.Context {
	if tracer == nil {
		tracer = DiscardTracer{}
	}
	return context.WithValue(ctx, tracerContextKey{}, tracer)
}

// TracerFromContext returns the tracer stored in ctx, or DiscardTracer when
// none is present.
func TracerFromContext(ctx context.Context) Tracer {
	tracer, ok := ctx.Value(tracerContextKey{}).(Tracer)
	if !ok || tracer == nil {
		return DiscardTracer{}
	}
	return tracer
}

// ContextWithRunInfo returns a child context that carries run correlation
// identifiers.
func ContextWithRunInfo(ctx context.Context, info RunInfo) context.Context {
	return context.WithValue(ctx, runInfoContextKey{}, info)
}

// RunInfoFromContext returns the run identifiers stored in ctx.
func RunInfoFromContext(ctx context.Context) (RunInfo, bool) {
	info, ok := ctx.Value(runInfoContextKey{}).(RunInfo)
	return info, ok
}

// Start starts a span using the tracer stored in ctx. Missing run identifiers
// in event are filled from context.
func Start(ctx context.Context, event Event) (context.Context, Span) {
	info, hasInfo := RunInfoFromContext(ctx)
	if hasInfo {
		event = event.WithRunInfo(info)
	}
	ctx, span := TracerFromContext(ctx).Start(ctx, event)
	if span == nil {
		span = discardSpan{}
	}
	return ctx, runInfoSpan{span: span, info: info, hasInfo: hasInfo}
}

type runInfoSpan struct {
	span    Span
	info    RunInfo
	hasInfo bool
}

func (s runInfoSpan) AddEvent(ctx context.Context, event Event) {
	s.span.AddEvent(ctx, s.withRunInfo(ctx, event))
}

func (s runInfoSpan) End(ctx context.Context, event Event) {
	s.span.End(ctx, s.withRunInfo(ctx, event))
}

func (s runInfoSpan) withRunInfo(ctx context.Context, event Event) Event {
	if info, ok := RunInfoFromContext(ctx); ok {
		return event.WithRunInfo(info)
	}
	if s.hasInfo {
		return event.WithRunInfo(s.info)
	}
	return event
}
