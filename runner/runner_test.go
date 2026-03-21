package runner

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
	memorysession "github.com/soasurs/adk/session/memory"
	"github.com/soasurs/adk/session/message"
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
func collectRun(t *testing.T, r *Runner, sessionID int64, input model.Message) ([]model.Message, error) {
	t.Helper()
	var msgs []model.Message
	for event, err := range r.Run(t.Context(), sessionID, input) {
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
	_, err := svc.CreateSession(t.Context(), sessionID)
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

	msgs, err := collectRun(t, r, sessionID, model.Message{Content: "ping"})

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

	msgs, err := collectRun(t, r, sessionID, model.Message{Content: "echo hi"})

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

	_, err := collectRun(t, r, sessionID, model.Message{Content: "hello"})
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
	_, err := collectRun(t, r, sessionID, model.Message{Content: "turn 1"})
	require.NoError(t, err)

	// Second turn.
	_, err = collectRun(t, r, sessionID, model.Message{Content: "turn 2"})
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

	_, err := collectRun(t, r, sessionID, model.Message{Content: "persist me"})
	require.NoError(t, err)

	// Retrieve raw session to inspect stored messages.
	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, sess)

	stored, err := sess.GetMessages(t.Context(), 100, 0)
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

	msgs, runErr := collectRun(t, r, 1, model.Message{Content: "hello"})
	assert.Error(t, runErr)
	assert.Empty(t, msgs)
}

// TestRunner_Run_AgentError verifies that an error from the agent is
// propagated and stops iteration.
func TestRunner_Run_AgentError(t *testing.T) {
	agentErr := errors.New("agent failure")
	a := errorAgent(agentErr)
	r, sessionID := newRunnerWithSession(t, a)

	msgs, err := collectRun(t, r, sessionID, model.Message{Content: "hello"})
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
	for event, err := range r.Run(t.Context(), sessionID, model.Message{Content: "go"}) {
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

	msgs, err := collectRun(t, r, sessionID, model.Message{Content: "silent"})
	require.NoError(t, err)
	assert.Empty(t, msgs)

	// User message must still be persisted.
	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetMessages(t.Context(), 100, 0)
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
	for event, err := range r.Run(t.Context(), sessionID, model.Message{Content: "hi"}) {
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

	_, err := collectRun(t, r, sessionID, model.Message{Content: "stream test"})
	require.NoError(t, err)

	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetMessages(t.Context(), 100, 0)
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
func (s *errSession) DeleteMessage(_ context.Context, _ int64) error { return errors.New("errSession") }
func (s *errSession) CompactMessages(_ context.Context, _ int64, _ *message.Message) error {
	return errors.New("errSession")
}

// TestRunner_Run_WithCompaction verifies that compaction of memory session works
// correctly: after CompactMessages, subsequent runner.Run calls receive only
// the kept messages plus the summary, not the archived messages.
func TestRunner_Run_WithCompaction(t *testing.T) {
	// Create an agent that always responds with "ok" and includes usage data.
	agentWithUsage := &mockAgent{
		name:        "agent-with-usage",
		description: "yields messages with usage",
		runFunc: func(_ context.Context, msgs []model.Message) iter.Seq2[*model.Event, error] {
			return func(yield func(*model.Event, error) bool) {
				yield(&model.Event{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "ok",
						Usage: &model.TokenUsage{
							PromptTokens:     100,
							CompletionTokens: 10,
							TotalTokens:      110,
						},
					},
					Partial: false,
				}, nil)
			}
		},
	}

	const sessionID = int64(42)
	svc := memorysession.NewMemorySessionService()
	_, err := svc.CreateSession(t.Context(), sessionID)
	require.NoError(t, err)

	r, err := New(agentWithUsage, svc)
	require.NoError(t, err)

	ctx := t.Context()

	// Run 4 turns to build up history.
	for i := 0; i < 4; i++ {
		_, err := collectRun(t, r, sessionID, model.Message{Content: "turn"})
		require.NoError(t, err)
	}

	// Check message count before compaction.
	sess, err := svc.GetSession(ctx, sessionID)
	require.NoError(t, err)
	msgsBefore, err := sess.ListMessages(ctx)
	require.NoError(t, err)
	// 4 turns × (user + assistant) = 8 messages.
	assert.Equal(t, 8, len(msgsBefore), "expected 8 messages before compaction")

	// Compact: archive first 2 rounds (4 messages), keep last 2 rounds (4 messages).
	// Find the splitMessageID: the 5th message (first message of the 3rd round).
	splitMessageID := msgsBefore[4].MessageID
	summaryMsg := &message.Message{
		MessageID: 99999,
		Role:      "system",
		Content:   "summary of rounds 1-2",
		CreatedAt: msgsBefore[0].CreatedAt,
		UpdatedAt: msgsBefore[0].UpdatedAt,
	}

	err = sess.CompactMessages(ctx, splitMessageID, summaryMsg)
	require.NoError(t, err)

	// Check message count after compaction.
	msgsAfter, err := sess.ListMessages(ctx)
	require.NoError(t, err)
	// kept (4) + summary (1) = 5 messages.
	assert.Equal(t, 5, len(msgsAfter), "expected 5 messages after compaction")

	// Run one more turn — agent should receive only the kept + summary messages.
	_, err = collectRun(t, r, sessionID, model.Message{Content: "after compaction"})
	require.NoError(t, err)

	// Agent's captured messages should be: 5 (kept + summary) + 1 (new user) = 6.
	assert.Equal(t, 6, len(agentWithUsage.capturedMessages),
		"agent should receive kept messages + summary + new user input")
}

// TestRunner_Run_SessionNotFound verifies that Run returns a descriptive error
// when the requested session does not exist, rather than panic-ing on nil.
func TestRunner_Run_SessionNotFound(t *testing.T) {
	a := staticAgent(model.Message{Role: model.RoleAssistant, Content: "ok"})
	// Use a session service with no sessions created — GetSession returns nil, nil.
	svc := memorysession.NewMemorySessionService()
	r, err := New(a, svc)
	require.NoError(t, err)

	msgs, runErr := collectRun(t, r, 999, model.Message{Content: "hello"})
	assert.Error(t, runErr)
	assert.ErrorIs(t, runErr, ErrSessionNotFound)
	var notFoundErr *SessionNotFoundError
	require.True(t, errors.As(runErr, &notFoundErr))
	assert.Equal(t, int64(999), notFoundErr.SessionID)
	assert.Contains(t, runErr.Error(), "not found")
	assert.Empty(t, msgs)
}

// TestRunner_Run_MultimodalInput verifies that a user message carrying ContentParts
// (rather than plain text) is persisted to the session and forwarded to the agent
// with its Parts intact.
func TestRunner_Run_MultimodalInput(t *testing.T) {
	a := staticAgent(model.Message{Role: model.RoleAssistant, Content: "ok"})
	r, sessionID := newRunnerWithSession(t, a)

	userMsg := model.Message{
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

	// The agent must receive the Parts on the user message.
	require.Len(t, a.capturedMessages, 1)
	assert.Equal(t, model.RoleUser, a.capturedMessages[0].Role)
	require.Len(t, a.capturedMessages[0].Parts, 2)
	assert.Equal(t, model.ContentPartTypeText, a.capturedMessages[0].Parts[0].Type)
	assert.Equal(t, "describe this", a.capturedMessages[0].Parts[0].Text)
	assert.Equal(t, model.ContentPartTypeImageURL, a.capturedMessages[0].Parts[1].Type)
	assert.Equal(t, "https://example.com/img.png", a.capturedMessages[0].Parts[1].ImageURL)

	// Parts must survive persistence and be restored on the next turn.
	sess, err := r.session.GetSession(t.Context(), sessionID)
	require.NoError(t, err)
	stored, err := sess.GetMessages(t.Context(), 100, 0)
	require.NoError(t, err)
	require.Len(t, stored, 2) // user + assistant
	assert.Equal(t, string(model.RoleUser), stored[0].Role)
	require.Len(t, stored[0].Parts, 2)
	assert.Equal(t, model.ContentPartTypeText, model.ContentPartType(stored[0].Parts[0].Type))
	assert.Equal(t, "describe this", stored[0].Parts[0].Text)

	// Second turn: agent should receive the persisted multimodal message as history.
	a2 := staticAgent(model.Message{Role: model.RoleAssistant, Content: "done"})
	r.agent = a2
	_, err = collectRun(t, r, sessionID, model.Message{Content: "follow-up"})
	require.NoError(t, err)

	// History should contain the original user multimodal message with Parts restored.
	var multimodalMsg *model.Message
	for i := range a2.capturedMessages {
		if a2.capturedMessages[i].Role == model.RoleUser && len(a2.capturedMessages[i].Parts) > 0 {
			m := a2.capturedMessages[i]
			multimodalMsg = &m
			break
		}
	}
	require.NotNil(t, multimodalMsg, "multimodal user message not found in history")
	assert.Equal(t, "describe this", multimodalMsg.Parts[0].Text)
}
