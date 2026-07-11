package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genai"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/retry"
	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/builtin"
)

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
// Optional: GEMINI_BASE_URL — overrides the default Gemini endpoint.
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
	opts := optionsFromBaseURL(os.Getenv("GEMINI_BASE_URL"))
	llm, err := NewWithOptions(t.Context(), apiKey, modelName, opts...)
	require.NoError(t, err)
	return llm
}

// newVertexAIClientFromEnv creates a model.LLM backed by Vertex AI from environment variables.
// Required: VERTEX_AI_PROJECT + VERTEX_AI_LOCATION — test is skipped when either is absent.
// Optional: VERTEX_AI_BASE_URL — overrides the default Vertex AI endpoint.
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
	opts := optionsFromBaseURL(os.Getenv("VERTEX_AI_BASE_URL"))
	llm, err := NewVertexAIWithOptions(t.Context(), project, location, modelName, opts...)
	require.NoError(t, err)
	return llm
}

// newThinkingClientFromEnv creates a model.LLM for thinking model tests.
// Required: GEMINI_API_KEY + GEMINI_THINKING_MODEL — test is skipped when either is absent.
// Optional: GEMINI_BASE_URL — overrides the default Gemini endpoint.
func newThinkingClientFromEnv(t *testing.T, opts ...Option) model.LLM {
	t.Helper()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	modelName := os.Getenv("GEMINI_THINKING_MODEL")
	if modelName == "" {
		t.Skip("GEMINI_THINKING_MODEL not set")
	}
	opts = append(optionsFromBaseURL(os.Getenv("GEMINI_BASE_URL")), opts...)
	llm, err := NewWithOptions(t.Context(), apiKey, modelName, opts...)
	require.NoError(t, err)
	return llm
}

func optionsFromBaseURL(baseURL string) []Option {
	if baseURL == "" {
		return nil
	}
	return []Option{WithBaseURL(baseURL)}
}

type capturedRequest struct {
	method string
	path   string
	body   []byte
}

func newCaptureServer(t *testing.T, response string) (*httptest.Server, <-chan capturedRequest) {
	t.Helper()
	reqCh := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		select {
		case reqCh <- capturedRequest{method: r.Method, path: r.URL.Path, body: body}:
		default:
			t.Errorf("unexpected extra request")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server, reqCh
}

func readCapturedRequest(t *testing.T, reqCh <-chan capturedRequest) capturedRequest {
	t.Helper()
	select {
	case req := <-reqCh:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for request")
		return capturedRequest{}
	}
}

// ---------------------------------------------------------------------------
// Unit tests (no network required)
// ---------------------------------------------------------------------------

func TestGenerateContent_Name(t *testing.T) {
	g := &GenerateContent{modelName: "gemini-2.0-flash"}
	assert.Equal(t, "gemini-2.0-flash", g.Name())
}

func TestNewVertexAI_DefaultRetryConfig(t *testing.T) {
	llm, err := NewVertexAI(t.Context(), "test-project", "us-central1", "gemini-2.0-flash")
	if err != nil {
		t.Skipf("NewVertexAI requires local ADC setup in this environment: %v", err)
	}
	require.NotNil(t, llm)
	assert.Equal(t, retry.DefaultConfig(), llm.retryCfg)
}

func TestGenerateContent_WithBaseURL(t *testing.T) {
	server, reqCh := newCaptureServer(t, `{
		"candidates": [
			{
				"content": {
					"role": "model",
					"parts": [{"text": "ok"}]
				},
				"finishReason": "STOP"
			}
		],
		"usageMetadata": {
			"promptTokenCount": 1,
			"candidatesTokenCount": 1,
			"totalTokenCount": 2
		}
	}`)

	llm, err := NewWithOptions(
		t.Context(),
		"test-key",
		"gemini-test",
		WithBaseURL(server.URL),
		WithRetryConfig(retry.Config{MaxAttempts: 1}),
	)
	require.NoError(t, err)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "hello"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ok", resp.Content.Content)

	req := readCapturedRequest(t, reqCh)
	assert.Equal(t, http.MethodPost, req.method)
	assert.Equal(t, "/v1beta/models/gemini-test:generateContent", req.path)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(req.body, &payload))
	contents, ok := payload["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 1)
}

func TestTokenUsageFromGemini_Details(t *testing.T) {
	usage := tokenUsageFromGemini(&genai.GenerateContentResponseUsageMetadata{
		CachedContentTokenCount: 3,
		CandidatesTokenCount:    4,
		PromptTokenCount:        10,
		ThoughtsTokenCount:      5,
		ToolUsePromptTokenCount: 2,
		TotalTokenCount:         21,
		PromptTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityAudio, TokenCount: 6},
			{Modality: genai.MediaModalityText, TokenCount: 7},
		},
		CandidatesTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityAudio, TokenCount: 8},
		},
	})

	require.NotNil(t, usage)
	assert.Equal(t, int64(10), usage.PromptTokens)
	assert.Equal(t, int64(4), usage.CompletionTokens)
	assert.Equal(t, int64(21), usage.TotalTokens)
	require.NotNil(t, usage.Details)
	assert.Equal(t, int64(3), usage.Details.CachedPromptTokens)
	assert.Equal(t, int64(5), usage.Details.ReasoningTokens)
	assert.Equal(t, int64(2), usage.Details.ToolUsePromptTokens)
	assert.Equal(t, int64(6), usage.Details.AudioPromptTokens)
	assert.Equal(t, int64(8), usage.Details.AudioCompletionTokens)

	assert.Nil(t, tokenUsageFromGemini(nil))
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

func TestMapThinkingLevel(t *testing.T) {
	cases := []struct {
		input    ThinkingLevel
		expected genai.ThinkingLevel
	}{
		{ThinkingLevelMinimal, genai.ThinkingLevelMinimal},
		{ThinkingLevelLow, genai.ThinkingLevelLow},
		{ThinkingLevelMedium, genai.ThinkingLevelMedium},
		{ThinkingLevelHigh, genai.ThinkingLevelHigh},
		{ThinkingLevel("unknown"), ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.input), func(t *testing.T) {
			assert.Equal(t, tc.expected, mapThinkingLevel(tc.input))
		})
	}
}

func TestConvertMessages_System(t *testing.T) {
	contents, sysInstruction, err := convertMessages([]model.Content{
		{Role: model.RoleSystem, Content: "you are helpful"},
	})
	require.NoError(t, err)
	assert.Empty(t, contents)
	require.NotNil(t, sysInstruction)
	require.Len(t, sysInstruction.Parts, 1)
	assert.Equal(t, "you are helpful", sysInstruction.Parts[0].Text)
}

func TestConvertMessages_User(t *testing.T) {
	contents, sysInstruction, err := convertMessages([]model.Content{
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
	contents, _, err := convertMessages([]model.Content{
		{Role: model.RoleAssistant, Content: "hi there"},
	})
	require.NoError(t, err)
	require.Len(t, contents, 1)
	assert.Equal(t, "model", contents[0].Role)
	require.Len(t, contents[0].Parts, 1)
	assert.Equal(t, "hi there", contents[0].Parts[0].Text)
}

func TestConvertMessages_Assistant_ToolCalls(t *testing.T) {
	contents, _, err := convertMessages([]model.Content{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: json.RawMessage(`{"echo":"hi"}`)},
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
	contents, _, err := convertMessages([]model.Content{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: json.RawMessage("{}")},
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

func TestConvertMessages_Tool_StructuredResult(t *testing.T) {
	contents, _, err := convertMessages([]model.Content{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Weather", Arguments: json.RawMessage("{}")},
			},
		},
		{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ToolCallID: "call_1",
				Name:       "Weather",
				Outcome: &tool.Result{
					StructuredContent: json.RawMessage(`{"temperature":23,"condition":"clear"}`),
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, contents, 2)
	response := contents[1].Parts[0].FunctionResponse
	require.NotNil(t, response)
	assert.Equal(t, "call_1", response.ID)
	assert.Equal(t, "Weather", response.Name)
	assert.Equal(t, float64(23), response.Response["temperature"])
	assert.Equal(t, "clear", response.Response["condition"])
}

func TestConvertMessages_Tool_StructuredErrorResult(t *testing.T) {
	contents, _, err := convertMessages([]model.Content{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Weather", Arguments: json.RawMessage("{}")},
			},
		},
		{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ToolCallID: "call_1",
				Name:       "Weather",
				Outcome: &tool.HandledError{
					Content:           "weather service unavailable",
					StructuredContent: json.RawMessage(`{"code":"unavailable","retry_after":30}`),
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, contents, 2)
	response := contents[1].Parts[0].FunctionResponse
	require.NotNil(t, response)
	assert.Equal(t, "weather service unavailable", response.Response["error"])
	details, ok := response.Response["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "unavailable", details["code"])
	assert.Equal(t, float64(30), details["retry_after"])
}

func TestConvertMessages_ConsecutiveToolsBatched(t *testing.T) {
	contents, _, err := convertMessages([]model.Content{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "c1", Name: "Echo", Arguments: json.RawMessage("{}")},
				{ID: "c2", Name: "Echo", Arguments: json.RawMessage("{}")},
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
	_, _, err := convertMessages([]model.Content{{Role: "invalid"}})
	assert.Error(t, err)
}

func TestApplyConfig_Temperature(t *testing.T) {
	gCfg := &genai.GenerateContentConfig{}
	applyConfig(gCfg, &model.GenerateConfig{Temperature: 0.7}, generationOptions{})
	require.NotNil(t, gCfg.Temperature)
	assert.InDelta(t, float32(0.7), *gCfg.Temperature, 0.001)
}

func TestApplyConfig_ThinkingOptions(t *testing.T) {
	tests := []struct {
		name        string
		generation  generationOptions
		wantLevel   genai.ThinkingLevel
		wantBudget  *int32
		wantInclude bool
	}{
		{
			name:        "thinking level high → HIGH + IncludeThoughts",
			generation:  generationOptions{thinkingLevel: ThinkingLevelHigh},
			wantLevel:   genai.ThinkingLevelHigh,
			wantInclude: true,
		},
		{
			name:        "thinking level wins over boolean toggle",
			generation:  generationOptions{thinkingLevel: ThinkingLevelLow, enableThinking: new(false)},
			wantLevel:   genai.ThinkingLevelLow,
			wantInclude: true,
		},
		{
			name:        "EnableThinking=true → IncludeThoughts",
			generation:  generationOptions{enableThinking: new(true)},
			wantInclude: true,
		},
		{
			name:       "EnableThinking=false → budget=0",
			generation: generationOptions{enableThinking: new(false)},
			wantBudget: func() *int32 { z := int32(0); return &z }(),
		},
		{
			name: "nil EnableThinking + no effort → no ThinkingConfig",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gCfg := &genai.GenerateContentConfig{}
			applyConfig(gCfg, nil, tt.generation)
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
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "Reply with the single word: pong"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Content.Role)
	assert.NotEmpty(t, resp.Content.Content)
	assert.Equal(t, model.FinishReasonStop, resp.FinishReason)
	require.NotNil(t, resp.Usage)
	assert.Positive(t, resp.Usage.TotalTokens)
}

func TestGenerateContent_Generate_WithSystemPrompt(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleSystem, Content: "You are a helpful assistant. Keep answers very short."},
			{Role: model.RoleUser, Content: "What is 1+1?"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Content.Role)
	assert.NotEmpty(t, resp.Content.Content)
}

func TestGenerateContent_Generate_WithTool(t *testing.T) {
	llm := newClientFromEnv(t)

	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)
	tools := []tool.Tool{echo}

	messages := []model.Content{
		{Role: model.RoleUser, Content: "Please echo the message: hello world"},
	}

	var finalResp *model.LLMResponse
	for i := range 10 {
		t.Logf("[turn %d] sending %d messages", i+1, len(messages))
		resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
			Model:    llm.Name(),
			Contents: messages,
			Tools:    tools,
		}, nil)
		require.NoError(t, err)
		require.NotNil(t, resp)

		messages = append(messages, resp.Content)

		if resp.FinishReason == model.FinishReasonStop {
			t.Logf("[turn %d] finish_reason=stop, content=%q", i+1, resp.Content.Content)
			finalResp = resp
			break
		}

		require.Equal(t, model.FinishReasonToolCalls, resp.FinishReason)
		require.NotEmpty(t, resp.Content.ToolCalls)

		for _, tc := range resp.Content.ToolCalls {
			t.Logf("[turn %d] tool_call: %s args=%s", i+1, tc.Name, tc.Arguments)
			result, err := echo.Run(t.Context(), tool.Call{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
			require.NoError(t, err)
			t.Logf("[turn %d] tool_result: %s", i+1, result.Content)
			messages = append(messages, model.Content{
				Role:       model.RoleTool,
				Content:    result.Content,
				ToolCallID: tc.ID,
				ToolResponse: &model.ToolResponse{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Outcome:    result,
				},
			})
		}
	}

	require.NotNil(t, finalResp, "model did not stop within max iterations")
	assert.Equal(t, model.RoleAssistant, finalResp.Content.Role)
	assert.Equal(t, model.FinishReasonStop, finalResp.FinishReason)
}

func TestGenerateContent_Generate_WithConfig(t *testing.T) {
	llm := newClientFromEnv(t)

	cfg := &model.GenerateConfig{
		Temperature: 0.2,
	}

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "Say hi"},
		},
	}, cfg)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content.Content)
}

// TestGenerateContent_Generate_Thinking verifies that a thinking-capable model
// returns non-empty ReasoningContent when thinking is explicitly enabled.
// Required env vars: GEMINI_API_KEY + GEMINI_THINKING_MODEL
func TestGenerateContent_Generate_Thinking(t *testing.T) {
	llm := newThinkingClientFromEnv(t, WithThinkingEnabled(true))

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "What is 12 * 13? Think step by step."},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Content.Role)
	assert.NotEmpty(t, resp.Content.Content)
	assert.NotEmpty(t, resp.Content.ReasoningContent, "expected thinking model to populate ReasoningContent")
	t.Logf("reasoning: %s", resp.Content.ReasoningContent)
	t.Logf("answer:    %s", resp.Content.Content)
}

// ---------------------------------------------------------------------------
// Vertex AI integration tests (require VERTEX_AI_PROJECT + VERTEX_AI_LOCATION)
// ---------------------------------------------------------------------------

func TestVertexAI_Generate_Text(t *testing.T) {
	llm := newVertexAIClientFromEnv(t)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "Reply with the single word: pong"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Content.Role)
	assert.NotEmpty(t, resp.Content.Content)
	assert.Equal(t, model.FinishReasonStop, resp.FinishReason)
	require.NotNil(t, resp.Usage)
	assert.Positive(t, resp.Usage.TotalTokens)
}

func TestVertexAI_Generate_WithTool(t *testing.T) {
	llm := newVertexAIClientFromEnv(t)

	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)
	tools := []tool.Tool{echo}

	messages := []model.Content{
		{Role: model.RoleUser, Content: "Please echo the message: hello vertex"},
	}

	var finalResp *model.LLMResponse
	for range 10 {
		resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
			Model:    llm.Name(),
			Contents: messages,
			Tools:    tools,
		}, nil)
		require.NoError(t, err)
		require.NotNil(t, resp)

		messages = append(messages, resp.Content)

		if resp.FinishReason == model.FinishReasonStop {
			finalResp = resp
			break
		}

		require.Equal(t, model.FinishReasonToolCalls, resp.FinishReason)
		for _, tc := range resp.Content.ToolCalls {
			result, err := echo.Run(t.Context(), tool.Call{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
			require.NoError(t, err)
			messages = append(messages, model.Content{
				Role:       model.RoleTool,
				Content:    result.Content,
				ToolCallID: tc.ID,
				ToolResponse: &model.ToolResponse{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Outcome:    result,
				},
			})
		}
	}

	require.NotNil(t, finalResp, "model did not stop within max iterations")
	assert.Equal(t, model.FinishReasonStop, finalResp.FinishReason)
}
