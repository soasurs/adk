package runner

import adktrace "github.com/soasurs/adk/trace"

// Option configures a Runner.
type Option func(*Runner)

// WithTracer configures span-oriented tracing for Runner and the agents it
// invokes. A nil tracer disables tracing.
func WithTracer(tracer adktrace.Tracer) Option {
	return func(r *Runner) {
		if tracer == nil {
			tracer = adktrace.DiscardTracer{}
		}
		r.tracer = tracer
	}
}
