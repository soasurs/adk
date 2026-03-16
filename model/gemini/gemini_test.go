package gemini

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genai"

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
// Required: GEMINI_API_KEY — test is skipped when absent.
// Optional: GEMINI_MODEL — model name; defaults to "gemini-2.0-flash" when absent.
func newClientFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-2.0-flash"
	}
	llm, err := New(t.Context(), apiKey, modelName)
	require.NoError(t, err)
	return llm
}

// newVertexAIClientFromEnv creates a model.LLM backed by Vertex AI from environment variables.
// Required: VERTEX_AI_PROJECT + VERTEX_AI_LOCATION — test is skipped when either is absent.
// Optional: VERTEX_AI_MODEL — defaults to "gemini-2.0-flash" when absent.
// Authentication relies on Application Default Credentials (ADC).
func newVertexAIClientFromEnv(t *testing.T) model.LLM {
	t.Helper()
	project := os.Getenv("VERTEX_AI_PROJECT")
	if project == "" {
		t.Skip("VERTEX_AI_PROJECT not set")
	}
	location := os.Getenv("VERTEX_AI_LOCATION")
	if location == "" {
		t.Skip("VERTEX_AI_LOCATION not set")
	}
	modelName := os.Getenv("VERTEX_AI_MODEL")
	if modelName == "" {
		modelName = "gemini-2.0-flash"
	}
	llm, err := NewVertexAI(t.Context(), project, location, modelName)
	require.NoError(t, err)
	return llm
}

// newThinkingClientFromEnv creates a model.LLM for thinking model tests.
// Required: GEMINI_API_KEY + GEMINI_THINKING_MODEL — test is skipped when either is absent.
func newThinkingClientFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	modelName := os.Getenv("GEMINI_THINKING_MODEL")
	if modelName == "" {
		t.Skip("GEMINI_THINKING_MODEL not set")
	}
	llm, err := New(t.Context(), apiKey, modelName)
	require.NoError(t, err)
	return llm
}

// ---------------------------------------------------------------------------
// Unit tests (no network required)
// ---------------------------------------------------------------------------

func TestGenerateContent_Name(t *testing.T) {
	g := &GenerateContent{modelName: "gemini-2.0-flash"}
	assert.Equal(t, "gemini-2.0-flash", g.Name())
}

func TestConvertFinishReason(t *testing.T) {
	cases := []struct {
		input    genai.FinishReason
		expected model.FinishReason
	}{
		{genai.FinishReasonStop, model.FinishReasonStop},
		{genai.FinishReasonMaxTokens, model.FinishReasonLength},
		{genai.FinishReasonSafety, model.FinishReasonContentFilter},
		{genai.FinishReasonProhibitedContent, model.FinishReasonContentFilter},
		{genai.FinishReasonBlocklist, model.FinishReasonContentFilter},
		{genai.FinishReasonSPII, model.FinishReasonContentFilter},
		{genai.FinishReason("UNKNOWN"), model.FinishReasonStop},
		{"", model.FinishReasonStop},
	}
	for _, tc := range cases {
		t.Run(string(tc.input), func(t *testing.T) {
			assert.Equal(t, tc.expected, convertFinishReason(tc.input))
		})
	}
}

func TestMapReasoningEffort(t *testing.T) {
	cases := []struct {
		input    model.ReasoningEffort
		expected genai.ThinkingLevel
	}{
		{model.ReasoningEffortMinimal, genai.ThinkingLevelMinimal},
		{model.ReasoningEffortLow, genai.ThinkingLevelLow},
		{model.ReasoningEffortMedium, genai.ThinkingLevelMedium},
		{model.ReasoningEffortHigh, genai.ThinkingLevelHigh},
		{model.ReasoningEffortXhigh, genai.ThinkingLevelHigh},
		{model.ReasoningEffort("unknown"), ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.input), func(t *testing.T) {
			assert.Equal(t, tc.expected, mapReasoningEffort(tc.input))
		})
	}
}

func TestConvertMessages_System(t *testing.T) {
	contents, sysInstruction, err := convertMessages([]model.Message{
		{Role: model.RoleSystem, Content: "you are helpful"},
	})
	require.NoError(t, err)
	assert.Empty(t, contents)
	require.NotNil(t, sysInstruction)
	require.Len(t, sysInstruction.Parts, 1)
	assert.Equal(t, "you are helpful", sysInstruction.Parts[0].Text)
}

func TestConvertMessages_User(t *testing.T) {
	contents, sysInstruction, err := convertMessages([]model.Message{
		{Role: model.RoleUser, Content: "hello"},
	})
	require.NoError(t, err)
	assert.Nil(t, sysInstruction)
	require.Len(t, contents, 1)
	assert.Equal(t, "user", contents[0].Role)
	require.Len(t, contents[0].Parts, 1)
	assert.Equal(t, "hello", contents[0].Parts[0].Text)
}

func TestConvertMessages_Assistant_Text(t *testing.T) {
	contents, _, err := convertMessages([]model.Message{
		{Role: model.RoleAssistant, Content: "hi there"},
	})
	require.NoError(t, err)
	require.Len(t, contents, 1)
	assert.Equal(t, "model", contents[0].Role)
	require.Len(t, contents[0].Parts, 1)
	assert.Equal(t, "hi there", contents[0].Parts[0].Text)
}

func TestConvertMessages_Assistant_ToolCalls(t *testing.T) {
	contents, _, err := convertMessages([]model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: `{"echo":"hi"}`},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, contents, 1)
	assert.Equal(t, "model", contents[0].Role)
	require.Len(t, contents[0].Parts, 1)
	require.NotNil(t, contents[0].Parts[0].FunctionCall)
	assert.Equal(t, "call_1", contents[0].Parts[0].FunctionCall.ID)
	assert.Equal(t, "Echo", contents[0].Parts[0].FunctionCall.Name)
	assert.Equal(t, "hi", contents[0].Parts[0].FunctionCall.Args["echo"])
}

func TestConvertMessages_Tool(t *testing.T) {
	// The assistant message must appear first so the toolCallNames map is populated.
	contents, _, err := convertMessages([]model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: "{}"},
			},
		},
		{Role: model.RoleTool, Content: "pong", ToolCallID: "call_1"},
	})
	require.NoError(t, err)
	require.Len(t, contents, 2)
	// Second content is the batched tool response.
	toolContent := contents[1]
	assert.Equal(t, "user", toolContent.Role)
	require.Len(t, toolContent.Parts, 1)
	require.NotNil(t, toolContent.Parts[0].FunctionResponse)
	assert.Equal(t, "call_1", toolContent.Parts[0].FunctionResponse.ID)
	assert.Equal(t, "Echo", toolContent.Parts[0].FunctionResponse.Name)
	assert.Equal(t, "pong", toolContent.Parts[0].FunctionResponse.Response["output"])
}

func TestConvertMessages_ConsecutiveToolsBatched(t *testing.T) {
	contents, _, err := convertMessages([]model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "c1", Name: "Echo", Arguments: "{}"},
				{ID: "c2", Name: "Echo", Arguments: "{}"},
			},
		},
		{Role: model.RoleTool, Content: "r1", ToolCallID: "c1"},
		{Role: model.RoleTool, Content: "r2", ToolCallID: "c2"},
	})
	require.NoError(t, err)
	require.Len(t, contents, 2, "two consecutive tool messages should be batched into one content")
	assert.Equal(t, "user", contents[1].Role)
	assert.Len(t, contents[1].Parts, 2)
}

func TestConvertMessages_UnknownRole(t *testing.T) {
	_, _, err := convertMessages([]model.Message{{Role: "invalid"}})
	assert.Error(t, err)
}

func TestApplyConfig_Temperature(t *testing.T) {
	gCfg := &genai.GenerateContentConfig{}
	applyConfig(gCfg, &model.GenerateConfig{Temperature: 0.7})
	require.NotNil(t, gCfg.Temperature)
	assert.InDelta(t, float32(0.7), *gCfg.Temperature, 0.001)
}

func TestApplyConfig_ReasoningEffort(t *testing.T) {
	tests := []struct {
		name        string
		cfg         model.GenerateConfig
		wantLevel   genai.ThinkingLevel
		wantBudget  *int32
		wantInclude bool
	}{
		{
			name:       "none → budget=0",
			cfg:        model.GenerateConfig{ReasoningEffort: model.ReasoningEffortNone},
			wantBudget: func() *int32 { z := int32(0); return &z }(),
		},
		{
			name:        "high → HIGH + IncludeThoughts",
			cfg:         model.GenerateConfig{ReasoningEffort: model.ReasoningEffortHigh},
			wantLevel:   genai.ThinkingLevelHigh,
			wantInclude: true,
		},
		{
			name:        "medium → MEDIUM + IncludeThoughts",
			cfg:         model.GenerateConfig{ReasoningEffort: model.ReasoningEffortMedium},
			wantLevel:   genai.ThinkingLevelMedium,
			wantInclude: true,
		},
		{
			name:        "EnableThinking=true → IncludeThoughts",
			cfg:         model.GenerateConfig{EnableThinking: boolPtr(true)},
			wantInclude: true,
		},
		{
			name:       "EnableThinking=false → budget=0",
			cfg:        model.GenerateConfig{EnableThinking: boolPtr(false)},
			wantBudget: func() *int32 { z := int32(0); return &z }(),
		},
		{
			name: "nil EnableThinking + no effort → no ThinkingConfig",
			cfg:  model.GenerateConfig{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gCfg := &genai.GenerateContentConfig{}
			applyConfig(gCfg, &tt.cfg)
			if tt.wantBudget == nil && !tt.wantInclude && tt.wantLevel == "" {
				assert.Nil(t, gCfg.ThinkingConfig, "expected no ThinkingConfig")
				return
			}
			require.NotNil(t, gCfg.ThinkingConfig)
			if tt.wantBudget != nil {
				require.NotNil(t, gCfg.ThinkingConfig.ThinkingBudget)
				assert.Equal(t, *tt.wantBudget, *gCfg.ThinkingConfig.ThinkingBudget)
			}
			assert.Equal(t, tt.wantInclude, gCfg.ThinkingConfig.IncludeThoughts)
			assert.Equal(t, tt.wantLevel, gCfg.ThinkingConfig.ThinkingLevel)
		})
	}
}

func TestConvertTools_Empty(t *testing.T) {
	result, err := convertTools(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConvertTools_EchoTool(t *testing.T) {
	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)
	result, err := convertTools([]tool.Tool{echo})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0].FunctionDeclarations, 1)
	decl := result[0].FunctionDeclarations[0]
	assert.Equal(t, echo.Definition().Name, decl.Name)
	assert.Equal(t, echo.Definition().Description, decl.Description)
	assert.NotNil(t, decl.ParametersJsonSchema)
}

// ---------------------------------------------------------------------------
// Integration tests (require GEMINI_API_KEY)
// ---------------------------------------------------------------------------

func TestGenerateContent_Generate_Text(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
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

func TestGenerateContent_Generate_WithSystemPrompt(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
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

func TestGenerateContent_Generate_WithTool(t *testing.T) {
	llm := newClientFromEnv(t)

	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)
	tools := []tool.Tool{echo}

	messages := []model.Message{
		{Role: model.RoleUser, Content: "Please echo the message: hello world"},
	}

	var finalResp *model.LLMResponse
	for i := 0; i < 10; i++ {
		t.Logf("[turn %d] sending %d messages", i+1, len(messages))
		resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
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
			result, err := echo.Run(t.Context(), tc.ID, tc.Arguments)
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

func TestGenerateContent_Generate_WithConfig(t *testing.T) {
	llm := newClientFromEnv(t)

	cfg := &model.GenerateConfig{
		Temperature: 0.2,
	}

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Say hi"},
		},
	}, cfg)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Message.Content)
}

// TestGenerateContent_Generate_Thinking verifies that a thinking-capable model
// returns non-empty ReasoningContent when thinking is explicitly enabled.
// Required env vars: GEMINI_API_KEY + GEMINI_THINKING_MODEL
func TestGenerateContent_Generate_Thinking(t *testing.T) {
	llm := newThinkingClientFromEnv(t)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
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

// ---------------------------------------------------------------------------
// Vertex AI integration tests (require VERTEX_AI_PROJECT + VERTEX_AI_LOCATION)
// ---------------------------------------------------------------------------

func TestVertexAI_Generate_Text(t *testing.T) {
	llm := newVertexAIClientFromEnv(t)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
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

func TestVertexAI_Generate_WithTool(t *testing.T) {
	llm := newVertexAIClientFromEnv(t)

	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)
	tools := []tool.Tool{echo}

	messages := []model.Message{
		{Role: model.RoleUser, Content: "Please echo the message: hello vertex"},
	}

	var finalResp *model.LLMResponse
	for i := 0; i < 10; i++ {
		resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
			Model:    llm.Name(),
			Messages: messages,
			Tools:    tools,
		}, nil)
		require.NoError(t, err)
		require.NotNil(t, resp)

		messages = append(messages, resp.Message)

		if resp.FinishReason == model.FinishReasonStop {
			finalResp = resp
			break
		}

		require.Equal(t, model.FinishReasonToolCalls, resp.FinishReason)
		for _, tc := range resp.Message.ToolCalls {
			result, err := echo.Run(t.Context(), tc.ID, tc.Arguments)
			require.NoError(t, err)
			messages = append(messages, model.Message{
				Role:       model.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	require.NotNil(t, finalResp, "model did not stop within max iterations")
	assert.Equal(t, model.FinishReasonStop, finalResp.FinishReason)
}
