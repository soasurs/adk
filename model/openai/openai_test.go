package openai

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/tool"
	"soasurs.dev/soasurs/adk/tool/builtin"
)

// newClientFromEnv creates a ChatCompletion from environment variables.
// Required: OPENAI_API_KEY — test is skipped when absent.
// Optional: OPENAI_BASE_URL — overrides the default OpenAI endpoint.
// Optional: OPENAI_MODEL   — model name; defaults to "gpt-4o-mini" when absent.
func newClientFromEnv(t *testing.T) *ChatCompletion {
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
	return New(apiKey, baseURL, modelName)
}

// ---------------------------------------------------------------------------
// Unit tests (no network required)
// ---------------------------------------------------------------------------

func TestChatCompletion_Name(t *testing.T) {
	c := &ChatCompletion{modelName: "gpt-4o"}
	assert.Equal(t, "gpt-4o", c.Name())
}

func TestConvertFinishReason(t *testing.T) {
	cases := []struct {
		input    string
		expected model.FinishReason
	}{
		{"stop", model.FinishReasonStop},
		{"tool_calls", model.FinishReasonToolCalls},
		{"length", model.FinishReasonLength},
		{"content_filter", model.FinishReasonContentFilter},
		{"unknown", model.FinishReasonStop},
		{"", model.FinishReasonStop},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, convertFinishReason(tc.input))
		})
	}
}

func TestConvertMessage_System(t *testing.T) {
	p, err := convertMessage(model.Message{Role: model.RoleSystem, Content: "you are helpful"})
	require.NoError(t, err)
	require.NotNil(t, p.OfSystem)
	assert.True(t, p.OfSystem.Content.OfString.Valid())
}

func TestConvertMessage_User(t *testing.T) {
	p, err := convertMessage(model.Message{Role: model.RoleUser, Content: "hello"})
	require.NoError(t, err)
	require.NotNil(t, p.OfUser)
	assert.True(t, p.OfUser.Content.OfString.Valid())
}

func TestConvertMessage_Assistant_Text(t *testing.T) {
	p, err := convertMessage(model.Message{Role: model.RoleAssistant, Content: "hi there"})
	require.NoError(t, err)
	require.NotNil(t, p.OfAssistant)
	assert.True(t, p.OfAssistant.Content.OfString.Valid())
	assert.Empty(t, p.OfAssistant.ToolCalls)
}

func TestConvertMessage_Assistant_ToolCalls(t *testing.T) {
	p, err := convertMessage(model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "call_1", Name: "Echo", Arguments: `{"echo":"hi"}`},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, p.OfAssistant)
	require.Len(t, p.OfAssistant.ToolCalls, 1)
	require.NotNil(t, p.OfAssistant.ToolCalls[0].OfFunction)
	assert.Equal(t, "call_1", p.OfAssistant.ToolCalls[0].OfFunction.ID)
	assert.Equal(t, "Echo", p.OfAssistant.ToolCalls[0].OfFunction.Function.Name)
}

func TestConvertMessage_Tool(t *testing.T) {
	p, err := convertMessage(model.Message{Role: model.RoleTool, Content: "result", ToolCallID: "call_1"})
	require.NoError(t, err)
	require.NotNil(t, p.OfTool)
	assert.Equal(t, "call_1", p.OfTool.ToolCallID)
}

func TestConvertMessage_UnknownRole(t *testing.T) {
	_, err := convertMessage(model.Message{Role: "invalid"})
	assert.Error(t, err)
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
	require.NotNil(t, result[0].OfFunction)
	assert.Equal(t, echo.Definition().Name, result[0].OfFunction.Function.Name)
	assert.True(t, result[0].OfFunction.Function.Description.Valid())
	assert.NotEmpty(t, result[0].OfFunction.Function.Parameters)
}

// ---------------------------------------------------------------------------
// Integration tests (require OPENAI_API_KEY)
// ---------------------------------------------------------------------------

func TestChatCompletion_Generate_Text(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := llm.Generate(context.Background(), &model.LLMRequest{
		Model: llm.modelName,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Reply with the single word: pong"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Message.Role)
	assert.NotEmpty(t, resp.Message.Content)
	assert.Equal(t, model.FinishReasonStop, resp.FinishReason)
}

func TestChatCompletion_Generate_WithSystemPrompt(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := llm.Generate(context.Background(), &model.LLMRequest{
		Model: llm.modelName,
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

func TestChatCompletion_Generate_WithTool(t *testing.T) {
	llm := newClientFromEnv(t)

	echo := builtin.NewEchoTool()
	tools := []tool.Tool{echo}

	messages := []model.Message{
		{Role: model.RoleUser, Content: "Please echo the message: hello world"},
	}

	var finalResp *model.LLMResponse
	for i := 0; i < 10; i++ {
		t.Logf("[turn %d] sending %d messages", i+1, len(messages))
		resp, err := llm.Generate(context.Background(), &model.LLMRequest{
			Model:    llm.modelName,
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

func TestChatCompletion_Generate_WithConfig(t *testing.T) {
	llm := newClientFromEnv(t)

	cfg := &model.GenerateConfig{
		Temperature: 0.2,
		TopP:        0.9,
	}

	resp, err := llm.Generate(context.Background(), &model.LLMRequest{
		Model: llm.modelName,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Say hi"},
		},
	}, cfg)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Message.Content)
}
