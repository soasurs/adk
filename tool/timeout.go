package tool

import (
	"context"
	"time"
)

// withTimeout wraps a Tool and bounds each Run call with a fixed deadline.
type withTimeout struct {
	inner   Tool
	timeout time.Duration
}

// WithTimeout wraps t so that each Run invocation is bounded by the given
// timeout. The timeout is applied on top of any deadline already present in
// the incoming context, so the shorter of the two wins. A zero or negative
// duration is a no-op and returns t unchanged.
func WithTimeout(t Tool, d time.Duration) Tool {
	if d <= 0 {
		return t
	}
	return &withTimeout{inner: t, timeout: d}
}

func (w *withTimeout) Definition() Definition {
	return w.inner.Definition()
}

func (w *withTimeout) Run(ctx context.Context, toolCallID string, arguments string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	return w.inner.Run(ctx, toolCallID, arguments)
}
