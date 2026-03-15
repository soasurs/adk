package runner

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/session"
	memorysession "soasurs.dev/soasurs/adk/session/memory"
	"soasurs.dev/soasurs/adk/session/message"
)

// ---------------------------------------------------------------------------
// Mock Agent
// ---------------------------------------------------------------------------

// mockAgent is a test double for agent.Agent.
type mockAgent struct {
	name        string
	description string
	// runFunc is called by Run to produce the event sequence.
	runFunc func(ctx context.Context, messages []model.Message) iter.Seq2[*model.Event, error]
	// capturedMessages records the messages argument passed to the last Run call.
	capturedMessages []model.Message
}

func (m *mockAgent) Name() string        { return m.name }
func (m *mockAgent) Description() string { return m.description }
func (m *mockAgent) Run(ctx context.Context, messages []model.Message) iter.Seq2[*model.Event, error] {
	m.capturedMessages = messages
	return m.runFunc(ctx, messages)
}

// staticAgent returns a fixed slice of complete (non-partial) events.
func staticAgent(msgs ...model.Message) *mockAgent {
	return &mockAgent{
		name:        "static-agent",
		description: "yields fixed messages",
		runFunc: func(_ context.Context, _ []model.Message) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				for _, m := range msgs {
					if !yield(&model.Event{Message: m, Partial: false}, nil) {
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
		runFunc: func(_ context.Context, _ []model.Message) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				yield(nil, err)
			}
		},
	}
}

// collectRun drains all complete events from runner.Run into a message slice.
func collectRun(t *testing.T, r *Runner, sessionID int64, input string) ([]model.Message, error) {
	t.Helper()
	var msgs []model.Message
	for event, err := range r.Run(context.Background(), sessionID, input) {
		if err != nil {
			return msgs, err
		}
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}
	return msgs, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newRunnerWithSession creates a Runner backed by an in-memory session service
// and pre-creates a session for sessionID 1.
func newRunnerWithSession(t *testing.T, a *mockAgent) (*Runner, int64) {
	t.Helper()
	const sessionID = int64(1)
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(context.Background(), sessionID)
	require.NoError(t, err)

	r, err := New(a, svc)
	require.NoError(t, err)
	return r, sessionID
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunner_Run_Basic verifies that a single user turn is forwarded to the
// agent and that the resulting assistant message is both yielded and persisted.
func TestRunner_Run_Basic(t *testing.T) {
	reply := model.Message{Role: model.RoleAssistant, Content: "pong"}
	a := staticAgent(reply)

	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, "ping")

	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, "pong", msgs[0].Content)
}

// TestRunner_Run_MultipleAgentMessages verifies that all messages yielded by
// the agent are forwarded to the caller.
func TestRunner_Run_MultipleAgentMessages(t *testing.T) {
	tool := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: "tc1", Name: "echo", Arguments: `{"text":"hi"}`}},
	}
	toolResult := model.Message{Role: model.RoleTool, Content: "hi", ToolCallID: "tc1"}
	final := model.Message{Role: model.RoleAssistant, Content: "Done."}

	a := staticAgent(tool, toolResult, final)
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, "echo hi")

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
	a := staticAgent(model.Message{Role: model.RoleAssistant, Content: "ok"})
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, "hello")
	require.NoError(t, err)

	// The agent must have received exactly the user message.
	require.Len(t, a.capturedMessages, 1)
	assert.Equal(t, model.RoleUser, a.capturedMessages[0].Role)
	assert.Equal(t, "hello", a.capturedMessages[0].Content)
}

// TestRunner_Run_HistoryPassedToAgent verifies that messages already stored in
// the session are prepended to the agent's input on the next turn.
func TestRunner_Run_HistoryPassedToAgent(t *testing.T) {
	a := staticAgent(model.Message{Role: model.RoleAssistant, Content: "ok"})
	r, sessionID := newRunnerWithSession(t, a)

	// First turn – produces one user + one assistant message in session.
	_, err := collectRun(t, r, sessionID, "turn 1")
	require.NoError(t, err)

	// Second turn.
	_, err = collectRun(t, r, sessionID, "turn 2")
	require.NoError(t, err)

	// Agent should have received: user(turn1) + assistant(ok) + user(turn2).
	require.Len(t, a.capturedMessages, 3)
	assert.Equal(t, model.RoleUser, a.capturedMessages[0].Role)
	assert.Equal(t, "turn 1", a.capturedMessages[0].Content)
	assert.Equal(t, model.RoleAssistant, a.capturedMessages[1].Role)
	assert.Equal(t, model.RoleUser, a.capturedMessages[2].Role)
	assert.Equal(t, "turn 2", a.capturedMessages[2].Content)
}

// TestRunner_Run_MessagesPersistedToSession verifies that the user message and
// all agent replies are stored in the session so subsequent turns see them.
func TestRunner_Run_MessagesPersistedToSession(t *testing.T) {
	reply := model.Message{Role: model.RoleAssistant, Content: "stored"}
	a := staticAgent(reply)
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, "persist me")
	require.NoError(t, err)

	// Retrieve raw session to inspect stored messages.
	sess, err := r.session.GetSession(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, sess)

	stored, err := sess.GetMessages(context.Background(), 100, 0)
	require.NoError(t, err)
	// Expect: user + assistant.
	require.Len(t, stored, 2)
	assert.Equal(t, string(model.RoleUser), stored[0].Role)
	assert.Equal(t, "persist me", stored[0].Content)
	assert.Equal(t, string(model.RoleAssistant), stored[1].Role)
	assert.Equal(t, "stored", stored[1].Content)
	// Snowflake IDs must be positive.
	assert.Greater(t, stored[0].MessageID, int64(0))
	assert.Greater(t, stored[1].MessageID, int64(0))
	// Timestamps must be set.
	assert.Greater(t, stored[0].CreatedAt, int64(0))
}

// TestRunner_Run_GetSessionError verifies that a GetSession failure is
// propagated as an error from Run.
func TestRunner_Run_GetSessionError(t *testing.T) {
	a := staticAgent()

	// Create a runner with a session service that has no session for ID 999.
	svc := memorysession.NewMemorySessionService()
	r, err := New(a, svc)
	require.NoError(t, err)

	// memory service returns (nil, nil) when not found; nil session will panic
	// on GetMessages. Instead test with a wrapping service mock.
	errSvc := &errSessionService{err: errors.New("db unavailable")}
	r.session = errSvc

	msgs, runErr := collectRun(t, r, 1, "hello")
	assert.Error(t, runErr)
	assert.Empty(t, msgs)
}

// TestRunner_Run_AgentError verifies that an error from the agent is
// propagated and stops iteration.
func TestRunner_Run_AgentError(t *testing.T) {
	agentErr := errors.New("agent failure")
	a := errorAgent(agentErr)
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, "hello")
	assert.ErrorIs(t, err, agentErr)
	assert.Empty(t, msgs)
}

// TestRunner_Run_EarlyBreak verifies that a consumer breaking out of the
// iteration loop does not cause a panic and that partial results are returned
// correctly up to the break point.
func TestRunner_Run_EarlyBreak(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleAssistant, Content: "first"},
		{Role: model.RoleAssistant, Content: "second"},
		{Role: model.RoleAssistant, Content: "third"},
	}
	a := staticAgent(msgs...)
	r, sessionID := newRunnerWithSession(t, a)

	var collected []model.Message
	for event, err := range r.Run(context.Background(), sessionID, "go") {
		require.NoError(t, err)
		if !event.Partial {
			collected = append(collected, event.Message)
		}
		break // stop after the first message
	}

	require.Len(t, collected, 1)
	assert.Equal(t, "first", collected[0].Content)
}

// TestRunner_Run_NoAgentMessages verifies that an agent that yields nothing
// still persists the user message and returns no yielded messages.
func TestRunner_Run_NoAgentMessages(t *testing.T) {
	a := staticAgent() // yields nothing
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, "silent")
	require.NoError(t, err)
	assert.Empty(t, msgs)

	// User message must still be persisted.
	sess, err := r.session.GetSession(context.Background(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetMessages(context.Background(), 100, 0)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, "silent", stored[0].Content)
}

// streamingAgent returns an agent that yields the given partial content
// fragments followed by a single complete message.
func streamingAgent(partials []string, complete model.Message) *mockAgent {
	return &mockAgent{
		name:        "streaming-agent",
		description: "yields partial events then a complete event",
		runFunc: func(_ context.Context, _ []model.Message) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				for _, content := range partials {
					if !yield(&model.Event{
						Message: model.Message{Role: model.RoleAssistant, Content: content},
						Partial: true,
					}, nil) {
						return
					}
				}
				yield(&model.Event{Message: complete, Partial: false}, nil)
			}
		},
	}
}

// TestRunner_Run_PartialEventsForwarded verifies that partial streaming events
// produced by the agent are forwarded to the caller in the correct order.
func TestRunner_Run_PartialEventsForwarded(t *testing.T) {
	complete := model.Message{Role: model.RoleAssistant, Content: "Hello"}
	a := streamingAgent([]string{"He", "llo"}, complete)
	r, sessionID := newRunnerWithSession(t, a)

	var events []*model.Event
	for event, err := range r.Run(context.Background(), sessionID, "hi") {
		require.NoError(t, err)
		events = append(events, event)
	}

	// 2 partial chunks + 1 complete event must all be forwarded.
	require.Len(t, events, 3)
	assert.True(t, events[0].Partial)
	assert.Equal(t, "He", events[0].Message.Content)
	assert.True(t, events[1].Partial)
	assert.Equal(t, "llo", events[1].Message.Content)
	assert.False(t, events[2].Partial)
	assert.Equal(t, "Hello", events[2].Message.Content)
}

// TestRunner_Run_PartialEventsNotPersisted verifies that partial streaming
// events are forwarded to the caller but are NOT written to the session;
// only the complete message is persisted alongside the user message.
func TestRunner_Run_PartialEventsNotPersisted(t *testing.T) {
	complete := model.Message{Role: model.RoleAssistant, Content: "Hello"}
	a := streamingAgent([]string{"He", "llo"}, complete)
	r, sessionID := newRunnerWithSession(t, a)

	_, err := collectRun(t, r, sessionID, "stream test")
	require.NoError(t, err)

	sess, err := r.session.GetSession(context.Background(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetMessages(context.Background(), 100, 0)
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

func (e *errSessionService) CreateSession(_ context.Context, _ int64) (session.Session, error) {
	return nil, e.err
}
func (e *errSessionService) DeleteSession(_ context.Context, _ int64) error { return e.err }
func (e *errSessionService) GetSession(_ context.Context, _ int64) (session.Session, error) {
	return nil, e.err
}

// errSession satisfies session.Session for errSessionService (never actually used).
type errSession struct{}

func (s *errSession) GetSessionID() int64 { return 0 }
func (s *errSession) CreateMessage(_ context.Context, _ *message.Message) error {
	return errors.New("errSession")
}
func (s *errSession) GetMessages(_ context.Context, _, _ int64) ([]*message.Message, error) {
	return nil, errors.New("errSession")
}
func (s *errSession) ListMessages(_ context.Context) ([]*message.Message, error) {
	return nil, errors.New("errSession")
}
func (s *errSession) ListCompactedMessages(_ context.Context) ([]*message.Message, error) {
	return nil, errors.New("errSession")
}
func (s *errSession) DeleteMessage(_ context.Context, _ int64) error { return errors.New("errSession") }
func (s *errSession) CompactMessages(_ context.Context, _ func(context.Context, []*message.Message) (*message.Message, error)) error {
	return errors.New("errSession")
}
