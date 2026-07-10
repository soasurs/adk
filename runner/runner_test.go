package runner

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/event"
	memorysession "github.com/soasurs/adk/session/memory"
	adktrace "github.com/soasurs/adk/trace"
)

// ---------------------------------------------------------------------------
// Mock Agent
// ---------------------------------------------------------------------------

// mockAgent is a test double for agent.Agent.
type mockAgent struct {
	name        string
	description string
	// runFunc is called by Run to produce the event sequence.
	runFunc func(ctx context.Context, events []model.Event) iter.Seq2[*model.Event, error]
	// capturedMessages records the event history argument passed to the last Run call.
	capturedMessages []model.Event
}

func (m *mockAgent) Name() string        { return m.name }
func (m *mockAgent) Description() string { return m.description }
func (m *mockAgent) Run(ctx context.Context, events []model.Event) iter.Seq2[*model.Event, error] {
	m.capturedMessages = events
	return m.runFunc(ctx, events)
}

// staticAgent returns a fixed slice of complete (non-partial) events.
func staticAgent(msgs ...model.Content) *mockAgent {
	return &mockAgent{
		name:        "static-agent",
		description: "yields fixed messages",
		runFunc: func(_ context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				for _, m := range msgs {
					if !yield(&model.Event{Content: m, Partial: false}, nil) {
						return
					}
				}
			}
		},
	}
}

// errorAgent always yields a single error.
func errorAgent(err error) *mockAgent {
	return &mockAgent{
		name:        "error-agent",
		description: "always errors",
		runFunc: func(_ context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				yield(nil, err)
			}
		},
	}
}

// collectRun drains all complete events from runner.Run into a message slice.
func collectRun(t *testing.T, r *Runner, sessionID string, input model.Content) ([]model.Content, error) {
	t.Helper()
	var msgs []model.Content
	for event, err := range r.Run(t.Context(), sessionID, input) {
		if err != nil {
			return msgs, err
		}
		if !event.Partial {
			msgs = append(msgs, event.Content)
		}
	}
	return msgs, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newRunnerWithSession creates a Runner backed by an in-memory session service
// and pre-creates a session.
func newRunnerWithSession(t *testing.T, a *mockAgent) (*Runner, string) {
	t.Helper()
	const sessionID = "session-1"
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(t.Context(), session.CreateSessionRequest{
		SessionID: sessionID,
		AppID:     "runner-test",
		UserID:    "user-1",
	})
	require.NoError(t, err)

	r, err := New(a, svc)
	require.NoError(t, err)
	return r, sessionID
}

type recordedSpan struct {
	kind  adktrace.Kind
	event adktrace.Event
}

type recordingTracer struct {
	starts []adktrace.Event
	ends   []recordedSpan
}

func (t *recordingTracer) Start(ctx context.Context, event adktrace.Event) (context.Context, adktrace.Span) {
	t.starts = append(t.starts, event)
	return ctx, &recordingSpan{tracer: t, kind: event.Kind}
}

type recordingSpan struct {
	tracer *recordingTracer
	kind   adktrace.Kind
}

func (s *recordingSpan) AddEvent(context.Context, adktrace.Event) {}

func (s *recordingSpan) End(_ context.Context, event adktrace.Event) {
	s.tracer.ends = append(s.tracer.ends, recordedSpan{kind: s.kind, event: event})
}

func startedKinds(tracer *recordingTracer) []adktrace.Kind {
	kinds := make([]adktrace.Kind, len(tracer.starts))
	for i, event := range tracer.starts {
		kinds[i] = event.Kind
	}
	return kinds
}

func endedSpan(tracer *recordingTracer, kind adktrace.Kind) (adktrace.Event, bool) {
	for _, span := range tracer.ends {
		if span.kind == kind {
			return span.event, true
		}
	}
	return adktrace.Event{}, false
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunner_Run_Basic verifies that a single user turn is forwarded to the
// agent and that the resulting assistant message is both yielded and persisted.
func TestRunner_Run_Basic(t *testing.T) {
	reply := model.Content{Role: model.RoleAssistant, Content: "pong"}
	a := staticAgent(reply)

	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, model.Content{Content: "ping"})

	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, "pong", msgs[0].Content)
}

func TestRunner_Run_Tracing(t *testing.T) {
	tracer := new(recordingTracer)
	a := &mockAgent{
		name:        "trace-agent",
		description: "checks trace context",
		runFunc: func(ctx context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			info, ok := adktrace.RunInfoFromContext(ctx)
			require.True(t, ok)
			assert.NotEmpty(t, info.RunID)
			assert.NotEmpty(t, info.TurnID)
			assert.Equal(t, "session-1", info.SessionID)
			assert.Equal(t, "runner-test", info.AppID)
			assert.Equal(t, "user-1", info.UserID)
			return func(yield func(*model.Event, error) bool) {
				yield(&model.Event{
					Author: "trace-agent",
					Content: model.Content{
						Role:    model.RoleAssistant,
						Content: "pong",
					},
				}, nil)
			}
		},
	}
	const sessionID = "session-1"
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(t.Context(), session.CreateSessionRequest{
		SessionID: sessionID,
		AppID:     "runner-test",
		UserID:    "user-1",
	})
	require.NoError(t, err)
	r, err := New(a, svc, WithTracer(tracer))
	require.NoError(t, err)

	_, err = collectRun(t, r, sessionID, model.Content{Content: "ping"})
	require.NoError(t, err)

	kinds := startedKinds(tracer)
	assert.Contains(t, kinds, adktrace.KindRunnerRun)
	assert.Contains(t, kinds, adktrace.KindRunnerLock)
	assert.Contains(t, kinds, adktrace.KindSessionLoad)
	assert.Contains(t, kinds, adktrace.KindEventPersist)
	assert.Contains(t, kinds, adktrace.KindAgentRun)

	runEnd, ok := endedSpan(tracer, adktrace.KindRunnerRun)
	require.True(t, ok)
	assert.NotEmpty(t, runEnd.RunID)
	assert.NotEmpty(t, runEnd.TurnID)
	assert.Equal(t, sessionID, runEnd.SessionID)
	assert.NoError(t, runEnd.Err)
}

// TestRunner_Run_MultipleAgentMessages verifies that all messages yielded by
// the agent are forwarded to the caller.
func TestRunner_Run_MultipleAgentMessages(t *testing.T) {
	tool := model.Content{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: "tc1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)}},
	}
	toolResult := model.Content{Role: model.RoleTool, Content: "hi", ToolCallID: "tc1"}
	final := model.Content{Role: model.RoleAssistant, Content: "Done."}

	a := staticAgent(tool, toolResult, final)
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, model.Content{Content: "echo hi"})

	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, model.RoleTool, msgs[1].Role)
	assert.Equal(t, model.RoleAssistant, msgs[2].Role)
}

// TestRunner_Run_UserMessagePrependedToAgent verifies that the incoming user
// message is included in the messages slice passed to the agent, after any
// pre-existing session history.
func TestRunner_Run_UserMessagePrependedToAgent(t *testing.T) {
	a := staticAgent(model.Content{Role: model.RoleAssistant, Content: "ok"})
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, model.Content{Content: "hello"})
	require.NoError(t, err)

	// The agent must have received exactly the user event.
	require.Len(t, a.capturedMessages, 1)
	assert.Equal(t, model.RoleUser, a.capturedMessages[0].Content.Role)
	assert.Equal(t, "hello", a.capturedMessages[0].Content.Content)
}

// TestRunner_Run_HistoryPassedToAgent verifies that messages already stored in
// the session are prepended to the agent's input on the next turn.
func TestRunner_Run_HistoryPassedToAgent(t *testing.T) {
	a := staticAgent(model.Content{Role: model.RoleAssistant, Content: "ok"})
	r, sessionID := newRunnerWithSession(t, a)

	// First turn – produces one user + one assistant message in session.
	_, err := collectRun(t, r, sessionID, model.Content{Content: "turn 1"})
	require.NoError(t, err)

	// Second turn.
	_, err = collectRun(t, r, sessionID, model.Content{Content: "turn 2"})
	require.NoError(t, err)

	// Agent should have received: user(turn1) + assistant(ok) + user(turn2).
	require.Len(t, a.capturedMessages, 3)
	assert.Equal(t, model.RoleUser, a.capturedMessages[0].Content.Role)
	assert.Equal(t, "turn 1", a.capturedMessages[0].Content.Content)
	assert.Equal(t, model.RoleAssistant, a.capturedMessages[1].Content.Role)
	assert.Equal(t, model.RoleUser, a.capturedMessages[2].Content.Role)
	assert.Equal(t, "turn 2", a.capturedMessages[2].Content.Content)
}

// TestRunner_Run_MessagesPersistedToSession verifies that the user message and
// all agent replies are stored in the session so subsequent turns see them.
func TestRunner_Run_MessagesPersistedToSession(t *testing.T) {
	reply := model.Content{Role: model.RoleAssistant, Content: "stored"}
	a := staticAgent(reply)
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, model.Content{Content: "persist me"})
	require.NoError(t, err)

	// Retrieve raw session to inspect stored messages.
	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, sess)

	stored, err := sess.GetEvents(t.Context(), 100, 0)
	require.NoError(t, err)
	// Expect: user + assistant.
	require.Len(t, stored, 2)
	assert.Equal(t, string(model.RoleUser), stored[0].Role)
	assert.Equal(t, "persist me", stored[0].Content)
	assert.Equal(t, string(model.RoleAssistant), stored[1].Role)
	assert.Equal(t, "stored", stored[1].Content)
	// Snowflake IDs must be positive.
	assert.Greater(t, stored[0].EventID, int64(0))
	assert.Greater(t, stored[1].EventID, int64(0))
	// Timestamps must be set.
	assert.Greater(t, stored[0].CreatedAt, int64(0))
}

// TestRunner_Run_GetSessionError verifies that a GetSession failure is
// propagated as an error from Run.
func TestRunner_Run_GetSessionError(t *testing.T) {
	a := staticAgent()

	// Create a runner with a session service that has no session.
	svc := memorysession.NewMemorySessionService()
	r, err := New(a, svc)
	require.NoError(t, err)

	// memory service returns (nil, nil) when not found; nil session will panic
	// on GetEvents. Instead test with a wrapping service mock.
	errSvc := &errSessionService{err: errors.New("db unavailable")}
	r.session = errSvc

	msgs, runErr := collectRun(t, r, "session-1", model.Content{Content: "hello"})
	assert.Error(t, runErr)
	assert.Empty(t, msgs)
}

func TestRunner_Run_UsesAppUserSessionRunLockKey(t *testing.T) {
	a := staticAgent(model.Content{Role: model.RoleAssistant, Content: "ok"})
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(t.Context(), session.CreateSessionRequest{
		SessionID: "session-1",
		AppID:     "app-1",
		UserID:    "user-1",
	})
	require.NoError(t, err)

	lockingSvc := &recordingScopedLockService{SessionService: svc}
	r, err := New(a, lockingSvc)
	require.NoError(t, err)

	_, err = collectRun(t, r, "session-1", model.Content{Content: "hello"})
	require.NoError(t, err)

	require.Equal(t, []session.RunLockKey{
		{AppID: "app-1", UserID: "user-1", SessionID: "session-1"},
	}, lockingSvc.keys)
}

// TestRunner_Run_AgentError verifies that an error from the agent is
// propagated and stops iteration.
func TestRunner_Run_AgentError(t *testing.T) {
	agentErr := errors.New("agent failure")
	a := errorAgent(agentErr)
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, model.Content{Content: "hello"})
	assert.ErrorIs(t, err, agentErr)
	assert.Empty(t, msgs)

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.ListEvents(t.Context())
	require.NoError(t, err)
	assert.Empty(t, stored, "failed turn must not remain in active history")
}

func TestRunner_Run_AgentErrorRollsBackYieldedTurnEvents(t *testing.T) {
	agentErr := errors.New("tool execution failed")
	a := &mockAgent{
		name:        "event-then-error-agent",
		description: "yields a tool call and then fails",
		runFunc: func(_ context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				if !yield(&model.Event{
					Author: "event-then-error-agent",
					Content: model.Content{
						Role:      model.RoleAssistant,
						ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "lookup", Arguments: json.RawMessage(`{}`)}},
					},
				}, nil) {
					return
				}
				yield(nil, agentErr)
			}
		},
	}
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, model.Content{Content: "look up record"})

	require.ErrorIs(t, err, agentErr)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.ListEvents(t.Context())
	require.NoError(t, err)
	assert.Empty(t, stored, "assistant tool call without a result must be rolled back")
}

// TestRunner_Run_EarlyBreak verifies that a consumer breaking out of the
// iteration loop does not cause a panic and that partial results are returned
// correctly up to the break point.
func TestRunner_Run_EarlyBreak(t *testing.T) {
	msgs := []model.Content{
		{Role: model.RoleAssistant, Content: "first"},
		{Role: model.RoleAssistant, Content: "second"},
		{Role: model.RoleAssistant, Content: "third"},
	}
	a := staticAgent(msgs...)
	r, sessionID := newRunnerWithSession(t, a)

	var collected []model.Content
	for event, err := range r.Run(t.Context(), sessionID, model.Content{Content: "go"}) {
		require.NoError(t, err)
		if !event.Partial {
			collected = append(collected, event.Content)
		}
		break // stop after the first message
	}

	require.Len(t, collected, 1)
	assert.Equal(t, "first", collected[0].Content)

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.ListEvents(t.Context())
	require.NoError(t, err)
	assert.Empty(t, stored, "incomplete turn must not remain in active history")
}

// TestRunner_Run_NoAgentMessages verifies that an agent that yields nothing
// still persists the user message and returns no yielded messages.
func TestRunner_Run_NoAgentMessages(t *testing.T) {
	a := staticAgent() // yields nothing
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, model.Content{Content: "silent"})
	require.NoError(t, err)
	assert.Empty(t, msgs)

	// User message must still be persisted.
	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetEvents(t.Context(), 100, 0)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, "silent", stored[0].Content)
}

// streamingAgent returns an agent that yields the given partial content
// fragments followed by a single complete event.
func streamingAgent(partials []string, complete model.Content) *mockAgent {
	return &mockAgent{
		name:        "streaming-agent",
		description: "yields partial events then a complete event",
		runFunc: func(_ context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				for _, content := range partials {
					if !yield(&model.Event{
						Content: model.Content{Role: model.RoleAssistant, Content: content},
						Partial: true,
					}, nil) {
						return
					}
				}
				yield(&model.Event{Content: complete, Partial: false}, nil)
			}
		},
	}
}

// TestRunner_Run_PartialEventsForwarded verifies that partial streaming events
// produced by the agent are forwarded to the caller in the correct order.
func TestRunner_Run_PartialEventsForwarded(t *testing.T) {
	complete := model.Content{Role: model.RoleAssistant, Content: "Hello"}
	a := streamingAgent([]string{"He", "llo"}, complete)
	r, sessionID := newRunnerWithSession(t, a)

	var events []*model.Event
	for event, err := range r.Run(t.Context(), sessionID, model.Content{Content: "hi"}) {
		require.NoError(t, err)
		events = append(events, event)
	}

	// 2 partial chunks + 1 complete event must all be forwarded.
	require.Len(t, events, 3)
	assert.True(t, events[0].Partial)
	assert.Equal(t, "He", events[0].Content.Content)
	assert.True(t, events[1].Partial)
	assert.Equal(t, "llo", events[1].Content.Content)
	assert.False(t, events[2].Partial)
	assert.Equal(t, "Hello", events[2].Content.Content)
}

func TestRunner_Run_AssignsTurnIDToRunEvents(t *testing.T) {
	complete := model.Content{Role: model.RoleAssistant, Content: "Hello"}
	a := streamingAgent([]string{"He", "llo"}, complete)
	r, sessionID := newRunnerWithSession(t, a)

	var events []*model.Event
	for event, err := range r.Run(t.Context(), sessionID, model.Content{Content: "hi"}) {
		require.NoError(t, err)
		events = append(events, event)
	}
	require.Len(t, events, 3)

	turnID := events[0].TurnID
	require.NotEmpty(t, turnID)
	for _, event := range events {
		assert.Equal(t, sessionID, event.SessionID)
		assert.Equal(t, turnID, event.TurnID)
	}

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetEvents(t.Context(), 100, 0)
	require.NoError(t, err)
	require.Len(t, stored, 2)
	assert.Equal(t, turnID, stored[0].TurnID)
	assert.Equal(t, turnID, stored[1].TurnID)

	_, err = collectRun(t, r, sessionID, model.Content{Content: "again"})
	require.NoError(t, err)

	stored, err = sess.GetEvents(t.Context(), 100, 0)
	require.NoError(t, err)
	require.Len(t, stored, 4)
	assert.NotEqual(t, stored[0].TurnID, stored[2].TurnID)
	assert.Equal(t, stored[2].TurnID, stored[3].TurnID)
}

// TestRunner_Run_PartialEventsNotPersisted verifies that partial streaming
// events are forwarded to the caller but are NOT written to the session;
// only the complete message is persisted alongside the user event.
func TestRunner_Run_PartialEventsNotPersisted(t *testing.T) {
	complete := model.Content{Role: model.RoleAssistant, Content: "Hello"}
	a := streamingAgent([]string{"He", "llo"}, complete)
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, model.Content{Content: "stream test"})
	require.NoError(t, err)

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetEvents(t.Context(), 100, 0)
	require.NoError(t, err)

	// Only user + complete assistant must be stored — the 2 partial chunks must NOT be.
	require.Len(t, stored, 2)
	assert.Equal(t, string(model.RoleUser), stored[0].Role)
	assert.Equal(t, "stream test", stored[0].Content)
	assert.Equal(t, string(model.RoleAssistant), stored[1].Role)
	assert.Equal(t, "Hello", stored[1].Content)
}

// ---------------------------------------------------------------------------
// errSessionService – minimal SessionService that always returns an error
// from GetSession, used to test error propagation.
// ---------------------------------------------------------------------------

type errSessionService struct {
	err error
}

func (e *errSessionService) CreateSession(_ context.Context, _ session.CreateSessionRequest) (session.Session, error) {
	return nil, e.err
}
func (e *errSessionService) DeleteSession(_ context.Context, _ string) error { return e.err }
func (e *errSessionService) GetSession(_ context.Context, _ string) (session.Session, error) {
	return nil, e.err
}

// errSession satisfies session.Session for errSessionService (never actually used).
type errSession struct{}

func (s *errSession) GetSessionID() string { return "" }
func (s *errSession) GetAppID() string     { return "" }
func (s *errSession) GetUserID() string    { return "" }
func (s *errSession) CreateEvent(_ context.Context, _ *event.Event) error {
	return errors.New("errSession")
}
func (s *errSession) GetEvents(_ context.Context, _, _ int64) ([]*event.Event, error) {
	return nil, errors.New("errSession")
}
func (s *errSession) ListEvents(_ context.Context) ([]*event.Event, error) {
	return nil, errors.New("errSession")
}
func (s *errSession) DeleteEvent(_ context.Context, _ int64) error { return errors.New("errSession") }
func (s *errSession) CompactEvents(_ context.Context, _ int64, _ *event.Event) error {
	return errors.New("errSession")
}

type recordingScopedLockService struct {
	session.SessionService
	keys []session.RunLockKey
}

func (s *recordingScopedLockService) LockRun(_ context.Context, key session.RunLockKey) (func(), error) {
	s.keys = append(s.keys, key)
	return func() {}, nil
}

// TestRunner_Run_WithCompaction verifies that compaction of memory session works
// correctly: after CompactEvents, subsequent runner.Run calls receive only
// the kept messages plus the summary, not the archived messages.
func TestRunner_Run_WithCompaction(t *testing.T) {
	// Create an agent that always responds with "ok" and includes usage data.
	agentWithUsage := &mockAgent{
		name:        "agent-with-usage",
		description: "yields messages with usage",
		runFunc: func(_ context.Context, msgs []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				yield(&model.Event{
					Content: model.Content{
						Role:    model.RoleAssistant,
						Content: "ok",
					},
					Usage: &model.TokenUsage{
						PromptTokens:     100,
						CompletionTokens: 10,
						TotalTokens:      110,
					},
					Partial: false,
				}, nil)
			}
		},
	}

	const sessionID = "session-42"
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(t.Context(), session.CreateSessionRequest{SessionID: sessionID})
	require.NoError(t, err)

	r, err := New(agentWithUsage, svc)
	require.NoError(t, err)

	ctx := t.Context()

	// Run 4 turns to build up history.
	for range 4 {
		_, err := collectRun(t, r, sessionID, model.Content{Content: "turn"})
		require.NoError(t, err)
	}

	// Check message count before compaction.
	sess, err := svc.GetSession(ctx, sessionID)
	require.NoError(t, err)
	msgsBefore, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	// 4 turns × (user + assistant) = 8 messages.
	assert.Equal(t, 8, len(msgsBefore), "expected 8 messages before compaction")

	// Compact: archive first 2 rounds (4 messages), keep last 2 rounds (4 messages).
	// Find the splitEventID: the 5th message (first message of the 3rd round).
	splitEventID := msgsBefore[4].EventID
	summaryMsg := &event.Event{
		EventID:   99999,
		Role:      "system",
		Content:   "summary of rounds 1-2",
		CreatedAt: msgsBefore[0].CreatedAt,
		UpdatedAt: msgsBefore[0].UpdatedAt,
	}

	err = sess.CompactEvents(ctx, splitEventID, summaryMsg)
	require.NoError(t, err)

	// Check message count after compaction.
	msgsAfter, err := sess.ListEvents(ctx)
	require.NoError(t, err)
	// kept (4) + summary (1) = 5 messages.
	assert.Equal(t, 5, len(msgsAfter), "expected 5 messages after compaction")

	// Run one more turn — agent should receive only the kept + summary messages.
	_, err = collectRun(t, r, sessionID, model.Content{Content: "after compaction"})
	require.NoError(t, err)

	// Agent's captured messages should be: 5 (kept + summary) + 1 (new user) = 6.
	assert.Equal(t, 6, len(agentWithUsage.capturedMessages),
		"agent should receive kept messages + summary + new user input")
}

// TestRunner_Run_SessionNotFound verifies that Run returns a descriptive error
// when the requested session does not exist, rather than panic-ing on nil.
func TestRunner_Run_SessionNotFound(t *testing.T) {
	a := staticAgent(model.Content{Role: model.RoleAssistant, Content: "ok"})
	// Use a session service with no sessions created — GetSession returns nil, nil.
	svc := memorysession.NewMemorySessionService()
	r, err := New(a, svc)
	require.NoError(t, err)

	msgs, runErr := collectRun(t, r, "missing", model.Content{Content: "hello"})
	assert.Error(t, runErr)
	assert.ErrorIs(t, runErr, ErrSessionNotFound)
	var notFoundErr *SessionNotFoundError
	require.True(t, errors.As(runErr, &notFoundErr))
	assert.Equal(t, "missing", notFoundErr.SessionID)
	assert.Contains(t, runErr.Error(), "not found")
	assert.Empty(t, msgs)
}

func TestRunner_Run_SameSessionSerializesTurns(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	a := &mockAgent{
		name:        "blocking-agent",
		description: "blocks the first run",
		runFunc: func(_ context.Context, _ []model.Event) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				if calls.Add(1) == 1 {
					close(started)
				}
				<-release
				yield(&model.Event{
					Content: model.Content{Role: model.RoleAssistant, Content: "done"},
				}, nil)
			}
		},
	}

	const sessionID = "session-1"
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(t.Context(), session.CreateSessionRequest{SessionID: sessionID})
	require.NoError(t, err)
	r, err := New(a, svc)
	require.NoError(t, err)

	drain := func(ctx context.Context, input string) error {
		for _, err := range r.Run(ctx, sessionID, model.Content{Content: input}) {
			if err != nil {
				return err
			}
		}
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- drain(t.Context(), "first")
	}()
	<-started

	secondCtx, cancelSecond := context.WithCancel(t.Context())
	cancelSecond()
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- drain(secondCtx, "second")
	}()

	close(release)
	require.NoError(t, <-firstDone)
	assert.ErrorIs(t, <-secondDone, context.Canceled)
	assert.Equal(t, int32(1), calls.Load())

	sess, err := svc.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	messages, err := sess.ListEvents(t.Context())
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, "first", messages[0].Content)
	assert.Equal(t, "done", messages[1].Content)
}

// TestRunner_Run_MultimodalInput verifies that a user message carrying ContentParts
// (rather than plain text) is persisted to the session and forwarded to the agent
// with its Parts intact.
func TestRunner_Run_MultimodalInput(t *testing.T) {
	a := staticAgent(model.Content{Role: model.RoleAssistant, Content: "ok"})
	r, sessionID := newRunnerWithSession(t, a)

	userMsg := model.Content{
		Parts: []model.ContentPart{
			{Type: model.ContentPartTypeText, Text: "describe this"},
			{
				Type:        model.ContentPartTypeImageURL,
				ImageURL:    "https://example.com/img.png",
				ImageDetail: model.ImageDetailHigh,
			},
		},
	}

	_, err := collectRun(t, r, sessionID, userMsg)
	require.NoError(t, err)

	// The agent must receive the Parts on the user event.
	require.Len(t, a.capturedMessages, 1)
	assert.Equal(t, model.RoleUser, a.capturedMessages[0].Content.Role)
	require.Len(t, a.capturedMessages[0].Content.Parts, 2)
	assert.Equal(t, model.ContentPartTypeText, a.capturedMessages[0].Content.Parts[0].Type)
	assert.Equal(t, "describe this", a.capturedMessages[0].Content.Parts[0].Text)
	assert.Equal(t, model.ContentPartTypeImageURL, a.capturedMessages[0].Content.Parts[1].Type)
	assert.Equal(t, "https://example.com/img.png", a.capturedMessages[0].Content.Parts[1].ImageURL)

	// Parts must survive persistence and be restored on the next turn.
	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetEvents(t.Context(), 100, 0)
	require.NoError(t, err)
	require.Len(t, stored, 2) // user + assistant
	assert.Equal(t, string(model.RoleUser), stored[0].Role)
	require.Len(t, stored[0].Parts, 2)
	assert.Equal(t, model.ContentPartTypeText, model.ContentPartType(stored[0].Parts[0].Type))
	assert.Equal(t, "describe this", stored[0].Parts[0].Text)

	// Second turn: agent should receive the persisted multimodal message as history.
	a2 := staticAgent(model.Content{Role: model.RoleAssistant, Content: "done"})
	r.agent = a2
	_, err = collectRun(t, r, sessionID, model.Content{Content: "follow-up"})
	require.NoError(t, err)

	// History should contain the original user multimodal message with Parts restored.
	var multimodalMsg *model.Content
	for i := range a2.capturedMessages {
		if a2.capturedMessages[i].Content.Role == model.RoleUser && len(a2.capturedMessages[i].Content.Parts) > 0 {
			m := a2.capturedMessages[i].Content
			multimodalMsg = &m
			break
		}
	}
	require.NotNil(t, multimodalMsg, "multimodal user message not found in history")
	assert.Equal(t, "describe this", multimodalMsg.Parts[0].Text)
}
