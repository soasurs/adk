package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	goanthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
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
// Required: ANTHROPIC_API_KEY — test is skipped when absent.
// Optional: ANTHROPIC_BASE_URL — overrides the default Anthropic endpoint.
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
	return NewWithOptions(apiKey, modelName, optionsFromBaseURL(os.Getenv("ANTHROPIC_BASE_URL"))...)
}

// newThinkingClientFromEnv creates a model.LLM for thinking model tests.
// Required: ANTHROPIC_API_KEY + ANTHROPIC_THINKING_MODEL — test is skipped when either is absent.
// Optional: ANTHROPIC_BASE_URL — overrides the default Anthropic endpoint.
func newThinkingClientFromEnv(t *testing.T, opts ...Option) model.LLM {
	t.Helper()
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	modelName := os.Getenv("ANTHROPIC_THINKING_MODEL")
	if modelName == "" {
		t.Skip("ANTHROPIC_THINKING_MODEL not set")
	}
	opts = append(optionsFromBaseURL(os.Getenv("ANTHROPIC_BASE_URL")), opts...)
	return NewWithOptions(apiKey, modelName, opts...)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// ---------------------------------------------------------------------------
// Unit tests (no network required)
// ---------------------------------------------------------------------------

func TestMessages_Name(t *testing.T) {
	m := &Model{modelName: "claude-haiku-4-5"}
	assert.Equal(t, "claude-haiku-4-5", m.Name())
}

func TestMessages_WithBaseURL(t *testing.T) {
	server, reqCh := newCaptureServer(t, `{
		"id": "msg_test",
		"type": "message",
		"role": "assistant",
		"model": "claude-test",
		"content": [{"type": "text", "text": "ok"}],
		"stop_reason": "end_turn",
		"stop_sequence": null,
		"usage": {
			"input_tokens": 1,
			"output_tokens": 1
		}
	}`)

	llm := NewWithOptions("test-key", "claude-test", WithBaseURL(server.URL))
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
	assert.Equal(t, "/v1/messages", req.path)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(req.body, &payload))
	assert.Equal(t, "claude-test", payload["model"])
	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
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

func TestTokenUsageFromAnthropic_IncludesCacheTokens(t *testing.T) {
	usage := tokenUsageFromAnthropic(goanthropic.Usage{
		InputTokens:              10,
		CacheCreationInputTokens: 2,
		CacheReadInputTokens:     3,
		OutputTokens:             4,
	}, true)

	require.NotNil(t, usage)
	assert.Equal(t, int64(15), usage.PromptTokens)
	assert.Equal(t, int64(4), usage.CompletionTokens)
	assert.Equal(t, int64(19), usage.TotalTokens)
	require.NotNil(t, usage.Details)
	assert.Equal(t, int64(3), usage.Details.CachedPromptTokens)
	assert.Equal(t, int64(2), usage.Details.CacheCreationPromptTokens)
	assert.Equal(t, int64(3), usage.Details.CacheReadPromptTokens)
	assert.Nil(t, tokenUsageFromAnthropic(goanthropic.Usage{InputTokens: 10}, false))
}

func TestTokenUsageFromAnthropicDelta_IncludesCacheTokens(t *testing.T) {
	usage := tokenUsageFromAnthropicDelta(goanthropic.MessageDeltaUsage{
		InputTokens:              10,
		CacheCreationInputTokens: 2,
		CacheReadInputTokens:     3,
		OutputTokens:             4,
	}, true)

	require.NotNil(t, usage)
	assert.Equal(t, int64(15), usage.PromptTokens)
	assert.Equal(t, int64(4), usage.CompletionTokens)
	assert.Equal(t, int64(19), usage.TotalTokens)
	require.NotNil(t, usage.Details)
	assert.Equal(t, int64(3), usage.Details.CachedPromptTokens)
	assert.Equal(t, int64(2), usage.Details.CacheCreationPromptTokens)
	assert.Equal(t, int64(3), usage.Details.CacheReadPromptTokens)
	assert.Nil(t, tokenUsageFromAnthropicDelta(goanthropic.MessageDeltaUsage{InputTokens: 10}, false))
}

func TestMergeAnthropicDeltaUsage_PreservesPromptTokens(t *testing.T) {
	usage := mergeAnthropicDeltaUsage(&model.TokenUsage{
		PromptTokens:     15,
		CompletionTokens: 1,
		TotalTokens:      16,
		Details: &model.TokenUsageDetails{
			CachedPromptTokens:        3,
			CacheCreationPromptTokens: 2,
			CacheReadPromptTokens:     3,
		},
	}, goanthropic.MessageDeltaUsage{
		OutputTokens: 4,
	}, true)

	require.NotNil(t, usage)
	assert.Equal(t, int64(15), usage.PromptTokens)
	assert.Equal(t, int64(4), usage.CompletionTokens)
	assert.Equal(t, int64(19), usage.TotalTokens)
	require.NotNil(t, usage.Details)
	assert.Equal(t, int64(3), usage.Details.CachedPromptTokens)
	assert.Equal(t, int64(2), usage.Details.CacheCreationPromptTokens)
	assert.Equal(t, int64(3), usage.Details.CacheReadPromptTokens)
}

func TestMessages_Stream_Usage(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/v1/messages", r.URL.Path)

			body := strings.Join([]string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":1}}}`,
				``,
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
				``,
				`event: content_block_stop`,
				`data: {"type":"content_block_stop","index":0}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
			}, "\n")

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
	m := &Model{
		client:    goanthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(httpClient)),
		modelName: "claude-test",
	}

	var finalResp *model.LLMResponse
	for resp, err := range m.callAPIStreaming(t.Context(), goanthropic.MessageNewParams{
		Model:     goanthropic.Model("claude-test"),
		MaxTokens: 128,
		Messages: []goanthropic.MessageParam{
			{
				Role: goanthropic.MessageParamRoleUser,
				Content: []goanthropic.ContentBlockParamUnion{
					{OfText: &goanthropic.TextBlockParam{Text: "ping"}},
				},
			},
		},
	}) {
		require.NoError(t, err)
		if !resp.Partial {
			finalResp = resp
		}
	}

	require.NotNil(t, finalResp)
	require.NotNil(t, finalResp.Usage)
	assert.Equal(t, "pong", finalResp.Content.Content)
	assert.Equal(t, int64(15), finalResp.Usage.PromptTokens)
	assert.Equal(t, int64(4), finalResp.Usage.CompletionTokens)
	assert.Equal(t, int64(19), finalResp.Usage.TotalTokens)
	require.NotNil(t, finalResp.Usage.Details)
	assert.Equal(t, int64(3), finalResp.Usage.Details.CachedPromptTokens)
	assert.Equal(t, int64(2), finalResp.Usage.Details.CacheCreationPromptTokens)
	assert.Equal(t, int64(3), finalResp.Usage.Details.CacheReadPromptTokens)
}

func TestConvertMessages_System(t *testing.T) {
	messages, system, err := convertMessages([]model.Content{
		{Role: model.RoleSystem, Content: "you are helpful"},
	})
	require.NoError(t, err)
	assert.Empty(t, messages)
	require.Len(t, system, 1)
	assert.Equal(t, "you are helpful", system[0].Text)
}

func TestConvertMessages_User(t *testing.T) {
	messages, system, err := convertMessages([]model.Content{
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
	messages, _, err := convertMessages([]model.Content{
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
	messages, _, err := convertMessages([]model.Content{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: json.RawMessage(`{"echo":"hi"}`)},
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
	messages, _, err := convertMessages([]model.Content{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: json.RawMessage("{}")},
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
	messages, _, err := convertMessages([]model.Content{
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
	require.Len(t, messages, 2, "two consecutive tool messages should be batched into one user message")
	assert.Equal(t, goanthropic.MessageParamRoleUser, messages[1].Role)
	assert.Len(t, messages[1].Content, 2)
}

func TestConvertMessages_UnknownRole(t *testing.T) {
	_, _, err := convertMessages([]model.Content{{Role: "invalid"}})
	assert.Error(t, err)
}

func TestApplyConfig_Temperature(t *testing.T) {
	p := &goanthropic.MessageNewParams{}
	applyConfig(p, &model.GenerateConfig{Temperature: 0.7}, generationOptions{})
	assert.True(t, p.Temperature.Valid())
	assert.InDelta(t, 0.7, p.Temperature.Value, 0.001)
}

func TestApplyConfig_EnableThinking(t *testing.T) {
	tests := []struct {
		name           string
		generation     generationOptions
		wantEnabled    bool
		wantDisabled   bool
		wantNoThinking bool
		wantBudget     int64
	}{
		{
			name:        "EnableThinking=true → OfEnabled set",
			generation:  generationOptions{enableThinking: new(true)},
			wantEnabled: true,
			wantBudget:  defaultThinkingBudget,
		},
		{
			name:        "ThinkingBudget>0 enables thinking without EnableThinking",
			generation:  generationOptions{thinkingBudget: 4096},
			wantEnabled: true,
			wantBudget:  4096,
		},
		{
			name:         "EnableThinking=false → OfDisabled set",
			generation:   generationOptions{enableThinking: new(false)},
			wantDisabled: true,
		},
		{
			name:           "nil EnableThinking → no ThinkingConfig",
			generation:     generationOptions{},
			wantNoThinking: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &goanthropic.MessageNewParams{}
			applyConfig(p, nil, tt.generation)
			if tt.wantNoThinking {
				assert.Nil(t, p.Thinking.OfEnabled)
				assert.Nil(t, p.Thinking.OfDisabled)
				return
			}
			if tt.wantEnabled {
				require.NotNil(t, p.Thinking.OfEnabled)
				assert.Equal(t, tt.wantBudget, p.Thinking.OfEnabled.BudgetTokens)
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
	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)
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

func TestMessages_Generate_WithSystemPrompt(t *testing.T) {
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

func TestMessages_Generate_WithTool(t *testing.T) {
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

func TestMessages_Generate_WithConfig(t *testing.T) {
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

// TestMessages_Generate_Thinking verifies that a thinking-capable model
// returns non-empty ReasoningContent when thinking is explicitly enabled.
// Required env vars: ANTHROPIC_API_KEY + ANTHROPIC_THINKING_MODEL
func TestMessages_Generate_Thinking(t *testing.T) {
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

// TestMessages_Stream_Thinking verifies that streaming mode yields partial
// thinking deltas before the complete response with full ReasoningContent.
// Required env vars: ANTHROPIC_API_KEY + ANTHROPIC_THINKING_MODEL
func TestMessages_Stream_Thinking(t *testing.T) {
	llm := newThinkingClientFromEnv(t, WithThinkingEnabled(true))

	var partialReasoningBuf strings.Builder
	var finalResp *model.LLMResponse

	for resp, err := range llm.GenerateContent(t.Context(), &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "What is 9 * 8? Think step by step."},
		},
	}, nil, true) {
		require.NoError(t, err)
		if resp.Partial {
			if resp.Content.ReasoningContent != "" {
				partialReasoningBuf.WriteString(resp.Content.ReasoningContent)
				t.Logf("partial reasoning: %s", resp.Content.ReasoningContent)
			}
		} else {
			finalResp = resp
		}
	}

	require.NotNil(t, finalResp)
	assert.Equal(t, model.RoleAssistant, finalResp.Content.Role)
	assert.NotEmpty(t, finalResp.Content.Content)
	assert.NotEmpty(t, finalResp.Content.ReasoningContent, "expected final response to include full ReasoningContent")
	assert.GreaterOrEqual(t, len(finalResp.Content.ReasoningContent), partialReasoningBuf.Len(),
		"final ReasoningContent should be at least as long as accumulated partials")
	t.Logf("\nfinal reasoning: %s", finalResp.Content.ReasoningContent)
	t.Logf("final answer:    %s", finalResp.Content.Content)
}
