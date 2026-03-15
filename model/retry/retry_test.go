package retry

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

// okSeq returns an iter.Seq2 that yields a single string value.
func okSeq(val string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		yield(val, nil)
	}
}

// errSeq returns an iter.Seq2 that yields a single error.
func errSeq(err error) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		yield("", err)
	}
}

// partialThenErrSeq returns an iter.Seq2 that yields one partial value then an error.
func partialThenErrSeq(partial, err error) iter.Seq2[string, error] {
	_ = partial // unused, just for documentation
	return func(yield func(string, error) bool) {
		if !yield("partial", nil) {
			return
		}
		yield("", err)
	}
}

// zeroDelayConfig returns a Config with zero delays so tests run fast.
func zeroDelayConfig(maxAttempts int) Config {
	return Config{
		MaxAttempts:  maxAttempts,
		InitialDelay: 0,
		MaxDelay:     0,
		Multiplier:   2.0,
		Jitter:       false,
	}
}

// isPartialString reports whether a string value is a partial/streaming fragment.
// In tests, we use the literal value "partial" to signal a partial event.
func isPartialString(s string) bool { return s == "partial" }

// retryableErr matches the "503" retryable pattern.
var retryableErr = errors.New("upstream 503 service unavailable")

// nonRetryableErr does not match any retryable pattern.
var nonRetryableErr = errors.New("invalid request: bad parameter")

// statusErr implements httpStatusCoder.
type statusErr struct {
	code int
	msg  string
}

func (e *statusErr) Error() string   { return e.msg }
func (e *statusErr) StatusCode() int { return e.code }

// makeAttemptFunc returns a function that produces an iter.Seq2[string,error].
// It returns retryableErr for the first (failUntil-1) calls, then okSeq.
func makeAttemptFunc(failUntil int) func() iter.Seq2[string, error] {
	call := 0
	return func() iter.Seq2[string, error] {
		call++
		if call < failUntil {
			return errSeq(retryableErr)
		}
		return okSeq(fmt.Sprintf("ok-%d", call))
	}
}

// ---- IsRetryable ------------------------------------------------------------

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline exceeded", context.DeadlineExceeded, false},
		{"wrapped context canceled", fmt.Errorf("wrap: %w", context.Canceled), false},
		{"429 via statusCoder", &statusErr{429, "rate limited"}, true},
		{"500 via statusCoder", &statusErr{500, "internal error"}, true},
		{"503 via statusCoder", &statusErr{503, "unavailable"}, true},
		{"400 via statusCoder", &statusErr{400, "bad request"}, false},
		{"401 via statusCoder", &statusErr{401, "unauthorized"}, false},
		{"rate limit string", errors.New("rate limit exceeded"), true},
		{"429 string", errors.New("error 429"), true},
		{"503 string", errors.New("503 service unavailable"), true},
		{"eof string", errors.New("unexpected eof"), true},
		{"dial tcp string", errors.New("dial tcp connect: connection refused"), true},
		{"non retryable string", errors.New("invalid api key"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsRetryable(tc.err)
			if got != tc.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ---- nextDelay --------------------------------------------------------------

func TestNextDelay(t *testing.T) {
	cfg := Config{Multiplier: 2.0, MaxDelay: 8 * time.Second}
	steps := []time.Duration{
		nextDelay(1*time.Second, cfg),
		nextDelay(2*time.Second, cfg),
		nextDelay(4*time.Second, cfg),
		nextDelay(8*time.Second, cfg),
	}
	expected := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, got := range steps {
		if got != expected[i] {
			t.Errorf("step %d: got %v, want %v", i, got, expected[i])
		}
	}
}

// ---- Seq2 -------------------------------------------------------------------

func TestSeq2_SuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		return okSeq("pong")
	}
	var got string
	for v, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, nil) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = v
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if got != "pong" {
		t.Errorf("unexpected value: %q", got)
	}
}

func TestSeq2_RetriesOnRetryableError(t *testing.T) {
	fn := makeAttemptFunc(3) // fails for calls 1–2, succeeds on call 3

	var got string
	for v, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, nil) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = v
	}
	if got != "ok-3" {
		t.Errorf("expected ok-3, got %q", got)
	}
}

func TestSeq2_ExhaustsAttemptsAndPropagatesError(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		return errSeq(retryableErr)
	}
	var gotErr error
	for _, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, nil) {
		gotErr = err
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
	if !errors.Is(gotErr, retryableErr) {
		t.Errorf("expected retryableErr, got %v", gotErr)
	}
}

func TestSeq2_DoesNotRetryNonRetryableError(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		return errSeq(nonRetryableErr)
	}
	var gotErr error
	for _, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, nil) {
		gotErr = err
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if !errors.Is(gotErr, nonRetryableErr) {
		t.Errorf("expected nonRetryableErr, got %v", gotErr)
	}
}

func TestSeq2_DoesNotRetryContextCanceled(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		return errSeq(context.Canceled)
	}
	var gotErr error
	for _, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, nil) {
		gotErr = err
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retries for context.Canceled), got %d", calls)
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", gotErr)
	}
}

func TestSeq2_StreamingRetryBeforePartial(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		if calls == 1 {
			return errSeq(retryableErr)
		}
		return okSeq("streamed")
	}
	var got string
	for v, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, isPartialString) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = v
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if got != "streamed" {
		t.Errorf("expected 'streamed', got %q", got)
	}
}

func TestSeq2_StreamingNoRetryAfterPartial(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		// yields a partial value then an error
		return partialThenErrSeq(nil, retryableErr)
	}
	var partials int
	var gotErr error
	for v, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, isPartialString) {
		if err != nil {
			gotErr = err
			break
		}
		if v == "partial" {
			partials++
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry after partial), got %d", calls)
	}
	if partials != 1 {
		t.Errorf("expected 1 partial event, got %d", partials)
	}
	if !errors.Is(gotErr, retryableErr) {
		t.Errorf("expected retryableErr, got %v", gotErr)
	}
}

func TestSeq2_MaxAttemptsOne_CallsFnDirectly(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		return okSeq("direct")
	}
	cfg := Config{MaxAttempts: 1}
	for _, err := range Seq2(context.Background(), cfg, fn, nil) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestSeq2_ContextCancelledDuringBackoff(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		return errSeq(retryableErr)
	}
	cfg := Config{
		MaxAttempts:  5,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     time.Second,
		Multiplier:   2.0,
		Jitter:       false,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	var gotErr error
	for _, err := range Seq2(ctx, cfg, fn, nil) {
		gotErr = err
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", gotErr)
	}
	if calls > 2 {
		t.Errorf("expected at most 2 calls before cancel, got %d", calls)
	}
}

func TestSeq2_StatusCodeInterface_429(t *testing.T) {
	calls := 0
	fn := func() iter.Seq2[string, error] {
		calls++
		if calls == 1 {
			return errSeq(&statusErr{429, "rate limited"})
		}
		return okSeq("ok")
	}
	var got string
	for v, err := range Seq2(context.Background(), zeroDelayConfig(3), fn, nil) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = v
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if got != "ok" {
		t.Errorf("expected 'ok', got %q", got)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxAttempts != 3 {
		t.Errorf("MaxAttempts: got %d, want 3", cfg.MaxAttempts)
	}
	if cfg.InitialDelay != time.Second {
		t.Errorf("InitialDelay: got %v, want 1s", cfg.InitialDelay)
	}
	if cfg.MaxDelay != 60*time.Second {
		t.Errorf("MaxDelay: got %v, want 60s", cfg.MaxDelay)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("Multiplier: got %v, want 2.0", cfg.Multiplier)
	}
	if !cfg.Jitter {
		t.Error("Jitter: got false, want true")
	}
}
