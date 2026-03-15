package anthropic

import (
	"context"
	"os"
	"testing"

	goanthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/builtin"
)

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool { return &b }

// callGenerate is a test helper that calls GenerateContent(stream=false) and
// returns the single complete response, mimicking the old Generate API.
func callGenerate(ctx context.Context, llm model.LLM, req *model.LLMRequest, cfg *model.GenerateConfig) (*model.LLMResponse, error) {
	var resp *model.LLMResponse
	for r, err := range llm.GenerateContent(ctx, req, cfg, false) {
		if err != nil {
			return nil, err
		}
		if !r.Partial {
			resp = r
		}
	}
	return resp, nil
}

// newClientFromEnv creates a model.LLM from environment variables.
// Required: ANTHROPIC_API_KEY — test is skipped when absent.
// Optional: ANTHROPIC_MODEL — model name; defaults to "claude-haiku-4-5" when absent.
func newClientFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	modelName := os.Getenv("ANTHROPIC_MODEL")
	if modelName == "" {
		modelName = "claude-haiku-4-5"
	}
	return New(apiKey, modelName)
}

// newThinkingClientFromEnv creates a model.LLM for thinking model tests.
// Required: ANTHROPIC_API_KEY + ANTHROPIC_THINKING_MODEL — test is skipped when either is absent.
func newThinkingClientFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	modelName := os.Getenv("ANTHROPIC_THINKING_MODEL")
	if modelName == "" {
		t.Skip("ANTHROPIC_THINKING_MODEL not set")
	}
	return New(apiKey, modelName)
}

// ---------------------------------------------------------------------------
// Unit tests (no network required)
// ---------------------------------------------------------------------------

func TestMessages_Name(t *testing.T) {
	m := &Model{modelName: "claude-haiku-4-5"}
	assert.Equal(t, "claude-haiku-4-5", m.Name())
}

func TestConvertStopReason(t *testing.T) {
	cases := []struct {
		input    goanthropic.StopReason
		expected model.FinishReason
	}{
		{goanthropic.StopReasonEndTurn, model.FinishReasonStop},
		{goanthropic.StopReasonMaxTokens, model.FinishReasonLength},
		{goanthropic.StopReasonToolUse, model.FinishReasonToolCalls},
		{goanthropic.StopReasonStopSequence, model.FinishReasonStop},
		{goanthropic.StopReason("unknown"), model.FinishReasonStop},
		{"", model.FinishReasonStop},
	}
	for _, tc := range cases {
		t.Run(string(tc.input), func(t *testing.T) {
			assert.Equal(t, tc.expected, convertStopReason(tc.input))
		})
	}
}

func TestConvertMessages_System(t *testing.T) {
	messages, system, err := convertMessages([]model.Message{
		{Role: model.RoleSystem, Content: "you are helpful"},
	})
	require.NoError(t, err)
	assert.Empty(t, messages)
	require.Len(t, system, 1)
	assert.Equal(t, "you are helpful", system[0].Text)
}

func TestConvertMessages_User(t *testing.T) {
	messages, system, err := convertMessages([]model.Message{
		{Role: model.RoleUser, Content: "hello"},
	})
	require.NoError(t, err)
	assert.Empty(t, system)
	require.Len(t, messages, 1)
	assert.Equal(t, goanthropic.MessageParamRoleUser, messages[0].Role)
	require.Len(t, messages[0].Content, 1)
	require.NotNil(t, messages[0].Content[0].OfText)
	assert.Equal(t, "hello", messages[0].Content[0].OfText.Text)
}

func TestConvertMessages_Assistant_Text(t *testing.T) {
	messages, _, err := convertMessages([]model.Message{
		{Role: model.RoleAssistant, Content: "hi there"},
	})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, goanthropic.MessageParamRoleAssistant, messages[0].Role)
	require.Len(t, messages[0].Content, 1)
	require.NotNil(t, messages[0].Content[0].OfText)
	assert.Equal(t, "hi there", messages[0].Content[0].OfText.Text)
}

func TestConvertMessages_Assistant_ToolCalls(t *testing.T) {
	messages, _, err := convertMessages([]model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: `{"echo":"hi"}`},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, goanthropic.MessageParamRoleAssistant, messages[0].Role)
	require.Len(t, messages[0].Content, 1)
	require.NotNil(t, messages[0].Content[0].OfToolUse)
	assert.Equal(t, "call_1", messages[0].Content[0].OfToolUse.ID)
	assert.Equal(t, "Echo", messages[0].Content[0].OfToolUse.Name)
}

func TestConvertMessages_Tool(t *testing.T) {
	messages, _, err := convertMessages([]model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo"},
			},
		},
		{Role: model.RoleTool, Content: "pong", ToolCallID: "call_1"},
	})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	// Second message is the tool result as a user message.
	toolMsg := messages[1]
	assert.Equal(t, goanthropic.MessageParamRoleUser, toolMsg.Role)
	require.Len(t, toolMsg.Content, 1)
	require.NotNil(t, toolMsg.Content[0].OfToolResult)
	assert.Equal(t, "call_1", toolMsg.Content[0].OfToolResult.ToolUseID)
	require.Len(t, toolMsg.Content[0].OfToolResult.Content, 1)
	require.NotNil(t, toolMsg.Content[0].OfToolResult.Content[0].OfText)
	assert.Equal(t, "pong", toolMsg.Content[0].OfToolResult.Content[0].OfText.Text)
}

func TestConvertMessages_ConsecutiveToolsBatched(t *testing.T) {
	messages, _, err := convertMessages([]model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "c1", Name: "Echo"},
				{ID: "c2", Name: "Echo"},
			},
		},
		{Role: model.RoleTool, Content: "r1", ToolCallID: "c1"},
		{Role: model.RoleTool, Content: "r2", ToolCallID: "c2"},
	})
	require.NoError(t, err)
	require.Len(t, messages, 2, "two consecutive tool messages should be batched into one user message")
	assert.Equal(t, goanthropic.MessageParamRoleUser, messages[1].Role)
	assert.Len(t, messages[1].Content, 2)
}

func TestConvertMessages_UnknownRole(t *testing.T) {
	_, _, err := convertMessages([]model.Message{{Role: "invalid"}})
	assert.Error(t, err)
}

func TestApplyConfig_Temperature(t *testing.T) {
	p := &goanthropic.MessageNewParams{}
	applyConfig(p, &model.GenerateConfig{Temperature: 0.7})
	assert.True(t, p.Temperature.Valid())
	assert.InDelta(t, 0.7, p.Temperature.Value, 0.001)
}

func TestApplyConfig_EnableThinking(t *testing.T) {
	tests := []struct {
		name           string
		cfg            model.GenerateConfig
		wantEnabled    bool
		wantDisabled   bool
		wantNoThinking bool
	}{
		{
			name:        "EnableThinking=true → OfEnabled set",
			cfg:         model.GenerateConfig{EnableThinking: boolPtr(true)},
			wantEnabled: true,
		},
		{
			name:         "EnableThinking=false → OfDisabled set",
			cfg:          model.GenerateConfig{EnableThinking: boolPtr(false)},
			wantDisabled: true,
		},
		{
			name:           "nil EnableThinking → no ThinkingConfig",
			cfg:            model.GenerateConfig{},
			wantNoThinking: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &goanthropic.MessageNewParams{}
			applyConfig(p, &tt.cfg)
			if tt.wantNoThinking {
				assert.Nil(t, p.Thinking.OfEnabled)
				assert.Nil(t, p.Thinking.OfDisabled)
				return
			}
			if tt.wantEnabled {
				require.NotNil(t, p.Thinking.OfEnabled)
				assert.Equal(t, int64(defaultThinkingBudget), p.Thinking.OfEnabled.BudgetTokens)
			}
			if tt.wantDisabled {
				require.NotNil(t, p.Thinking.OfDisabled)
			}
		})
	}
}

func TestConvertTools_Empty(t *testing.T) {
	result, err := convertTools(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConvertTools_EchoTool(t *testing.T) {
	echo := builtin.NewEchoTool()
	result, err := convertTools([]tool.Tool{echo})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.NotNil(t, result[0].OfTool)
	assert.Equal(t, echo.Definition().Name, result[0].OfTool.Name)
	assert.True(t, result[0].OfTool.Description.Valid())
	assert.Equal(t, echo.Definition().Description, result[0].OfTool.Description.Value)
}

// ---------------------------------------------------------------------------
// Integration tests (require ANTHROPIC_API_KEY)
// ---------------------------------------------------------------------------

func TestMessages_Generate_Text(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := callGenerate(context.Background(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Reply with the single word: pong"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Message.Role)
	assert.NotEmpty(t, resp.Message.Content)
	assert.Equal(t, model.FinishReasonStop, resp.FinishReason)
	require.NotNil(t, resp.Usage)
	assert.Positive(t, resp.Usage.TotalTokens)
}

func TestMessages_Generate_WithSystemPrompt(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := callGenerate(context.Background(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "You are a helpful assistant. Keep answers very short."},
			{Role: model.RoleUser, Content: "What is 1+1?"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Message.Role)
	assert.NotEmpty(t, resp.Message.Content)
}

func TestMessages_Generate_WithTool(t *testing.T) {
	llm := newClientFromEnv(t)

	echo := builtin.NewEchoTool()
	tools := []tool.Tool{echo}

	messages := []model.Message{
		{Role: model.RoleUser, Content: "Please echo the message: hello world"},
	}

	var finalResp *model.LLMResponse
	for i := 0; i < 10; i++ {
		t.Logf("[turn %d] sending %d messages", i+1, len(messages))
		resp, err := callGenerate(context.Background(), llm, &model.LLMRequest{
			Model:    llm.Name(),
			Messages: messages,
			Tools:    tools,
		}, nil)
		require.NoError(t, err)
		require.NotNil(t, resp)

		messages = append(messages, resp.Message)

		if resp.FinishReason == model.FinishReasonStop {
			t.Logf("[turn %d] finish_reason=stop, content=%q", i+1, resp.Message.Content)
			finalResp = resp
			break
		}

		require.Equal(t, model.FinishReasonToolCalls, resp.FinishReason)
		require.NotEmpty(t, resp.Message.ToolCalls)

		for _, tc := range resp.Message.ToolCalls {
			t.Logf("[turn %d] tool_call: %s args=%s", i+1, tc.Name, tc.Arguments)
			result, err := echo.Run(context.Background(), tc.ID, tc.Arguments)
			require.NoError(t, err)
			t.Logf("[turn %d] tool_result: %s", i+1, result)
			messages = append(messages, model.Message{
				Role:       model.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	require.NotNil(t, finalResp, "model did not stop within max iterations")
	assert.Equal(t, model.RoleAssistant, finalResp.Message.Role)
	assert.Equal(t, model.FinishReasonStop, finalResp.FinishReason)
}

func TestMessages_Generate_WithConfig(t *testing.T) {
	llm := newClientFromEnv(t)

	cfg := &model.GenerateConfig{
		Temperature: 0.2,
	}

	resp, err := callGenerate(context.Background(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Say hi"},
		},
	}, cfg)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Message.Content)
}

// TestMessages_Generate_Thinking verifies that a thinking-capable model
// returns non-empty ReasoningContent when thinking is explicitly enabled.
// Required env vars: ANTHROPIC_API_KEY + ANTHROPIC_THINKING_MODEL
func TestMessages_Generate_Thinking(t *testing.T) {
	llm := newThinkingClientFromEnv(t)

	resp, err := callGenerate(context.Background(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "What is 12 * 13? Think step by step."},
		},
	}, &model.GenerateConfig{EnableThinking: boolPtr(true)})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Message.Role)
	assert.NotEmpty(t, resp.Message.Content)
	assert.NotEmpty(t, resp.Message.ReasoningContent, "expected thinking model to populate ReasoningContent")
	t.Logf("reasoning: %s", resp.Message.ReasoningContent)
	t.Logf("answer:    %s", resp.Message.Content)
}
