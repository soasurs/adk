package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/openai"
	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/builtin"
	adktrace "github.com/soasurs/adk/trace"
)

// newLLMFromEnv creates a model.LLM from environment variables.
// Required: OPENAI_API_KEY — test is skipped when absent.
// Optional: OPENAI_BASE_URL — overrides the default OpenAI endpoint.
// Optional: OPENAI_MODEL   — model name; defaults to "gpt-4o-mini" when absent.
func newLLMFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}
	return openai.New(apiKey, baseURL, modelName)
}

// newReasoningLLMFromEnv creates a model.LLM intended for reasoning tests.
// Required: OPENAI_API_KEY and OPENAI_REASONING_MODEL — test is skipped when either is absent.
// Optional: OPENAI_BASE_URL — overrides the default OpenAI endpoint (e.g. DeepSeek base URL).
func newReasoningLLMFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	modelName := os.Getenv("OPENAI_REASONING_MODEL")
	if modelName == "" {
		t.Skip("OPENAI_REASONING_MODEL not set")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	return openai.New(apiKey, baseURL, modelName)
}

// ---------------------------------------------------------------------------
// Mock LLM for unit tests
// ---------------------------------------------------------------------------

// mockLLM is a deterministic model.LLM implementation that replays a fixed
// sequence of responses, enabling unit tests without a real API.
type mockLLM struct {
	name      string
	responses []*model.LLMResponse
	callIdx   int
}

func (m *mockLLM) Name() string { return m.name }

func (m *mockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.callIdx >= len(m.responses) {
			yield(nil, fmt.Errorf("mockLLM: no more responses (call %d)", m.callIdx))
			return
		}
		resp := m.responses[m.callIdx]
		m.callIdx++
		yield(resp, nil)
	}
}

// streamingMockLLM is a test double that yields multiple *model.LLMResponse
// values per GenerateContent call, simulating LLM streaming behaviour.
// calls[i] is the ordered sequence of responses yielded on the (i+1)-th call.
type streamingMockLLM struct {
	name    string
	calls   [][]*model.LLMResponse
	callIdx int
}

func (m *streamingMockLLM) Name() string { return m.name }

func (m *streamingMockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.callIdx >= len(m.calls) {
			yield(nil, fmt.Errorf("streamingMockLLM: no more responses (call %d)", m.callIdx))
			return
		}
		resps := m.calls[m.callIdx]
		m.callIdx++
		for _, resp := range resps {
			if !yield(resp, nil) {
				return
			}
		}
	}
}

// logMessage prints a single message in a concise one-line format.
func logMessage(t *testing.T, idx int, m model.Content) {
	t.Helper()
	if len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			t.Logf("  [%d] %-9s tool_call  name=%s args=%s", idx, m.Role, tc.Name, tc.Arguments)
		}
		return
	}
	if m.ToolCallID != "" {
		t.Logf("  [%d] %-9s result     id=%s content=%s", idx, m.Role, m.ToolCallID, m.Content)
		return
	}
	if m.ReasoningContent != "" {
		t.Logf("  [%d] %-9s reasoning  %s", idx, m.Role, m.ReasoningContent)
	}
	t.Logf("  [%d] %-9s %s", idx, m.Role, m.Content)
}

// collectMessages drains the agent Run iterator, logs every complete yielded
// message, and returns all complete messages plus the first error (if any).
// Partial streaming events are consumed silently.
func collectMessages(t *testing.T, agent *LlmAgent, messages []model.Content) ([]model.Content, error) {
	t.Helper()
	return collectMessagesWithContext(t, t.Context(), agent, messages)
}

func collectMessagesWithContext(
	t *testing.T,
	ctx context.Context,
	agent *LlmAgent,
	messages []model.Content,
) ([]model.Content, error) {
	t.Helper()
	t.Log("  --- input ---")
	for i, m := range messages {
		logMessage(t, i, m)
	}
	t.Log("  --- output ---")
	var collected []model.Content
	for event, err := range agent.Run(ctx, model.EventsFromContents(messages)) {
		if err != nil {
			return collected, err
		}
		if event.Partial {
			continue
		}
		logMessage(t, len(collected), event.Content)
		collected = append(collected, event.Content)
	}
	return collected, nil
}

type recordedTraceSpan struct {
	kind  adktrace.Kind
	event adktrace.Event
}

type recordingTraceTracer struct {
	mu     sync.Mutex
	starts []adktrace.Event
	ends   []recordedTraceSpan
}

func (t *recordingTraceTracer) Start(ctx context.Context, event adktrace.Event) (context.Context, adktrace.Span) {
	t.mu.Lock()
	t.starts = append(t.starts, event)
	t.mu.Unlock()
	return ctx, &recordingTraceSpan{tracer: t, kind: event.Kind}
}

type recordingTraceSpan struct {
	tracer *recordingTraceTracer
	kind   adktrace.Kind
}

func (s *recordingTraceSpan) AddEvent(context.Context, adktrace.Event) {}

func (s *recordingTraceSpan) End(_ context.Context, event adktrace.Event) {
	s.tracer.mu.Lock()
	defer s.tracer.mu.Unlock()
	s.tracer.ends = append(s.tracer.ends, recordedTraceSpan{kind: s.kind, event: event})
}

func traceStarts(tracer *recordingTraceTracer) []adktrace.Event {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	starts := make([]adktrace.Event, len(tracer.starts))
	copy(starts, tracer.starts)
	return starts
}

func traceEndByKind(tracer *recordingTraceTracer, kind adktrace.Kind) (adktrace.Event, bool) {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	for _, span := range tracer.ends {
		if span.kind == kind {
			return span.event, true
		}
	}
	return adktrace.Event{}, false
}

// ---------------------------------------------------------------------------
// Unit tests (no network required)
// ---------------------------------------------------------------------------

func TestNewWithError_InvalidConfig(t *testing.T) {
	echoTool, err := builtin.NewEchoTool()
	require.NoError(t, err)

	tests := []struct {
		name   string
		config Config
	}{
		{
			name:   "nil model",
			config: Config{},
		},
		{
			name: "negative max iterations",
			config: Config{
				Model:         &mockLLM{name: "mock"},
				MaxIterations: -1,
			},
		},
		{
			name: "negative tool timeout",
			config: Config{
				Model:       &mockLLM{name: "mock"},
				ToolTimeout: -time.Second,
			},
		},
		{
			name: "nil tool",
			config: Config{
				Model: &mockLLM{name: "mock"},
				Tools: []tool.Tool{nil},
			},
		},
		{
			name: "duplicate tool name",
			config: Config{
				Model: &mockLLM{name: "mock"},
				Tools: []tool.Tool{echoTool, echoTool},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewWithError(tc.config)
			assert.Nil(t, a)
			assert.ErrorIs(t, err, ErrInvalidConfig)
			var configErr *ConfigError
			assert.ErrorAs(t, err, &configErr)
		})
	}
}

func TestNew_InvalidConfigPanics(t *testing.T) {
	assert.Panics(t, func() {
		New(Config{})
	})
}

func TestLlmAgent_Tracing_LLMCallLifecycle(t *testing.T) {
	tracer := new(recordingTraceTracer)
	mock := &mockLLM{
		name: "mock-trace",
		responses: []*model.LLMResponse{
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "pong"},
				FinishReason: model.FinishReasonStop,
				Usage: &model.TokenUsage{
					PromptTokens:     3,
					CompletionTokens: 4,
					TotalTokens:      7,
				},
			},
		},
	}
	a := New(Config{Name: "trace-agent", Model: mock}).(*LlmAgent)
	ctx := adktrace.ContextWithTracer(t.Context(), tracer)

	msgs, err := collectMessagesWithContext(t, ctx, a, []model.Content{
		{Role: model.RoleUser, Content: "ping"},
	})

	require.NoError(t, err)
	require.Len(t, msgs, 1)
	starts := traceStarts(tracer)
	assert.Contains(t, starts, adktrace.Event{
		Kind:      adktrace.KindLLMIteration,
		AgentName: "trace-agent",
		Model:     "mock-trace",
		Iteration: 1,
	})
	assert.Contains(t, starts, adktrace.Event{
		Kind:      adktrace.KindLLMCall,
		AgentName: "trace-agent",
		Model:     "mock-trace",
		Iteration: 1,
	})

	llmEnd, ok := traceEndByKind(tracer, adktrace.KindLLMCall)
	require.True(t, ok)
	assert.Equal(t, model.FinishReasonStop, llmEnd.FinishReason)
	assert.Equal(t, int64(3), llmEnd.PromptTokens)
	assert.Equal(t, int64(4), llmEnd.CompletionTokens)
	assert.Equal(t, int64(7), llmEnd.TotalTokens)
	assert.NoError(t, llmEnd.Err)
}

func TestLlmAgent_Tracing_StreamingPartialCount(t *testing.T) {
	tracer := new(recordingTraceTracer)
	mock := &streamingMockLLM{
		name: "mock-stream-trace",
		calls: [][]*model.LLMResponse{
			{
				{Content: model.Content{Role: model.RoleAssistant, Content: "po"}, Partial: true},
				{Content: model.Content{Role: model.RoleAssistant, Content: "ng"}, Partial: true},
				{
					Content:      model.Content{Role: model.RoleAssistant, Content: "pong"},
					FinishReason: model.FinishReasonStop,
				},
			},
		},
	}
	a := New(Config{Name: "stream-trace-agent", Model: mock, Stream: true}).(*LlmAgent)
	ctx := adktrace.ContextWithTracer(t.Context(), tracer)

	_, err := collectMessagesWithContext(t, ctx, a, []model.Content{
		{Role: model.RoleUser, Content: "ping"},
	})

	require.NoError(t, err)
	llmEnd, ok := traceEndByKind(tracer, adktrace.KindLLMCall)
	require.True(t, ok)
	assert.Equal(t, 2, llmEnd.PartialResponses)
	assert.True(t, llmEnd.Stream)
}

func TestLlmAgent_Tracing_ToolCallSpan(t *testing.T) {
	tracer := new(recordingTraceTracer)
	echoTool, err := builtin.NewEchoTool()
	require.NoError(t, err)
	mock := &mockLLM{
		name: "mock-tool-trace",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "tc-1", Name: "Echo", Arguments: json.RawMessage(`{"echo":"hello"}`)},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	a := New(Config{
		Name:  "tool-trace-agent",
		Model: mock,
		Tools: []tool.Tool{echoTool},
	}).(*LlmAgent)
	ctx := adktrace.ContextWithTracer(t.Context(), tracer)

	_, err = collectMessagesWithContext(t, ctx, a, []model.Content{
		{Role: model.RoleUser, Content: "run echo"},
	})

	require.NoError(t, err)
	var toolStart *adktrace.Event
	for _, event := range traceStarts(tracer) {
		if event.Kind == adktrace.KindToolCall {
			toolStart = &event
			break
		}
	}
	require.NotNil(t, toolStart)
	assert.Equal(t, "Echo", toolStart.ToolName)
	assert.Equal(t, "tc-1", toolStart.ToolCallID)
	assert.Equal(t, 0, toolStart.ToolIndex)

	toolEnd, ok := traceEndByKind(tracer, adktrace.KindToolCall)
	require.True(t, ok)
	assert.Equal(t, model.RoleTool, toolEnd.EventRole)
	assert.False(t, toolEnd.IsError)
	assert.NoError(t, toolEnd.Err)
}

// TestLlmAgent_MaxIterations verifies that Run yields an error once the
// MaxIterations limit is reached instead of looping forever.
func TestLlmAgent_MaxIterations(t *testing.T) {
	// Each call returns a tool-call response so the loop never stops on its own.
	toolCallResp := func() *model.LLMResponse {
		return &model.LLMResponse{
			Content: model.Content{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"message":"hi"}`)},
				},
			},
			FinishReason: model.FinishReasonToolCalls,
		}
	}

	const limit = 3
	llm := &mockLLM{
		name: "mock",
		responses: []*model.LLMResponse{
			toolCallResp(),
			toolCallResp(),
			toolCallResp(),
			toolCallResp(), // would be reached without a limit
		},
	}

	echoTool, err := builtin.NewEchoTool()
	require.NoError(t, err)

	a := New(Config{
		Name:          "test-agent",
		Model:         llm,
		Tools:         []tool.Tool{echoTool},
		MaxIterations: limit,
	}).(*LlmAgent)

	_, runErr := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "loop forever"},
	})

	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, ErrMaxIterationsExceeded)
	var maxErr *MaxIterationsError
	require.True(t, errors.As(runErr, &maxErr))
	assert.Equal(t, limit, maxErr.MaxIterations)
	assert.Contains(t, runErr.Error(), "max iterations exceeded")
	assert.Contains(t, runErr.Error(), fmt.Sprintf("(%d)", limit))
	// The mock should have been called exactly `limit` times.
	assert.Equal(t, limit, llm.callIdx)
}

// TestLlmAgent_MaxIterationsZeroMeansNoLimit verifies that MaxIterations=0
// does not impose any cap — the loop runs until the LLM stops requesting tools.
func TestLlmAgent_MaxIterationsZeroMeansNoLimit(t *testing.T) {
	stopResp := &model.LLMResponse{
		Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
		FinishReason: model.FinishReasonStop,
	}
	llm := &mockLLM{
		name:      "mock",
		responses: []*model.LLMResponse{stopResp},
	}

	a := New(Config{
		Name:          "test-agent",
		Model:         llm,
		MaxIterations: 0, // no limit
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "hi"},
	})

	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "done", msgs[0].Content)
}

// ---------------------------------------------------------------------------
// Integration tests (require OPENAI_API_KEY)
// ---------------------------------------------------------------------------

// TestLlmAgent_SimpleText verifies that the agent produces at least one
// assistant message and stops cleanly for a plain text conversation.
func TestLlmAgent_SimpleText(t *testing.T) {
	llm := newLLMFromEnv(t)

	a := New(Config{
		Name:        "test-agent",
		Description: "A test agent",
		Model:       llm,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "Reply with the single word: pong"},
	})

	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	last := msgs[len(msgs)-1]
	assert.Equal(t, model.RoleAssistant, last.Role)
	assert.NotEmpty(t, last.Content)
}

// TestLlmAgent_WithInstruction verifies that the instruction is forwarded
// and the agent still returns a valid assistant reply.
func TestLlmAgent_WithInstruction(t *testing.T) {
	llm := newLLMFromEnv(t)

	a := New(Config{
		Name:        "test-agent",
		Description: "A test agent",
		Model:       llm,
		Instruction: "You are a concise assistant. Keep answers to one sentence.",
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "What is 2+2?"},
	})

	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.Equal(t, model.RoleAssistant, msgs[len(msgs)-1].Role)
	assert.NotEmpty(t, msgs[len(msgs)-1].Content)
}

// TestLlmAgent_WithEchoTool verifies the full tool-call loop:
// the agent should call the Echo tool and eventually return a final
// assistant stop message.
func TestLlmAgent_WithEchoTool(t *testing.T) {
	llm := newLLMFromEnv(t)

	echoTool, err := builtin.NewEchoTool()
	require.NoError(t, err)

	a := New(Config{
		Name:        "test-agent",
		Description: "A test agent with echo tool",
		Model:       llm,
		Tools:       []tool.Tool{echoTool},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "Please echo the message: hello world"},
	})

	require.NoError(t, err)
	require.NotEmpty(t, msgs)

	// There must be at least one tool message and one final assistant message.
	hasToolMsg := false
	hasFinalAssistant := false
	for _, m := range msgs {
		if m.Role == model.RoleTool {
			hasToolMsg = true
		}
	}
	last := msgs[len(msgs)-1]
	if last.Role == model.RoleAssistant && len(last.ToolCalls) == 0 {
		hasFinalAssistant = true
	}

	assert.True(t, hasToolMsg, "expected at least one tool result message")
	assert.True(t, hasFinalAssistant, "expected a final assistant stop message")
}

// TestLlmAgent_MultiTurn verifies that the agent handles multi-turn
// conversation history correctly.
func TestLlmAgent_MultiTurn(t *testing.T) {
	llm := newLLMFromEnv(t)

	a := New(Config{
		Name:        "test-agent",
		Description: "A test agent",
		Model:       llm,
	}).(*LlmAgent)

	// First turn.
	history := []model.Content{
		{Role: model.RoleUser, Content: "My name is Alice. Just say ok."},
	}
	t.Log("=== turn 1 ===")
	msgs, err := collectMessages(t, a, history)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)

	// Append first turn result to history.
	for _, m := range msgs {
		history = append(history, m)
	}

	// Second turn: verify the agent can reference prior context.
	history = append(history, model.Content{
		Role:    model.RoleUser,
		Content: "What is my name? Reply with just the name.",
	})
	t.Log("=== turn 2 ===")
	msgs2, err := collectMessages(t, a, history)
	require.NoError(t, err)
	require.NotEmpty(t, msgs2)
	last := msgs2[len(msgs2)-1]
	assert.Equal(t, model.RoleAssistant, last.Role)
	assert.Contains(t, last.Content, "Alice")
}

// TestLlmAgent_Streaming_Integration verifies that a real LLM with Stream:true
// delivers at least one partial event before the final complete assistant
// message. Requires OPENAI_API_KEY; skipped when absent.
func TestLlmAgent_Streaming_Integration(t *testing.T) {
	llm := newLLMFromEnv(t)

	a := New(Config{
		Name:        "streaming-agent",
		Description: "A streaming integration test agent",
		Model:       llm,
		Stream:      true,
	}).(*LlmAgent)

	var partialEvents []*model.Event
	var completeEvents []*model.Event

	for event, err := range a.Run(t.Context(), model.EventHistory(
		model.Content{Role: model.RoleUser, Content: "Count from 1 to 5, one number per line."},
	)) {
		require.NoError(t, err)
		if event.Partial {
			partialEvents = append(partialEvents, event)
			t.Logf("  [partial +%d] %q", len(partialEvents), event.Content.Content)
		} else {
			completeEvents = append(completeEvents, event)
			t.Logf("  [complete] role=%s content=%q", event.Content.Role, event.Content.Content)
		}
	}

	// The real LLM must have emitted at least one streaming chunk.
	assert.NotEmpty(t, partialEvents, "expected at least one partial streaming event from the LLM")

	// There must be exactly one final complete assistant message.
	require.NotEmpty(t, completeEvents, "expected a complete assistant event")
	last := completeEvents[len(completeEvents)-1]
	assert.Equal(t, model.RoleAssistant, last.Content.Role)
	assert.NotEmpty(t, last.Content.Content)

	// All partial chunks must carry assistant role.
	for i, ev := range partialEvents {
		assert.Equal(t, model.RoleAssistant, ev.Content.Role, "partial event [%d] must have assistant role", i)
	}
}

// ---------------------------------------------------------------------------
// Reasoning tests
// ---------------------------------------------------------------------------

// TestLlmAgent_Reasoning_PassThrough verifies that a ReasoningContent returned
// by the LLM is present on the message yielded by the agent. This is a pure
// unit test: no real API call is made.
func TestLlmAgent_Reasoning_PassThrough(t *testing.T) {
	mock := &mockLLM{
		name: "mock-reasoning",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:             model.RoleAssistant,
					Content:          "The answer is 42.",
					ReasoningContent: "I need to think about this carefully. 6 times 7 is 42.",
				},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a := New(Config{
		Name:        "reasoning-agent",
		Description: "A test agent with reasoning",
		Model:       mock,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "What is 6 times 7?"},
	})

	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, "The answer is 42.", msgs[0].Content)
	assert.Equal(t, "I need to think about this carefully. 6 times 7 is 42.", msgs[0].ReasoningContent)
}

// TestLlmAgent_Reasoning_PassThrough_WithToolCall verifies that ReasoningContent
// on an intermediate assistant tool-call message is also correctly passed through.
func TestLlmAgent_Reasoning_PassThrough_WithToolCall(t *testing.T) {
	echoTool, err := builtin.NewEchoTool()
	require.NoError(t, err)

	mock := &mockLLM{
		name: "mock-reasoning-tool",
		responses: []*model.LLMResponse{
			// First call: model reasons and decides to call echo.
			{
				Content: model.Content{
					Role:             model.RoleAssistant,
					ReasoningContent: "I should use the echo tool to repeat the message.",
					ToolCalls: []model.ToolCall{
						{ID: "tc-1", Name: "echo", Arguments: json.RawMessage(`{"message":"hello"}`)},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			// Second call: model produces the final answer.
			{
				Content: model.Content{
					Role:    model.RoleAssistant,
					Content: "The echo result is: hello",
				},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a := New(Config{
		Name:        "reasoning-tool-agent",
		Description: "A test agent with reasoning and tool call",
		Model:       mock,
		Tools:       []tool.Tool{echoTool},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "Echo hello"},
	})

	require.NoError(t, err)
	// Expected: [assistant(tool_calls+reasoning), tool(result), assistant(stop)]
	require.Len(t, msgs, 3)

	// First yielded message is the assistant tool-call message with reasoning.
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, "I should use the echo tool to repeat the message.", msgs[0].ReasoningContent)
	assert.Len(t, msgs[0].ToolCalls, 1)

	// Second is the tool result.
	assert.Equal(t, model.RoleTool, msgs[1].Role)
	assert.Equal(t, "tc-1", msgs[1].ToolCallID)

	// Third is the final assistant stop message.
	assert.Equal(t, model.RoleAssistant, msgs[2].Role)
	assert.NotEmpty(t, msgs[2].Content)
}

// TestLlmAgent_ReasoningModel is an integration test that verifies a real
// reasoning model returns non-empty ReasoningContent.
// Required env vars: OPENAI_API_KEY + OPENAI_REASONING_MODEL
// Optional env var:  OPENAI_BASE_URL (e.g. https://api.deepseek.com for DeepSeek-R1)
func TestLlmAgent_ReasoningModel(t *testing.T) {
	llm := newReasoningLLMFromEnv(t)

	a := New(Config{
		Name:        "reasoning-agent",
		Description: "A test reasoning agent",
		Model:       llm,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "What is 15 * 17? Think step by step."},
	})

	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	last := msgs[len(msgs)-1]
	assert.Equal(t, model.RoleAssistant, last.Role)
	assert.NotEmpty(t, last.Content)
	assert.NotEmpty(t, last.ReasoningContent, "expected reasoning model to return non-empty ReasoningContent")
}

// ---------------------------------------------------------------------------
// Streaming unit tests
// ---------------------------------------------------------------------------

// TestLlmAgent_Streaming_YieldsPartialThenComplete verifies that when the LLM
// yields streaming fragments (Partial=true) the agent forwards each one as a
// partial Event before emitting the final complete Event.
func TestLlmAgent_Streaming_YieldsPartialThenComplete(t *testing.T) {
	llm := &streamingMockLLM{
		name: "streaming-mock",
		calls: [][]*model.LLMResponse{
			{
				// Three incremental chunks.
				{Content: model.Content{Role: model.RoleAssistant, Content: "He"}, Partial: true},
				{Content: model.Content{Role: model.RoleAssistant, Content: "llo"}, Partial: true},
				{Content: model.Content{Role: model.RoleAssistant, Content: " World"}, Partial: true},
				// Final assembled response.
				{
					Content:      model.Content{Role: model.RoleAssistant, Content: "Hello World"},
					FinishReason: model.FinishReasonStop,
					TurnComplete: true,
				},
			},
		},
	}

	a := New(Config{
		Name:   "streaming-agent",
		Model:  llm,
		Stream: true,
	}).(*LlmAgent)

	var events []*model.Event
	for event, err := range a.Run(t.Context(), model.EventHistory(
		model.Content{Role: model.RoleUser, Content: "Say hello"},
	)) {
		require.NoError(t, err)
		events = append(events, event)
	}

	// 3 partial chunks + 1 complete event.
	require.Len(t, events, 4)

	assert.True(t, events[0].Partial)
	assert.Equal(t, "He", events[0].Content.Content)

	assert.True(t, events[1].Partial)
	assert.Equal(t, "llo", events[1].Content.Content)

	assert.True(t, events[2].Partial)
	assert.Equal(t, " World", events[2].Content.Content)

	assert.False(t, events[3].Partial)
	assert.Equal(t, model.RoleAssistant, events[3].Content.Role)
	assert.Equal(t, "Hello World", events[3].Content.Content)
}

// TestLlmAgent_Streaming_WithToolCall verifies the full streaming + tool-call
// loop: partial events are forwarded for each LLM call, tool results are
// always emitted as complete events, and the sequence order is correct.
func TestLlmAgent_Streaming_WithToolCall(t *testing.T) {
	echoTool, err := builtin.NewEchoTool()
	require.NoError(t, err)

	llm := &streamingMockLLM{
		name: "streaming-tool-mock",
		calls: [][]*model.LLMResponse{
			// First call: a streaming preamble then the tool-call decision.
			{
				{Content: model.Content{Role: model.RoleAssistant, Content: "Let me echo that..."}, Partial: true},
				{
					Content: model.Content{
						Role:      model.RoleAssistant,
						ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "echo", Arguments: json.RawMessage(`{"message":"streaming"}`)}},
					},
					FinishReason: model.FinishReasonToolCalls,
				},
			},
			// Second call: streaming final answer.
			{
				{Content: model.Content{Role: model.RoleAssistant, Content: "The result: "}, Partial: true},
				{Content: model.Content{Role: model.RoleAssistant, Content: "streaming"}, Partial: true},
				{
					Content:      model.Content{Role: model.RoleAssistant, Content: "The result: streaming"},
					FinishReason: model.FinishReasonStop,
					TurnComplete: true,
				},
			},
		},
	}

	a := New(Config{
		Name:   "streaming-tool-agent",
		Model:  llm,
		Stream: true,
		Tools:  []tool.Tool{echoTool},
	}).(*LlmAgent)

	var events []*model.Event
	for event, err := range a.Run(t.Context(), model.EventHistory(
		model.Content{Role: model.RoleUser, Content: "Echo streaming"},
	)) {
		require.NoError(t, err)
		events = append(events, event)
	}

	// Expected order:
	// [0] partial  – "Let me echo that..."     (streaming preamble, call 1)
	// [1] complete – assistant w/ ToolCalls    (complete, call 1)
	// [2] complete – tool result               (complete, tool execution)
	// [3] partial  – "The result: "            (streaming chunk, call 2)
	// [4] partial  – "streaming"               (streaming chunk, call 2)
	// [5] complete – "The result: streaming"   (complete, call 2)
	require.Len(t, events, 6)

	assert.True(t, events[0].Partial)
	assert.Equal(t, "Let me echo that...", events[0].Content.Content)

	assert.False(t, events[1].Partial)
	assert.Equal(t, model.RoleAssistant, events[1].Content.Role)
	require.Len(t, events[1].Content.ToolCalls, 1)
	assert.Equal(t, "echo", events[1].Content.ToolCalls[0].Name)

	assert.False(t, events[2].Partial)
	assert.Equal(t, model.RoleTool, events[2].Content.Role)
	assert.Equal(t, "tc-1", events[2].Content.ToolCallID)

	assert.True(t, events[3].Partial)
	assert.Equal(t, "The result: ", events[3].Content.Content)

	assert.True(t, events[4].Partial)
	assert.Equal(t, "streaming", events[4].Content.Content)

	assert.False(t, events[5].Partial)
	assert.Equal(t, "The result: streaming", events[5].Content.Content)
}

// TestLlmAgent_Hooks_LLMCallLifecycle verifies that before/after LLM hooks are
// invoked around each GenerateContent call with the correct metadata.
func TestLlmAgent_Hooks_LLMCallLifecycle(t *testing.T) {
	llm := &captureLLM{name: "capture"}
	var order []string

	a := New(Config{
		Name:  "hooked-agent",
		Model: llm,
		BeforeLLMCall: func(ctx context.Context, call *LLMCall) (*model.LLMResponse, error) {
			order = append(order, fmt.Sprintf("before-llm-%d", call.Iteration))
			require.Equal(t, "hooked-agent", call.AgentName)
			require.Equal(t, 1, call.Iteration)
			require.NotNil(t, call.Request)
			return nil, nil
		},
		AfterLLMCall: func(ctx context.Context, call *LLMCall, result *LLMCallResult) error {
			order = append(order, fmt.Sprintf("after-llm-%d", call.Iteration))
			require.NotNil(t, result.Response)
			assert.Equal(t, model.FinishReasonStop, result.Response.FinishReason)
			assert.Zero(t, result.PartialResponses)
			assert.False(t, result.StoppedEarly)
			assert.NoError(t, result.Err)
			return nil
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "hello"}})

	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, []string{"before-llm-1", "after-llm-1"}, order)
	require.NotNil(t, llm.lastRequest)
	assert.Equal(t, "hello", llm.lastRequest.Contents[0].Content)
	assert.Equal(t, model.RoleUser, llm.lastRequest.Contents[0].Role)
	assert.Equal(t, "ok", msgs[0].Content)
}

// hookAwareTool is a test double that records the context values visible to Run.
type hookAwareTool struct {
	name       string
	result     string
	ctxChecker func(ctx context.Context)
	callCount  *atomic.Int64
}

type outcomeTool struct {
	name string
	run  func(context.Context, tool.Call) (tool.Result, error)
}

func (t *outcomeTool) Definition() tool.Definition {
	return tool.Definition{Name: t.name, Description: "configurable test tool"}
}

func (t *outcomeTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	return t.run(ctx, call)
}

func (h *hookAwareTool) Definition() tool.Definition {
	return tool.Definition{Name: h.name, Description: "hook-aware tool"}
}

func (h *hookAwareTool) Run(ctx context.Context, _ tool.Call) (tool.Result, error) {
	h.callCount.Add(1)
	if h.ctxChecker != nil {
		h.ctxChecker(ctx)
	}
	return tool.Result{Content: h.result}, nil
}

// TestLlmAgent_Hooks_ToolCallLifecycle verifies that tool hooks run around
// tool invocation and receive the expected metadata.
func TestLlmAgent_Hooks_ToolCallLifecycle(t *testing.T) {
	var callCount atomic.Int64
	hookTool := &hookAwareTool{
		name:      "hook-tool",
		result:    "tool-result",
		callCount: &callCount,
	}

	mock := &mockLLM{
		name: "mock-hook-tool",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "hook-tool", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	var mu sync.Mutex
	var order []string

	a := New(Config{
		Name:  "hooked-tool-agent",
		Model: mock,
		Tools: []tool.Tool{hookTool},
		BeforeLLMCall: func(ctx context.Context, call *LLMCall) (*model.LLMResponse, error) {
			mu.Lock()
			order = append(order, fmt.Sprintf("before-llm-%d", call.Iteration))
			mu.Unlock()
			return nil, nil
		},
		AfterLLMCall: func(ctx context.Context, call *LLMCall, result *LLMCallResult) error {
			mu.Lock()
			order = append(order, fmt.Sprintf("after-llm-%d", call.Iteration))
			mu.Unlock()
			if call.Iteration == 1 {
				require.NotNil(t, result.Response)
				assert.Equal(t, model.FinishReasonToolCalls, result.Response.FinishReason)
			}
			return nil
		},
		BeforeToolCall: func(ctx context.Context, call *ToolCall) (*ToolCallResult, error) {
			mu.Lock()
			order = append(order, fmt.Sprintf("before-tool-%d-%d", call.Iteration, call.ToolIndex))
			mu.Unlock()
			require.Equal(t, "hooked-tool-agent", call.AgentName)
			require.Equal(t, 1, call.Iteration)
			require.Equal(t, 0, call.ToolIndex)
			require.Equal(t, "hook-tool", call.Definition.Name)
			return nil, nil
		},
		AfterToolCall: func(ctx context.Context, call *ToolCall, result *ToolCallResult) error {
			mu.Lock()
			order = append(order, fmt.Sprintf("after-tool-%d-%d", call.Iteration, call.ToolIndex))
			mu.Unlock()
			assert.Equal(t, "tool-result", result.Result.Content)
			assert.NoError(t, result.Err)
			assert.Equal(t, "tool-result", result.Event.Content.Content)
			return nil
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "run hook tool"}})

	require.NoError(t, err)
	assert.Equal(t, int64(1), callCount.Load())
	assert.Equal(t, []string{
		"before-llm-1",
		"after-llm-1",
		"before-tool-1-0",
		"after-tool-1-0",
		"before-llm-2",
		"after-llm-2",
	}, order)
	require.Len(t, msgs, 3)
	assert.Equal(t, model.RoleTool, msgs[1].Role)
	assert.Equal(t, "tool-result", msgs[1].Content)
}

func TestLlmAgent_ToolHandledFailureContinuesRun(t *testing.T) {
	handled := &outcomeTool{
		name: "lookup",
		run: func(context.Context, tool.Call) (tool.Result, error) {
			return tool.Result{Content: "record not found", IsError: true}, nil
		},
	}
	mock := &mockLLM{
		name: "mock-handled-tool-failure",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "lookup", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{Content: model.Content{Role: model.RoleAssistant, Content: "not found"}, FinishReason: model.FinishReasonStop},
		},
	}
	var captured ToolCallResult
	a := New(Config{
		Name:  "handled-tool-failure-agent",
		Model: mock,
		Tools: []tool.Tool{handled},
		AfterToolCall: func(ctx context.Context, call *ToolCall, result *ToolCallResult) error {
			captured = *result
			return nil
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "look up record"}})

	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, 2, mock.callIdx)
	assert.Equal(t, model.RoleTool, msgs[1].Role)
	require.NotNil(t, msgs[1].ToolResult)
	assert.True(t, msgs[1].ToolResult.IsError)
	assert.Equal(t, "record not found", msgs[1].Content)
	assert.NoError(t, captured.Err)
	assert.True(t, captured.Result.IsError)
}

func TestLlmAgent_ToolExecutionErrorStopsRun(t *testing.T) {
	toolErr := errors.New("database unavailable")
	failing := &outcomeTool{
		name: "lookup",
		run: func(context.Context, tool.Call) (tool.Result, error) {
			return tool.Result{Content: "sensitive partial result", IsError: true}, toolErr
		},
	}
	mock := &mockLLM{
		name: "mock-terminal-tool-failure",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "lookup", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{Content: model.Content{Role: model.RoleAssistant, Content: "must not run"}, FinishReason: model.FinishReasonStop},
		},
	}
	tracer := new(recordingTraceTracer)
	hookErr := errors.New("after hook failed")
	var captured ToolCallResult
	a := New(Config{
		Name:  "terminal-tool-failure-agent",
		Model: mock,
		Tools: []tool.Tool{failing},
		AfterToolCall: func(ctx context.Context, call *ToolCall, result *ToolCallResult) error {
			captured = *result
			return hookErr
		},
	}).(*LlmAgent)
	ctx := adktrace.ContextWithTracer(t.Context(), tracer)

	msgs, err := collectMessagesWithContext(t, ctx, a, []model.Content{{Role: model.RoleUser, Content: "look up record"}})

	require.ErrorIs(t, err, toolErr)
	require.ErrorIs(t, err, hookErr)
	assert.Contains(t, err.Error(), `llmagent: run tool "lookup"`)
	assert.NotContains(t, err.Error(), "sensitive partial result")
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, 1, mock.callIdx)
	assert.Empty(t, captured.Event)
	assert.Empty(t, captured.Result)
	require.ErrorIs(t, captured.Err, toolErr)

	toolEnd, ok := traceEndByKind(tracer, adktrace.KindToolCall)
	require.True(t, ok)
	assert.ErrorIs(t, toolEnd.Err, toolErr)
	assert.ErrorIs(t, toolEnd.Err, hookErr)
	assert.False(t, toolEnd.IsError)
	assert.Empty(t, toolEnd.EventRole)
}

// TestLlmAgent_Hooks_BeforeToolCallErrorStopsRun verifies that hook failures
// are propagated and stop execution before the tool is invoked.
func TestLlmAgent_Hooks_BeforeToolCallErrorStopsRun(t *testing.T) {
	var callCount atomic.Int64
	hookTool := &hookAwareTool{
		name:      "hook-tool",
		result:    "tool-result",
		callCount: &callCount,
	}

	mock := &mockLLM{
		name: "mock-hook-error",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "hook-tool", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	hookErr := fmt.Errorf("tool hook blocked call")
	a := New(Config{
		Name:  "hook-error-agent",
		Model: mock,
		Tools: []tool.Tool{hookTool},
		BeforeToolCall: func(ctx context.Context, call *ToolCall) (*ToolCallResult, error) {
			return nil, hookErr
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "run blocked tool"}})

	require.ErrorIs(t, err, hookErr)
	assert.Equal(t, int64(0), callCount.Load())
	assert.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, 1, mock.callIdx)
}

// TestLlmAgent_MissingTool_ReturnsHandledFailure verifies that a model request
// for an unknown tool is returned to the model without becoming a terminal
// execution error.
func TestLlmAgent_MissingTool_ReturnsHandledFailure(t *testing.T) {
	mock := &mockLLM{
		name: "mock-missing-tool",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "missing-tool", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	var captured ToolCallResult
	a := New(Config{
		Name:  "missing-tool-agent",
		Model: mock,
		AfterToolCall: func(ctx context.Context, call *ToolCall, result *ToolCallResult) error {
			captured = *result
			return nil
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "run missing tool"}})

	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.NoError(t, captured.Err)
	assert.True(t, captured.Result.IsError)
	assert.Equal(t, model.RoleTool, msgs[1].Role)
	require.NotNil(t, msgs[1].ToolResult)
	assert.True(t, msgs[1].ToolResult.IsError)
	assert.Equal(t, `llmagent: tool "missing-tool" not found`, msgs[1].Content)
}

// TestLlmAgent_Hooks_BeforeLLMCall_Skip verifies that returning a non-nil
// *model.LLMResponse from BeforeLLMCall skips the actual LLM call and uses
// the returned response as the result instead.
func TestLlmAgent_Hooks_BeforeLLMCall_Skip(t *testing.T) {
	// A mock that would fail if invoked, ensuring the real LLM is never called.
	neverCalled := &mockLLM{name: "never-called"}

	fakeResp := &model.LLMResponse{
		Content:      model.Content{Role: model.RoleAssistant, Content: "cached response"},
		FinishReason: model.FinishReasonStop,
	}

	var afterCalled bool
	a := New(Config{
		Name:  "skip-llm-agent",
		Model: neverCalled,
		BeforeLLMCall: func(ctx context.Context, call *LLMCall) (*model.LLMResponse, error) {
			return fakeResp, nil
		},
		AfterLLMCall: func(ctx context.Context, call *LLMCall, result *LLMCallResult) error {
			afterCalled = true
			require.NotNil(t, result.Response)
			assert.Equal(t, "cached response", result.Response.Content.Content)
			return nil
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "hello"}})

	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "cached response", msgs[0].Content)
	assert.Equal(t, 0, neverCalled.callIdx, "real LLM must not have been called")
	assert.True(t, afterCalled, "AfterLLMCall must still be invoked after skip")
}

// TestLlmAgent_Hooks_BeforeToolCall_Skip verifies that returning a non-nil
// *ToolCallResult from BeforeToolCall skips the actual tool execution and uses
// the returned result instead.
func TestLlmAgent_Hooks_BeforeToolCall_Skip(t *testing.T) {
	var callCount atomic.Int64
	realTool := &hookAwareTool{
		name:      "real-tool",
		result:    "real-result",
		callCount: &callCount,
	}

	mock := &mockLLM{
		name: "mock-skip-tool",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "real-tool", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	fakeToolMsg := model.Content{
		Role:       model.RoleTool,
		ToolCallID: "tc-1",
		Content:    "injected-result",
	}
	var afterCalled bool

	a := New(Config{
		Name:  "skip-tool-agent",
		Model: mock,
		Tools: []tool.Tool{realTool},
		BeforeToolCall: func(ctx context.Context, call *ToolCall) (*ToolCallResult, error) {
			return &ToolCallResult{
				Event:  model.Event{Content: fakeToolMsg},
				Result: tool.Result{Content: "injected-result"},
			}, nil
		},
		AfterToolCall: func(ctx context.Context, call *ToolCall, result *ToolCallResult) error {
			afterCalled = true
			assert.Equal(t, "injected-result", result.Result.Content)
			assert.Equal(t, "injected-result", result.Event.Content.Content)
			return nil
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "run tool"}})

	require.NoError(t, err)
	assert.Equal(t, int64(0), callCount.Load(), "real tool must not have been called")
	assert.True(t, afterCalled, "AfterToolCall must still be invoked after skip")
	require.Len(t, msgs, 3)
	assert.Equal(t, model.RoleTool, msgs[1].Role)
	assert.Equal(t, "injected-result", msgs[1].Content)
}

func TestLlmAgent_Hooks_BeforeToolCall_SkipErrorStopsRun(t *testing.T) {
	var callCount atomic.Int64
	realTool := &hookAwareTool{
		name:      "real-tool",
		result:    "real-result",
		callCount: &callCount,
	}
	mock := &mockLLM{
		name: "mock-skip-tool-error",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "real-tool", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{Content: model.Content{Role: model.RoleAssistant, Content: "must not run"}, FinishReason: model.FinishReasonStop},
		},
	}
	skipErr := errors.New("blocked by policy")
	var afterCalled bool
	a := New(Config{
		Name:  "skip-tool-error-agent",
		Model: mock,
		Tools: []tool.Tool{realTool},
		BeforeToolCall: func(ctx context.Context, call *ToolCall) (*ToolCallResult, error) {
			return &ToolCallResult{
				Event:  model.Event{Content: model.Content{Role: model.RoleTool, Content: "must be ignored"}},
				Result: tool.Result{Content: "must be ignored", IsError: true},
				Err:    skipErr,
			}, nil
		},
		AfterToolCall: func(ctx context.Context, call *ToolCall, result *ToolCallResult) error {
			afterCalled = true
			require.ErrorIs(t, result.Err, skipErr)
			assert.Empty(t, result.Event)
			assert.Empty(t, result.Result)
			return nil
		},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "run tool"}})

	require.ErrorIs(t, err, skipErr)
	assert.Equal(t, int64(0), callCount.Load())
	assert.True(t, afterCalled)
	assert.Equal(t, 1, mock.callIdx)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
}

// ---------------------------------------------------------------------------
// Compaction summary merging tests
// ---------------------------------------------------------------------------

// captureLLM is a test double that records the last LLMRequest it receives and
// returns a fixed stop response. This lets tests inspect the exact message
// slice that the agent assembles before calling the model.
type captureLLM struct {
	name        string
	lastRequest *model.LLMRequest
}

func (c *captureLLM) Name() string { return c.name }

func (c *captureLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	c.lastRequest = req
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content:      model.Content{Role: model.RoleAssistant, Content: "ok"},
			FinishReason: model.FinishReasonStop,
		}, nil)
	}
}

// TestLlmAgent_CompactionSummary_MergedWithInstruction verifies that when the
// session history contains a system message (compaction summary), its content
// is merged with the agent instruction into a single leading system message,
// and the summary is removed from the middle of the conversation.
func TestLlmAgent_CompactionSummary_MergedWithInstruction(t *testing.T) {
	llm := &captureLLM{name: "capture"}

	a := New(Config{
		Name:        "agent",
		Model:       llm,
		Instruction: "You are a helpful assistant.",
	}).(*LlmAgent)

	input := []model.Content{
		{Role: model.RoleUser, Content: "hello"},
		{Role: model.RoleAssistant, Content: "hi"},
		{Role: model.RoleSystem, Content: "Summary: the user asked about Go."},
		{Role: model.RoleUser, Content: "tell me more"},
	}

	_, err := collectMessages(t, a, input)
	require.NoError(t, err)
	require.NotNil(t, llm.lastRequest)

	msgs := llm.lastRequest.Contents

	// First message must be the merged system message.
	require.NotEmpty(t, msgs)
	assert.Equal(t, model.RoleSystem, msgs[0].Role)
	assert.Contains(t, msgs[0].Content, "You are a helpful assistant.")
	assert.Contains(t, msgs[0].Content, "Summary: the user asked about Go.")

	// No other system messages should appear in the list.
	for i, m := range msgs[1:] {
		assert.NotEqual(t, model.RoleSystem, m.Role,
			"unexpected system message at index %d: %q", i+1, m.Content)
	}

	// Conversation messages must be present and in order.
	require.Len(t, msgs, 4) // 1 system + user + assistant + user
	assert.Equal(t, model.RoleUser, msgs[1].Role)
	assert.Equal(t, "hello", msgs[1].Content)
	assert.Equal(t, model.RoleAssistant, msgs[2].Role)
	assert.Equal(t, model.RoleUser, msgs[3].Role)
	assert.Equal(t, "tell me more", msgs[3].Content)
}

// TestLlmAgent_CompactionSummary_OnlyLastSystemTaken verifies that when there
// are multiple system messages in the session (stale + latest compaction), only
// the last one is merged and all earlier ones are dropped.
func TestLlmAgent_CompactionSummary_OnlyLastSystemTaken(t *testing.T) {
	llm := &captureLLM{name: "capture"}

	a := New(Config{
		Name:        "agent",
		Model:       llm,
		Instruction: "You are concise.",
	}).(*LlmAgent)

	input := []model.Content{
		{Role: model.RoleSystem, Content: "Old summary: session began with weather questions."},
		{Role: model.RoleUser, Content: "What about sports?"},
		{Role: model.RoleAssistant, Content: "Sports are great."},
		{Role: model.RoleSystem, Content: "Latest summary: topics covered weather and sports."},
		{Role: model.RoleUser, Content: "What else?"},
	}

	_, err := collectMessages(t, a, input)
	require.NoError(t, err)
	require.NotNil(t, llm.lastRequest)

	msgs := llm.lastRequest.Contents

	// Only one system message at position 0.
	require.NotEmpty(t, msgs)
	assert.Equal(t, model.RoleSystem, msgs[0].Role)
	assert.Contains(t, msgs[0].Content, "You are concise.")
	assert.Contains(t, msgs[0].Content, "Latest summary: topics covered weather and sports.")
	assert.NotContains(t, msgs[0].Content, "Old summary")

	for i, m := range msgs[1:] {
		assert.NotEqual(t, model.RoleSystem, m.Role,
			"unexpected system message at index %d", i+1)
	}
}

// TestLlmAgent_CompactionSummary_NoInstruction verifies that when the agent has
// no Instruction but the session contains a compaction summary, the summary
// alone becomes the leading system message.
func TestLlmAgent_CompactionSummary_NoInstruction(t *testing.T) {
	llm := &captureLLM{name: "capture"}

	a := New(Config{
		Name:  "agent",
		Model: llm,
		// Instruction intentionally empty.
	}).(*LlmAgent)

	input := []model.Content{
		{Role: model.RoleUser, Content: "recap?"},
		{Role: model.RoleAssistant, Content: "sure"},
		{Role: model.RoleSystem, Content: "Summary: prior conversation about cooking."},
		{Role: model.RoleUser, Content: "continue"},
	}

	_, err := collectMessages(t, a, input)
	require.NoError(t, err)
	require.NotNil(t, llm.lastRequest)

	msgs := llm.lastRequest.Contents

	require.NotEmpty(t, msgs)
	assert.Equal(t, model.RoleSystem, msgs[0].Role)
	assert.Equal(t, "Summary: prior conversation about cooking.", msgs[0].Content)

	// Remaining messages should be non-system conversation messages.
	for i, m := range msgs[1:] {
		assert.NotEqual(t, model.RoleSystem, m.Role,
			"unexpected system message at index %d", i+1)
	}
}

// TestLlmAgent_CompactionSummary_NoSystemInSession verifies that when there is
// no compaction summary in the session, the behaviour is identical to before:
// the agent instruction is prepended as the sole system message.
func TestLlmAgent_CompactionSummary_NoSystemInSession(t *testing.T) {
	llm := &captureLLM{name: "capture"}

	a := New(Config{
		Name:        "agent",
		Model:       llm,
		Instruction: "You are precise.",
	}).(*LlmAgent)

	input := []model.Content{
		{Role: model.RoleUser, Content: "hello"},
		{Role: model.RoleAssistant, Content: "hi"},
		{Role: model.RoleUser, Content: "bye"},
	}

	_, err := collectMessages(t, a, input)
	require.NoError(t, err)
	require.NotNil(t, llm.lastRequest)

	msgs := llm.lastRequest.Contents

	// System message is just the instruction, unchanged.
	require.NotEmpty(t, msgs)
	assert.Equal(t, model.RoleSystem, msgs[0].Role)
	assert.Equal(t, "You are precise.", msgs[0].Content)

	// All subsequent messages are the original conversation messages.
	require.Len(t, msgs, 4) // 1 system + 3 conversation
	assert.Equal(t, model.RoleUser, msgs[1].Role)
	assert.Equal(t, model.RoleAssistant, msgs[2].Role)
	assert.Equal(t, model.RoleUser, msgs[3].Role)
}

// ---------------------------------------------------------------------------
// Parallel tool execution tests
// ---------------------------------------------------------------------------

// slowTool is a test double that sleeps for a configurable duration before
// returning a fixed result, allowing tests to verify concurrent execution.
type slowTool struct {
	name    string
	delay   time.Duration
	result  string
	callLog *atomic.Int64 // counts how many times Run has been called
}

func (s *slowTool) Definition() tool.Definition {
	return tool.Definition{Name: s.name, Description: "a slow tool for testing"}
}

func (s *slowTool) Run(_ context.Context, _ tool.Call) (tool.Result, error) {
	s.callLog.Add(1)
	time.Sleep(s.delay)
	return tool.Result{Content: s.result}, nil
}

// TestLlmAgent_ParallelToolExecution verifies that multiple tool calls issued
// by a single LLM response are executed concurrently rather than sequentially.
// Two tools each sleep for 100 ms; if run sequentially the total elapsed time
// would be ≥ 200 ms, but parallel execution should complete in < 200 ms.
func TestLlmAgent_ParallelToolExecution(t *testing.T) {
	const delay = 100 * time.Millisecond

	var callCount atomic.Int64
	toolA := &slowTool{name: "tool-a", delay: delay, result: "result-a", callLog: &callCount}
	toolB := &slowTool{name: "tool-b", delay: delay, result: "result-b", callLog: &callCount}

	mock := &mockLLM{
		name: "mock-parallel",
		responses: []*model.LLMResponse{
			// First call: LLM requests both tools simultaneously.
			{
				Content: model.Content{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "tc-a", Name: "tool-a", Arguments: json.RawMessage(`{}`)},
						{ID: "tc-b", Name: "tool-b", Arguments: json.RawMessage(`{}`)},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			// Second call: final answer.
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a := New(Config{
		Name:  "parallel-agent",
		Model: mock,
		Tools: []tool.Tool{toolA, toolB},
	}).(*LlmAgent)

	start := time.Now()
	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "run both tools"},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)

	// Both tools must have been called exactly once.
	assert.Equal(t, int64(2), callCount.Load(), "both tools should be called")

	// Verify tool result messages are present and ordered correctly.
	var toolMsgs []model.Content
	for _, m := range msgs {
		if m.Role == model.RoleTool {
			toolMsgs = append(toolMsgs, m)
		}
	}
	require.Len(t, toolMsgs, 2)
	assert.Equal(t, "tc-a", toolMsgs[0].ToolCallID)
	assert.Equal(t, "result-a", toolMsgs[0].Content)
	assert.Equal(t, "tc-b", toolMsgs[1].ToolCallID)
	assert.Equal(t, "result-b", toolMsgs[1].Content)

	// Parallel execution should finish well under 2×delay.
	assert.Less(t, elapsed, 2*delay,
		"parallel tool execution should be faster than sequential (elapsed=%v, 2×delay=%v)", elapsed, 2*delay)

	t.Logf("elapsed=%v (2×delay=%v)", elapsed, 2*delay)
}

func TestLlmAgent_ParallelToolErrorsPreferNonCancellationCause(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	blocking := &outcomeTool{
		name: "blocking",
		run: func(ctx context.Context, _ tool.Call) (tool.Result, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return tool.Result{}, ctx.Err()
		},
	}
	rootErr := errors.New("dependency failed")
	failing := &outcomeTool{
		name: "failing",
		run: func(context.Context, tool.Call) (tool.Result, error) {
			<-started
			return tool.Result{}, rootErr
		},
	}
	mock := &mockLLM{
		name: "mock-parallel-tool-failure",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "tc-1", Name: "blocking", Arguments: json.RawMessage(`{}`)},
						{ID: "tc-2", Name: "failing", Arguments: json.RawMessage(`{}`)},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
		},
	}
	a := New(Config{
		Name:  "parallel-tool-failure-agent",
		Model: mock,
		Tools: []tool.Tool{blocking, failing},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{{Role: model.RoleUser, Content: "run tools"}})

	require.ErrorIs(t, err, rootErr)
	require.Len(t, msgs, 1)
	select {
	case <-cancelled:
	default:
		t.Fatal("blocking sibling was not cancelled")
	}
}

// ---------------------------------------------------------------------------
// ToolTimeout tests
// ---------------------------------------------------------------------------

// blockingTool is a test double that blocks until its context is cancelled,
// respecting the context deadline. It is used to verify ToolTimeout behaviour.
type blockingTool struct {
	name string
}

func (b *blockingTool) Definition() tool.Definition {
	return tool.Definition{Name: b.name, Description: "a blocking tool for testing"}
}

func (b *blockingTool) Run(ctx context.Context, _ tool.Call) (tool.Result, error) {
	<-ctx.Done()
	return tool.Result{}, ctx.Err()
}

// TestLlmAgent_ToolTimeout_ExceedsDeadline verifies that when ToolTimeout is set
// and a tool exceeds it, the deadline error terminates the current agent run
// without being converted into model-visible tool content.
func TestLlmAgent_ToolTimeout_ExceedsDeadline(t *testing.T) {
	bt := &blockingTool{name: "blocker"}

	mock := &mockLLM{
		name: "mock-timeout",
		responses: []*model.LLMResponse{
			// First call: LLM requests the blocking tool.
			{
				Content: model.Content{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "tc-1", Name: "blocker", Arguments: json.RawMessage(`{}`)},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			// Must not be reached after the terminal timeout.
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a := New(Config{
		Name:        "timeout-agent",
		Model:       mock,
		Tools:       []tool.Tool{bt},
		ToolTimeout: 30 * time.Millisecond,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "run the blocker"},
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, 1, mock.callIdx)
}

// TestLlmAgent_ToolTimeout_CompletesWithinDeadline verifies that when a tool
// finishes before the ToolTimeout the normal result is returned unchanged.
func TestLlmAgent_ToolTimeout_CompletesWithinDeadline(t *testing.T) {
	var callCount atomic.Int64
	fast := &slowTool{name: "fast", delay: 10 * time.Millisecond, result: "fast-result", callLog: &callCount}

	mock := &mockLLM{
		name: "mock-fast",
		responses: []*model.LLMResponse{
			{
				Content: model.Content{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "tc-1", Name: "fast", Arguments: json.RawMessage(`{}`)}},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{
				Content:      model.Content{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a := New(Config{
		Name:        "fast-timeout-agent",
		Model:       mock,
		Tools:       []tool.Tool{fast},
		ToolTimeout: 500 * time.Millisecond,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Content{
		{Role: model.RoleUser, Content: "run fast"},
	})

	require.NoError(t, err)
	assert.Equal(t, int64(1), callCount.Load())

	var toolMsg *model.Content
	for i := range msgs {
		if msgs[i].Role == model.RoleTool {
			toolMsg = &msgs[i]
			break
		}
	}
	require.NotNil(t, toolMsg)
	assert.Equal(t, "fast-result", toolMsg.Content)
}
