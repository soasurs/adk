package llmagent

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/model/openai"
	"soasurs.dev/soasurs/adk/tool"
	"soasurs.dev/soasurs/adk/tool/builtin"
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

func (m *mockLLM) Generate(_ context.Context, _ *model.LLMRequest, _ *model.GenerateConfig) (*model.LLMResponse, error) {
	if m.callIdx >= len(m.responses) {
		return nil, fmt.Errorf("mockLLM: no more responses (call %d)", m.callIdx)
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
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
	if m.ReasoningContent != "" {
		t.Logf("  [%d] %-9s reasoning  %s", idx, m.Role, m.ReasoningContent)
	}
	t.Logf("  [%d] %-9s %s", idx, m.Role, m.Content)
}

// collectMessages drains the agent Run iterator, logs every yielded message,
// and returns all messages plus the first error encountered (if any).
func collectMessages(t *testing.T, agent *LlmAgent, messages []model.Message) ([]model.Message, error) {
	t.Helper()
	t.Log("  --- input ---")
	for i, m := range messages {
		logMessage(t, i, m)
	}
	t.Log("  --- output ---")
	var collected []model.Message
	for msg, err := range agent.Run(context.Background(), messages) {
		if err != nil {
			return collected, err
		}
		logMessage(t, len(collected), msg)
		collected = append(collected, msg)
	}
	return collected, nil
}

// ---------------------------------------------------------------------------
// Integration tests (require OPENAI_API_KEY)
// ---------------------------------------------------------------------------

// TestLlmAgent_SimpleText verifies that the agent produces at least one
// assistant message and stops cleanly for a plain text conversation.
func TestLlmAgent_SimpleText(t *testing.T) {
	llm := newLLMFromEnv(t)

	a := New(LlmAgentConfig{
		Name:        "test-agent",
		Description: "A test agent",
		Model:       llm,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Message{
		{Role: model.RoleUser, Content: "Reply with the single word: pong"},
	})

	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	last := msgs[len(msgs)-1]
	assert.Equal(t, model.RoleAssistant, last.Role)
	assert.NotEmpty(t, last.Content)
}

// TestLlmAgent_WithSystemPrompt verifies that the system prompt is forwarded
// and the agent still returns a valid assistant reply.
func TestLlmAgent_WithSystemPrompt(t *testing.T) {
	llm := newLLMFromEnv(t)

	a := New(LlmAgentConfig{
		Name:         "test-agent",
		Description:  "A test agent",
		Model:        llm,
		SystemPrompt: "You are a concise assistant. Keep answers to one sentence.",
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Message{
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

	echoTool := builtin.NewEchoTool()

	a := New(LlmAgentConfig{
		Name:        "test-agent",
		Description: "A test agent with echo tool",
		Model:       llm,
		Tools:       []tool.Tool{echoTool},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Message{
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

	a := New(LlmAgentConfig{
		Name:        "test-agent",
		Description: "A test agent",
		Model:       llm,
	}).(*LlmAgent)

	// First turn.
	history := []model.Message{
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
	history = append(history, model.Message{
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
				Message: model.Message{
					Role:             model.RoleAssistant,
					Content:          "The answer is 42.",
					ReasoningContent: "I need to think about this carefully. 6 times 7 is 42.",
				},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a := New(LlmAgentConfig{
		Name:        "reasoning-agent",
		Description: "A test agent with reasoning",
		Model:       mock,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Message{
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
	echoTool := builtin.NewEchoTool()

	mock := &mockLLM{
		name: "mock-reasoning-tool",
		responses: []*model.LLMResponse{
			// First call: model reasons and decides to call echo.
			{
				Message: model.Message{
					Role:             model.RoleAssistant,
					ReasoningContent: "I should use the echo tool to repeat the message.",
					ToolCalls: []model.ToolCall{
						{ID: "tc-1", Name: "echo", Arguments: `{"message":"hello"}`},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			// Second call: model produces the final answer.
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "The echo result is: hello",
				},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a := New(LlmAgentConfig{
		Name:        "reasoning-tool-agent",
		Description: "A test agent with reasoning and tool call",
		Model:       mock,
		Tools:       []tool.Tool{echoTool},
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Message{
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

	a := New(LlmAgentConfig{
		Name:        "reasoning-agent",
		Description: "A test reasoning agent",
		Model:       llm,
	}).(*LlmAgent)

	msgs, err := collectMessages(t, a, []model.Message{
		{Role: model.RoleUser, Content: "What is 15 * 17? Think step by step."},
	})

	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	last := msgs[len(msgs)-1]
	assert.Equal(t, model.RoleAssistant, last.Role)
	assert.NotEmpty(t, last.Content)
	assert.NotEmpty(t, last.ReasoningContent, "expected reasoning model to return non-empty ReasoningContent")
}
