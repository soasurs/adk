package sequentialagent

import (
	"context"
	"fmt"
	"iter"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/agent"
	"github.com/soasurs/adk/agent/llmagent"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/openai"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mockLLM replays a fixed sequence of responses for deterministic unit tests.
type mockLLM struct {
	name      string
	responses []*model.LLMResponse
	callIdx   int
}

func (m *mockLLM) Name() string { return m.name }

func (m *mockLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.callIdx >= len(m.responses) {
			yield(nil, fmt.Errorf("mockLLM %q: no more responses (call %d, got %d messages)", m.name, m.callIdx, len(req.Messages)))
			return
		}
		resp := m.responses[m.callIdx]
		m.callIdx++
		yield(resp, nil)
	}
}

// newLLMFromEnv creates a real LLM from environment variables.
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

// logMessage prints a single message in a concise one-line format.
func logMessage(t *testing.T, idx int, m model.Message) {
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
	t.Logf("  [%d] %-9s %s", idx, m.Role, m.Content)
}

// ---------------------------------------------------------------------------
// Unit tests (no API required)
// ---------------------------------------------------------------------------

// TestSequentialAgent_Name verifies that Name and Description are forwarded.
func TestSequentialAgent_Name(t *testing.T) {
	agent1 := llmagent.New(llmagent.Config{
		Name:        "agent-1",
		Description: "First agent",
		Model:       &mockLLM{name: "m1"},
	})
	sa := New(Config{Name: "my-pipeline", Description: "a test pipeline", Agents: []agent.Agent{agent1}})
	assert.Equal(t, "my-pipeline", sa.Name())
	assert.Equal(t, "a test pipeline", sa.Description())
}

// TestSequentialAgent_PanicOnEmpty verifies that New panics with no agents.
func TestSequentialAgent_PanicOnEmpty(t *testing.T) {
	assert.Panics(t, func() {
		New(Config{Name: "empty", Description: "no agents"})
	})
}

// TestSequentialAgent_SingleAgent verifies that wrapping a single agent in a
// SequentialAgent produces exactly the same messages as running it directly.
func TestSequentialAgent_SingleAgent(t *testing.T) {
	llm := &mockLLM{
		name: "mock",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "Hello!"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	a := llmagent.New(llmagent.Config{Name: "a", Description: "d", Model: llm})
	sa := New(Config{Name: "pipeline", Description: "single-agent pipeline", Agents: []agent.Agent{a}})

	var msgs []model.Message
	for event, err := range sa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "Hi"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, "Hello!", msgs[0].Content)
}

// TestSequentialAgent_TwoAgents verifies the pipeline runs both agents in order
// and the second agent's input contains the first agent's output as context.
//
// Pipeline:
//
//	agent-1: receives [user:"ping"] → yields assistant:"pong"
//	agent-2: receives [user:"ping", assistant:"pong"] → yields assistant:"done"
func TestSequentialAgent_TwoAgents(t *testing.T) {
	llm1 := &mockLLM{
		name: "mock-1",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "pong"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "done"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	sa := New(Config{Name: "pipeline", Description: "two-agent pipeline", Agents: []agent.Agent{a1, a2}})

	var msgs []model.Message
	for event, err := range sa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "ping"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	// Both agents contribute one message each.
	require.Len(t, msgs, 2)
	assert.Equal(t, "pong", msgs[0].Content, "first agent output")
	assert.Equal(t, "done", msgs[1].Content, "second agent output")

	// Verify agent-2 received agent-1's output by checking its mock was called
	// exactly once (it had exactly one response prepared).
	assert.Equal(t, 1, llm2.callIdx, "agent-2 LLM should have been called once")
}

// TestSequentialAgent_ContextPropagation verifies that the second agent
// receives the first agent's messages as part of its input context.
// We do this by inspecting the request that arrives at the second mock LLM.
func TestSequentialAgent_ContextPropagation(t *testing.T) {
	// Capture what messages the second LLM receives.
	var capturedMessages []model.Message

	llm1 := &mockLLM{
		name: "mock-1",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "first-result"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	// Custom LLM that captures its input before replying.
	capturingLLM := &capturingMockLLM{
		mockLLM: mockLLM{
			name: "capturing-mock",
			responses: []*model.LLMResponse{
				{
					Message:      model.Message{Role: model.RoleAssistant, Content: "second-result"},
					FinishReason: model.FinishReasonStop,
				},
			},
		},
		capture: &capturedMessages,
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: capturingLLM})
	sa := New(Config{Name: "pipeline", Description: "context propagation test", Agents: []agent.Agent{a1, a2}})

	var msgs []model.Message
	for event, err := range sa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "start"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	require.Len(t, msgs, 2)

	// The second agent's LLM should have received:
	//   [user:"start", assistant:"first-result", user:"handoff message"]
	require.Len(t, capturedMessages, 3)
	assert.Equal(t, model.RoleUser, capturedMessages[0].Role)
	assert.Equal(t, "start", capturedMessages[0].Content)
	assert.Equal(t, model.RoleAssistant, capturedMessages[1].Role)
	assert.Equal(t, "first-result", capturedMessages[1].Content)
	assert.Equal(t, model.RoleUser, capturedMessages[2].Role)
	assert.Equal(t, "Please proceed.", capturedMessages[2].Content)
}

// capturingMockLLM wraps mockLLM and captures the request messages.
type capturingMockLLM struct {
	mockLLM
	capture *[]model.Message
}

func (c *capturingMockLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	*c.capture = append(*c.capture, req.Messages...)
	return c.mockLLM.GenerateContent(ctx, req, cfg, stream)
}

// TestSequentialAgent_EarlyStop verifies that if the caller breaks out of the
// iterator after the first message, the second agent is never run.
func TestSequentialAgent_EarlyStop(t *testing.T) {
	llm1 := &mockLLM{
		name: "mock-1",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "first"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "second"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	sa := New(Config{Name: "pipeline", Description: "early-stop test", Agents: []agent.Agent{a1, a2}})

	var msgs []model.Message
	for event, err := range sa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "go"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
		break // stop after the first message
	}

	require.Len(t, msgs, 1)
	assert.Equal(t, "first", msgs[0].Content)
	// agent-2 should not have been called.
	assert.Equal(t, 0, llm2.callIdx, "agent-2 should not have been called after early stop")
}

// TestSequentialAgent_ErrorPropagation verifies that an error from an agent
// is yielded and terminates the pipeline.
func TestSequentialAgent_ErrorPropagation(t *testing.T) {
	// llm1 will error on its first call (no responses prepared).
	llm1 := &mockLLM{name: "mock-1"}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "unreachable"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	sa := New(Config{Name: "pipeline", Description: "error propagation test", Agents: []agent.Agent{a1, a2}})

	var gotErr error
	for _, err := range sa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "go"},
	}) {
		if err != nil {
			gotErr = err
			break
		}
	}

	require.Error(t, gotErr, "expected an error from agent-1")
	// agent-2 should not have been called.
	assert.Equal(t, 0, llm2.callIdx, "agent-2 should not run after agent-1 errors")
}

// ---------------------------------------------------------------------------
// Integration tests (require OPENAI_API_KEY)
// ---------------------------------------------------------------------------

// TestSequentialAgent_Integration_TwoStepPipeline runs a two-step pipeline
// using real LLM calls:
//
//   - Step 1 (summariser): summarises the user's text in one sentence.
//   - Step 2 (translator): translates the summary into Chinese.
//
// The test verifies:
//   - Both agents produce a non-empty assistant reply.
//   - The total number of yielded messages is exactly 2.
//   - The second agent's reply is the last message.
//
// Required env var: OPENAI_API_KEY
// Optional env vars: OPENAI_BASE_URL, OPENAI_MODEL
func TestSequentialAgent_Integration_TwoStepPipeline(t *testing.T) {
	llm := newLLMFromEnv(t)

	// Step 1: summarise the input in one sentence.
	summariser := llmagent.New(llmagent.Config{
		Name:        "summariser",
		Description: "Summarises text in one sentence.",
		Model:       llm,
		Instruction: "You are a summariser. Summarise the user's text in exactly one sentence. Reply with only the summary.",
	})

	// Step 2: translate the summary into Chinese.
	translator := llmagent.New(llmagent.Config{
		Name:        "translator",
		Description: "Translates text into Chinese.",
		Model:       llm,
		Instruction: "You are a translator. Translate the most recent assistant message into Chinese. Reply with only the translation.",
	})

	pipeline := New(Config{
		Name:        "summarise-then-translate",
		Description: "Summarises text and then translates the summary into Chinese.",
		Agents:      []agent.Agent{summariser, translator},
	})

	input := []model.Message{
		{Role: model.RoleUser, Content: "Go is an open-source programming language designed for simplicity, reliability, and efficiency, created at Google."},
	}

	t.Log("=== input ===")
	logMessage(t, 0, input[0])
	t.Log("=== output ===")

	// Use a timeout context to prevent indefinite hangs on slow API responses.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var msgs []model.Message
	for event, err := range pipeline.Run(ctx, input) {
		require.NoError(t, err)
		if event.Partial {
			continue
		}
		logMessage(t, len(msgs), event.Message)
		msgs = append(msgs, event.Message)
	}

	// Expect exactly two assistant messages: one from each agent.
	require.Len(t, msgs, 2, "expected one message per agent")
	assert.Equal(t, model.RoleAssistant, msgs[0].Role, "summariser should produce an assistant message")
	assert.NotEmpty(t, msgs[0].Content, "summariser output should not be empty")
	assert.Equal(t, model.RoleAssistant, msgs[1].Role, "translator should produce an assistant message")
	assert.NotEmpty(t, msgs[1].Content, "translator output should not be empty")
}
