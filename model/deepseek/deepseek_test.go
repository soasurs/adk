package deepseek

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

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/retry"
)

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

func newClientFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	baseURL := os.Getenv("DEEPSEEK_BASE_URL")
	modelName := os.Getenv("DEEPSEEK_MODEL")
	if modelName == "" {
		modelName = ModelV4Flash
	}
	if baseURL != "" {
		return NewWithBaseURL(apiKey, baseURL, modelName)
	}
	return New(apiKey, modelName)
}

func newCaptureServer(t *testing.T, response string) (*httptest.Server, <-chan []byte) {
	t.Helper()
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		select {
		case bodyCh <- body:
		default:
			t.Errorf("unexpected extra request")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server, bodyCh
}

func readRequestBody(t *testing.T, bodyCh <-chan []byte) map[string]any {
	t.Helper()
	select {
	case body := <-bodyCh:
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for request body")
		return nil
	}
}

// Unit tests (no network required)
// ---------------------------------------------------------------------------

func TestChatCompletion_Name(t *testing.T) {
	llm := New("test-key", ModelV4Pro)
	assert.Equal(t, ModelV4Pro, llm.Name())
}

func TestChatCompletion_Generate_DeepSeekRequestShape(t *testing.T) {
	server, bodyCh := newCaptureServer(t, `{
		"id": "chatcmpl_test",
		"object": "chat.completion",
		"created": 0,
		"model": "deepseek-v4-pro",
		"choices": [
			{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "ok",
					"reasoning_content": "server reasoning"
				},
				"finish_reason": "stop"
			}
		],
		"usage": {
			"prompt_tokens": 1,
			"completion_tokens": 1,
			"total_tokens": 2
		}
	}`)

	llm := NewWithBaseURLOptions(
		"test-key",
		server.URL,
		ModelV4Pro,
		WithRetryConfig(retry.Config{MaxAttempts: 1}),
		WithThinkingEnabled(false),
	)
	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{
				Role:             model.RoleAssistant,
				Content:          "I should use a tool.",
				ReasoningContent: "need current data",
				ToolCalls: []model.ToolCall{
					{ID: "call_1", Name: "lookup", Arguments: `{"query":"weather"}`},
				},
			},
			{Role: model.RoleTool, ToolCallID: "call_1", Content: "sunny"},
			{Role: model.RoleUser, Content: "final answer please"},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ok", resp.Content.Content)
	assert.Equal(t, "server reasoning", resp.Content.ReasoningContent)

	payload := readRequestBody(t, bodyCh)
	assert.Equal(t, ModelV4Pro, payload["model"])
	assert.NotContains(t, payload, "enable_thinking")
	assert.NotContains(t, payload, "reasoning_effort")

	thinking, ok := payload["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "disabled", thinking["type"])

	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 3)
	assistant, ok := messages[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "assistant", assistant["role"])
	assert.Equal(t, "need current data", assistant["reasoning_content"])
	require.Len(t, assistant["tool_calls"], 1)
}

// Integration tests (require DEEPSEEK_API_KEY)
// ---------------------------------------------------------------------------

func TestChatCompletion_Generate_Text(t *testing.T) {
	llm := newClientFromEnv(t)

	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "Reply with the single word: pong"},
		},
	}, &model.GenerateConfig{MaxTokens: 16})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, model.RoleAssistant, resp.Content.Role)
	assert.NotEmpty(t, resp.Content.Content)
	assert.Equal(t, model.FinishReasonStop, resp.FinishReason)
}
