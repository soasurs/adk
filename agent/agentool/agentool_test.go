package agentool_test

import (
	"context"
	"fmt"
	"iter"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/agent/agentool"
	"github.com/soasurs/adk/agent/llmagent"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/openai"
	"github.com/soasurs/adk/tool"
)

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

// mockLLM replays a fixed sequence of responses for deterministic unit tests.
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

// TestAgentTool_OrchestratorFlow verifies the full Thought→Action→Observation
// cycle: an orchestrator LlmAgent delegates a task to a sub-LlmAgent via
// agentool, receives the result as a tool message, and produces a final answer.
// No external API calls are made — both LLMs are backed by mockLLM.
func TestAgentTool_OrchestratorFlow(t *testing.T) {
	// --- sub-agent --------------------------------------------------------
	// Returns a single text answer when asked a math question.
	subLLM := &mockLLM{
		name: "sub-llm",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "The answer is 4."},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	subAgent := llmagent.New(llmagent.Config{
		Name:        "math_agent",
		Description: "Solves simple math problems.",
		Model:       subLLM,
	})

	// --- orchestrator -----------------------------------------------------
	// Call 1: decides to delegate to math_agent via tool call.
	// Call 2: produces the final answer after observing the tool result.
	orchLLM := &mockLLM{
		name: "orch-llm",
		responses: []*model.LLMResponse{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "tc-1", Name: "math_agent", Arguments: `{"task":"what is 2+2?"}`},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			},
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "2+2 equals 4."},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	orchestrator := llmagent.New(llmagent.Config{
		Name:        "orchestrator",
		Description: "Routes tasks to specialist agents.",
		Model:       orchLLM,
		Tools:       []tool.Tool{agentool.New(subAgent)},
	})

	// --- run --------------------------------------------------------------
	var msgs []model.Message
	for event, err := range orchestrator.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "What is 2+2?"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	// Expected sequence:
	//   [0] assistant — tool call to math_agent
	//   [1] tool      — result returned by agentool (sub-agent's answer)
	//   [2] assistant — final answer from orchestrator
	require.Len(t, msgs, 3)

	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	require.Len(t, msgs[0].ToolCalls, 1)
	assert.Equal(t, "math_agent", msgs[0].ToolCalls[0].Name)

	assert.Equal(t, model.RoleTool, msgs[1].Role)
	assert.Equal(t, "tc-1", msgs[1].ToolCallID)
	assert.Contains(t, msgs[1].Content, "The answer is 4.")

	assert.Equal(t, model.RoleAssistant, msgs[2].Role)
	assert.Equal(t, "2+2 equals 4.", msgs[2].Content)
}

// ---------------------------------------------------------------------------
// Integration tests (require OPENAI_API_KEY)
// ---------------------------------------------------------------------------

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

// TestAgentTool_Integration_OrchestratorDelegation verifies the end-to-end
// flow using real LLM calls:
//
//   - A "translator" sub-agent is instructed to translate text to Chinese.
//   - An orchestrator agent knows about the translator via AgentTool.
//   - The user asks the orchestrator to translate "hello" to Chinese.
//
// Expected behaviour: the orchestrator calls the translator tool, receives the
// translation, and returns a final answer containing the Chinese text.
//
// Required env var: OPENAI_API_KEY
// Optional env vars: OPENAI_BASE_URL, OPENAI_MODEL
func TestAgentTool_Integration_OrchestratorDelegation(t *testing.T) {
	llm := newLLMFromEnv(t)

	// Sub-agent: translates text to Chinese.
	translatorAgent := llmagent.New(llmagent.Config{
		Name:        "translator",
		Description: "Translates the given text into Chinese. Input should be the text to translate.",
		Model:       llm,
		Instruction: "You are a professional translator. Translate the user's text into Chinese. Reply with only the translated text, nothing else.",
	})

	// Orchestrator: routes translation tasks to the translator agent.
	orchestrator := llmagent.New(llmagent.Config{
		Name:        "orchestrator",
		Description: "Routes tasks to specialist agents.",
		Model:       llm,
		Instruction: "You are an orchestrator. When asked to translate text, always use the translator tool.",
		Tools:       []tool.Tool{agentool.New(translatorAgent)},
	})

	t.Log("=== input ===")
	t.Logf("  [0] user      Translate 'hello' to Chinese.")
	t.Log("=== output ===")

	var msgs []model.Message
	for event, err := range orchestrator.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "Please translate 'hello' to Chinese."},
	}) {
		require.NoError(t, err)
		if event.Partial {
			continue
		}
		logMessage(t, len(msgs), event.Message)
		msgs = append(msgs, event.Message)
	}

	require.NotEmpty(t, msgs, "expected at least one output message")

	// There should be a tool-call message to the translator.
	hasToolCall := false
	for _, m := range msgs {
		if m.Role == model.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.Name == "translator" {
					hasToolCall = true
				}
			}
		}
	}
	assert.True(t, hasToolCall, "orchestrator should have called the translator tool")

	// There should be a tool result message.
	hasToolResult := false
	for _, m := range msgs {
		if m.Role == model.RoleTool {
			hasToolResult = true
			assert.NotEmpty(t, m.Content, "tool result should not be empty")
		}
	}
	assert.True(t, hasToolResult, "expected a tool result message from translator")

	// The last message should be a final assistant response.
	last := msgs[len(msgs)-1]
	assert.Equal(t, model.RoleAssistant, last.Role)
	assert.NotEmpty(t, last.Content, "final orchestrator response should not be empty")
}
