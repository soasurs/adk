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

// WithProjector configures how durable Turns and Events are converted into
// Agent context. A nil projector restores the default durable-Turn projector.
// Runner validates the projected tool protocol regardless of this option, but
// custom implementations remain responsible for the semantic truth of any
// tool results they create.
func WithProjector(projector Projector) Option {
	return func(r *Runner) {
		if projector == nil {
			projector = NewDefaultProjector()
		}
		r.projector = projector
	}
}

// WithFailureClassifier configures conversion of execution errors into safe,
// structured Turn failure metadata. It must return nil or a valid TurnFailure
// and must not copy arbitrary error text. A nil classifier restores the default.
func WithFailureClassifier(classifier FailureClassifier) Option {
	return func(r *Runner) {
		if classifier == nil {
			classifier = DefaultFailureClassifier
		}
		r.failureClassifier = classifier
	}
}
