package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	goopenai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	goresponses "github.com/openai/openai-go/v3/responses"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/retry"
	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/builtin"
)

func TestResponses_Name(t *testing.T) {
	r := &Responses{modelName: "gpt-5-mini"}
	assert.Equal(t, "gpt-5-mini", r.Name())
}

func TestConvertResponsesInput_HistoryWithToolCall(t *testing.T) {
	input, err := convertResponsesInput([]model.Content{
		{Role: model.RoleSystem, Content: "be concise"},
		{Role: model.RoleUser, Content: "hello"},
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "Echo", Arguments: json.RawMessage(`{"message":"hi"}`)},
			},
		},
		{
			Role: model.RoleTool,
			ToolResult: &model.ToolResult{
				ToolCallID:        "call_1",
				Name:              "Echo",
				Content:           "hi",
				StructuredContent: json.RawMessage(`{"message":"hi"}`),
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, input, 4)

	raw, err := json.Marshal(input)
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "system", got[0]["role"])
	assert.Equal(t, "be concise", got[0]["content"])
	assert.Equal(t, "function_call", got[2]["type"])
	assert.Equal(t, "call_1", got[2]["call_id"])
	assert.Equal(t, "Echo", got[2]["name"])
	assert.Equal(t, `{"message":"hi"}`, got[2]["arguments"])
	assert.Equal(t, "function_call_output", got[3]["type"])
	assert.Equal(t, "call_1", got[3]["call_id"])
	assert.Equal(t, "hi", got[3]["output"])
}

func TestConvertResponsesInput_MultimodalUser(t *testing.T) {
	input, err := convertResponsesInput([]model.Content{{
		Role: model.RoleUser,
		Parts: []model.ContentPart{
			{Type: model.ContentPartTypeText, Text: "describe"},
			{Type: model.ContentPartTypeImageURL, ImageURL: "https://example.com/image.png", ImageDetail: model.ImageDetailHigh},
			{Type: model.ContentPartTypeImageBase64, MIMEType: "image/png", ImageBase64: "aW1hZ2U=", ImageDetail: model.ImageDetailLow},
		},
	}})
	require.NoError(t, err)
	require.Len(t, input, 1)

	raw, err := json.Marshal(input[0])
	require.NoError(t, err)

	var got struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL string `json:"image_url"`
			Detail   string `json:"detail"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "user", got.Role)
	require.Len(t, got.Content, 3)
	assert.Equal(t, "input_text", got.Content[0].Type)
	assert.Equal(t, "describe", got.Content[0].Text)
	assert.Equal(t, "input_image", got.Content[1].Type)
	assert.Equal(t, "https://example.com/image.png", got.Content[1].ImageURL)
	assert.Equal(t, "high", got.Content[1].Detail)
	assert.Equal(t, "data:image/png;base64,aW1hZ2U=", got.Content[2].ImageURL)
	assert.Equal(t, "low", got.Content[2].Detail)
}

func TestConvertResponsesTools_EchoTool(t *testing.T) {
	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)

	result, err := convertResponsesTools([]tool.Tool{echo})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.NotNil(t, result[0].OfFunction)
	assert.Equal(t, echo.Definition().Name, result[0].OfFunction.Name)
	assert.True(t, result[0].OfFunction.Description.Valid())
	assert.NotEmpty(t, result[0].OfFunction.Parameters)
	assert.True(t, result[0].OfFunction.Strict.Valid())
	assert.False(t, result[0].OfFunction.Strict.Value)
}

func TestConvertResponsesResponse_TextAndToolCalls(t *testing.T) {
	var resp goresponses.Response
	require.NoError(t, json.Unmarshal([]byte(`{
		"id": "resp_1",
		"status": "completed",
		"output": [
			{
				"type": "message",
				"id": "msg_1",
				"role": "assistant",
				"status": "completed",
				"content": [
					{"type": "output_text", "text": "calling tool", "annotations": []}
				]
			},
			{
				"type": "function_call",
				"id": "fc_1",
				"call_id": "call_1",
				"name": "Echo",
				"arguments": "{\"message\":\"hi\"}",
				"status": "completed"
			}
		],
		"usage": {
			"input_tokens": 3,
			"input_tokens_details": {"cached_tokens": 2},
			"output_tokens": 4,
			"output_tokens_details": {"reasoning_tokens": 1},
			"total_tokens": 7
		}
	}`), &resp))

	got := convertResponsesResponse(&resp)
	assert.Equal(t, model.RoleAssistant, got.Content.Role)
	assert.Equal(t, "calling tool", got.Content.Content)
	assert.Equal(t, model.FinishReasonToolCalls, got.FinishReason)
	require.Len(t, got.Content.ToolCalls, 1)
	assert.Equal(t, "call_1", got.Content.ToolCalls[0].ID)
	assert.Equal(t, "Echo", got.Content.ToolCalls[0].Name)
	assert.JSONEq(t, `{"message":"hi"}`, string(got.Content.ToolCalls[0].Arguments))
	require.NotNil(t, got.Usage)
	assert.Equal(t, int64(3), got.Usage.PromptTokens)
	assert.Equal(t, int64(4), got.Usage.CompletionTokens)
	assert.Equal(t, int64(7), got.Usage.TotalTokens)
	require.NotNil(t, got.Usage.Details)
	assert.Equal(t, int64(2), got.Usage.Details.CachedPromptTokens)
	assert.Equal(t, int64(1), got.Usage.Details.ReasoningTokens)
}

func TestConvertResponsesResponse_MissingUsage(t *testing.T) {
	var resp goresponses.Response
	require.NoError(t, json.Unmarshal([]byte(`{
		"id": "resp_1",
		"status": "completed",
		"output": [
			{
				"type": "message",
				"id": "msg_1",
				"role": "assistant",
				"status": "completed",
				"content": [
					{"type": "output_text", "text": "pong", "annotations": []}
				]
			}
		]
	}`), &resp))

	got := convertResponsesResponse(&resp)
	assert.Nil(t, got.Usage)
}

func TestResponses_Generate_RequestShape(t *testing.T) {
	var captured struct {
		Model           string           `json:"model"`
		Input           []map[string]any `json:"input"`
		Store           bool             `json:"store"`
		MaxOutputTokens int64            `json:"max_output_tokens"`
		Temperature     float64          `json:"temperature"`
		ServiceTier     string           `json:"service_tier"`
		Tools           []map[string]any `json:"tools"`
	}

	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/responses", r.URL.Path)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{
			"id": "resp_1",
			"status": "completed",
			"output": [
				{
					"type": "message",
					"id": "msg_1",
					"role": "assistant",
					"status": "completed",
					"content": [
						{"type": "output_text", "text": "pong", "annotations": []}
					]
				}
			],
			"usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}
		}`)),
				Request: r,
			}, nil
		}),
	}

	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)
	llm := &Responses{
		client: goopenai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL("http://example.test"),
			option.WithHTTPClient(httpClient),
		),
		modelName:  "gpt-5-mini",
		retryCfg:   retry.DefaultConfig(),
		generation: generationOptions{serviceTier: ServiceTierFlex},
	}
	resp, err := callGenerate(t.Context(), llm, &model.LLMRequest{
		Model: llm.Name(),
		Contents: []model.Content{
			{Role: model.RoleUser, Content: "ping"},
		},
		Tools: []tool.Tool{echo},
	}, &model.GenerateConfig{
		Temperature: 0.2,
		MaxTokens:   32,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "pong", resp.Content.Content)
	assert.Equal(t, "gpt-5-mini", captured.Model)
	assert.False(t, captured.Store)
	assert.Equal(t, int64(32), captured.MaxOutputTokens)
	assert.Equal(t, 0.2, captured.Temperature)
	assert.Equal(t, "flex", captured.ServiceTier)
	require.Len(t, captured.Input, 1)
	assert.Equal(t, "user", captured.Input[0]["role"])
	assert.Equal(t, "ping", captured.Input[0]["content"])
	require.Len(t, captured.Tools, 1)
	assert.Equal(t, "function", captured.Tools[0]["type"])
	assert.Equal(t, echo.Definition().Name, captured.Tools[0]["name"])
	assert.Equal(t, false, captured.Tools[0]["strict"])
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
