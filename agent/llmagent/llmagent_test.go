package llmagent

import (
	"context"
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
