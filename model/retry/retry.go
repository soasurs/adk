// Package retry provides generic retry logic with exponential backoff for
// iter.Seq2-based operations such as LLM provider calls.
package retry

import (
	"context"
	"errors"
	"iter"
	"math/rand/v2"
	"strings"
	"time"
)

// Config holds configuration for automatic retry with exponential backoff.
type Config struct {
	// MaxAttempts is the total number of attempts (including the first).
	// A value ≤ 1 disables retries; only one attempt is made.
	MaxAttempts int

	// InitialDelay is the wait time before the first retry.
	InitialDelay time.Duration

	// MaxDelay caps the exponential backoff delay.
	MaxDelay time.Duration

	// Multiplier is the factor by which the delay grows on each retry.
	Multiplier float64

	// Jitter adds ±25 % random noise to the delay to avoid thundering-herd
	// problems when many callers retry simultaneously.
	Jitter bool
}

// DefaultConfig returns a sensible default Config.
// It retries up to 3 times with exponential backoff starting at 1 s, capped at 60 s.
func DefaultConfig() Config {
	return Config{
		MaxAttempts:  3,
		InitialDelay: time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
		Jitter:       true,
	}
}

// Seq2 wraps fn with retry logic for iter.Seq2-based operations.
//
// On each attempt fn is called to obtain a fresh iterator. Values are forwarded
// to the caller via yield. If the iterator produces an error and the error is
// retryable (see IsRetryable), Seq2 waits the appropriate backoff duration and
// calls fn again, up to cfg.MaxAttempts times total.
//
// The isPartial predicate is called for every successfully yielded value. Once
// a partial/streaming value has been forwarded to the caller, retry is disabled
// for the remainder of that call to prevent delivering duplicate content.
//
// If cfg.MaxAttempts ≤ 1, fn() is returned directly with no wrapping overhead.
func Seq2[V any](
	ctx context.Context,
	cfg Config,
	fn func() iter.Seq2[V, error],
	isPartial func(V) bool,
) iter.Seq2[V, error] {
	if cfg.MaxAttempts <= 1 {
		return fn()
	}
	return func(yield func(V, error) bool) {
		var zero V
		delay := cfg.InitialDelay

		for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
			// Apply backoff wait before every retry (not before the first attempt).
			if attempt > 0 {
				wait := delay
				if cfg.Jitter {
					// Add ±25 % random jitter.
					jitter := time.Duration(float64(wait) * 0.25 * (rand.Float64()*2 - 1))
					wait += jitter
					if wait < 0 {
						wait = 0
					}
				}
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					yield(zero, ctx.Err())
					return
				}
				delay = nextDelay(delay, cfg)
			}

			shouldRetry := false
			hadPartial := false

			for v, err := range fn() {
				if err != nil {
					lastAttempt := attempt+1 >= cfg.MaxAttempts
					if !hadPartial && !lastAttempt && IsRetryable(err) {
						// Transient error before any partial data was sent: schedule retry.
						shouldRetry = true
					} else {
						// Non-retryable, exhausted attempts, or partial data already sent:
						// forward the error to the caller.
						yield(zero, err)
					}
					break // stop consuming the current inner iterator
				}
				if isPartial != nil && isPartial(v) {
					hadPartial = true
				}
				if !yield(v, nil) {
					return // caller stopped iteration
				}
			}

			if !shouldRetry {
				return // success, or error already forwarded above
			}
			// shouldRetry == true → continue to next attempt
		}
	}
}

// nextDelay returns the next backoff delay by applying the multiplier and
// capping the result at MaxDelay.
func nextDelay(current time.Duration, cfg Config) time.Duration {
	next := time.Duration(float64(current) * cfg.Multiplier)
	if next > cfg.MaxDelay {
		next = cfg.MaxDelay
	}
	return next
}

// httpStatusCoder is an optional interface implemented by HTTP client errors that
// expose their HTTP response status code. Most LLM SDK error types satisfy this.
type httpStatusCoder interface {
	StatusCode() int
}

// IsRetryable reports whether err represents a transient failure that warrants a
// retry. The following are considered retryable:
//   - HTTP 429 Too Many Requests (rate limit)
//   - HTTP 5xx Server Errors (500–599)
//   - Common network-level errors (connection refused/reset, timeout, EOF)
//
// Context cancellation and deadline exceeded are explicitly non-retryable.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Context cancellations are not transient.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Prefer a structured status-code check via the common SDK error interface.
	var sc httpStatusCoder
	if errors.As(err, &sc) {
		code := sc.StatusCode()
		return code == 429 || (code >= 500 && code <= 599)
	}
	// Fallback: inspect the error string for known retryable patterns.
	// This handles SDK errors that embed the HTTP status in their message and
	// network-level errors that do not expose a StatusCode method.
	msg := strings.ToLower(err.Error())
	for _, pattern := range []string{
		"429", "rate limit", "rate_limit", "too many requests",
		"500", "502", "503", "504",
		"internal server error", "bad gateway",
		"service unavailable", "gateway timeout",
		"connection refused", "connection reset",
		"connection timed out", "i/o timeout",
		"dial tcp", "eof",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}
