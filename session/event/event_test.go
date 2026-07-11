package event

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
)

// TestParts_ValueScan verifies that Parts serializes to JSON and deserializes back correctly.
func TestParts_ValueScan(t *testing.T) {
	original := Parts{
		{Type: model.ContentPartTypeText, Text: "hello"},
		{
			Type:        model.ContentPartTypeImageURL,
			ImageURL:    "https://example.com/img.png",
			ImageDetail: model.ImageDetailHigh,
		},
		{
			Type:        model.ContentPartTypeImageBase64,
			ImageBase64: "abc123",
			MIMEType:    "image/jpeg",
		},
	}

	val, err := original.Value()
	require.NoError(t, err)

	var restored Parts
	err = restored.Scan(val)
	require.NoError(t, err)

	require.Len(t, restored, len(original))
	assert.Equal(t, original[0].Type, restored[0].Type)
	assert.Equal(t, original[0].Text, restored[0].Text)
	assert.Equal(t, original[1].ImageURL, restored[1].ImageURL)
	assert.Equal(t, original[1].ImageDetail, restored[1].ImageDetail)
	assert.Equal(t, original[2].ImageBase64, restored[2].ImageBase64)
	assert.Equal(t, original[2].MIMEType, restored[2].MIMEType)
}

// TestParts_ScanNil verifies that scanning a nil value yields an empty Parts slice.
func TestParts_ScanNil(t *testing.T) {
	var p Parts
	err := p.Scan(nil)
	require.NoError(t, err)
	assert.Empty(t, p)
}

// TestParts_ScanBytes verifies that scanning a []byte value works.
func TestParts_ScanBytes(t *testing.T) {
	var p Parts
	err := p.Scan([]byte(`[{"Type":"text","Text":"hi"}]`))
	require.NoError(t, err)
	require.Len(t, p, 1)
	assert.Equal(t, model.ContentPartTypeText, p[0].Type)
	assert.Equal(t, "hi", p[0].Text)
}

// TestFromModel_ToModel_Parts verifies that ContentParts survive a full FromModel → ToModel round-trip.
func TestFromModel_ToModel_Parts(t *testing.T) {
	original := model.Event{
		Author: "user",
		Content: model.Content{
			Role: model.RoleUser,
			Parts: []model.ContentPart{
				{Type: model.ContentPartTypeText, Text: "describe this"},
				{
					Type:        model.ContentPartTypeImageURL,
					ImageURL:    "https://example.com/cat.jpg",
					ImageDetail: model.ImageDetailAuto,
				},
			},
		},
	}

	persisted := FromModel(original)
	require.Len(t, persisted.Parts, 2)

	restored := persisted.ToModel()
	require.Len(t, restored.Content.Parts, 2)
	assert.Equal(t, model.ContentPartTypeText, restored.Content.Parts[0].Type)
	assert.Equal(t, "describe this", restored.Content.Parts[0].Text)
	assert.Equal(t, model.ContentPartTypeImageURL, restored.Content.Parts[1].Type)
	assert.Equal(t, "https://example.com/cat.jpg", restored.Content.Parts[1].ImageURL)
	assert.Equal(t, model.ImageDetailAuto, restored.Content.Parts[1].ImageDetail)
}

// TestFromModel_ToModel_NilParts verifies that a nil Parts field round-trips without panic.
func TestFromModel_ToModel_NilParts(t *testing.T) {
	original := model.Event{
		Author: "user",
		Content: model.Content{
			Role:    model.RoleUser,
			Content: "plain text",
		},
	}
	persisted := FromModel(original)
	assert.Empty(t, persisted.Parts)

	restored := persisted.ToModel()
	assert.Equal(t, "plain text", restored.Content.Content)
	assert.Empty(t, restored.Content.Parts)
}

func TestFromModel_ToModel_TurnID(t *testing.T) {
	original := model.Event{
		ID:        123,
		SessionID: "session-1",
		TurnID:    "turn-1",
		Author:    "user",
		Content: model.Content{
			Role:    model.RoleUser,
			Content: "hello",
		},
	}

	persisted := FromModel(original)
	assert.Equal(t, "turn-1", persisted.TurnID)

	restored := persisted.ToModel()
	assert.Equal(t, "turn-1", restored.TurnID)
}

func TestFromModel_ToModel_ToolCallThoughtSignature(t *testing.T) {
	original := model.Event{
		Author: "assistant",
		Content: model.Content{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID:               "call-1",
					Name:             "lookup",
					Arguments:        json.RawMessage(`{"query":"weather"}`),
					ThoughtSignature: []byte{0x01, 0x02, 0xff},
				},
			},
		},
	}

	restored := FromModel(original).ToModel()

	require.Len(t, restored.Content.ToolCalls, 1)
	assert.Equal(t, original.Content.ToolCalls[0], restored.Content.ToolCalls[0])
}

func TestFromModel_ToModel_ToolResponse(t *testing.T) {
	original := model.Event{
		Author: "lookup",
		Content: model.Content{
			Role:    model.RoleTool,
			Content: `{"temperature":23}`,
			ToolResponse: &model.ToolResponse{
				ToolCallID: "call-1",
				Name:       "lookup",
				Outcome: &tool.Result{
					Content:           `{"temperature":23}`,
					StructuredContent: json.RawMessage(`{"temperature":23}`),
				},
			},
		},
	}

	restored := FromModel(original).ToModel()

	require.NotNil(t, restored.Content.ToolResponse)
	assert.Equal(t, "call-1", restored.Content.ToolResponse.ToolCallID)
	assert.Equal(t, "lookup", restored.Content.ToolResponse.Name)
	result, ok := restored.Content.ToolResponse.Outcome.(*tool.Result)
	require.True(t, ok)
	assert.JSONEq(t, `{"temperature":23}`, string(result.StructuredContent))
	assert.Equal(t, "call-1", restored.Content.ToolCallID)
}

func TestFromModel_ToModel_HandledToolResponse(t *testing.T) {
	original := model.Event{
		Author: "lookup",
		Content: model.Content{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ToolCallID: "call-1",
				Name:       "lookup",
				Outcome: &tool.HandledError{
					Content:           "not available",
					StructuredContent: json.RawMessage(`{"code":"not_available"}`),
				},
			},
		},
	}

	stored := FromModel(original)
	value, err := stored.ToolResponse.Value()
	require.NoError(t, err)
	assert.NotContains(t, value, "is_error")
	assert.Contains(t, value, `"error"`)

	restored := stored.ToModel()
	require.NotNil(t, restored.Content.ToolResponse)
	failure, ok := restored.Content.ToolResponse.Outcome.(*tool.HandledError)
	require.True(t, ok)
	assert.Equal(t, "not available", failure.Content)
	assert.JSONEq(t, `{"code":"not_available"}`, string(failure.StructuredContent))
}

func TestToolResponse_ScanLegacyIsError(t *testing.T) {
	var response ToolResponse
	err := response.Scan(`{"tool_call_id":"call-1","name":"lookup","content":"not available","structured_content":{"code":"not_available"},"is_error":true}`)
	require.NoError(t, err)
	require.NotNil(t, response.Error)
	assert.Nil(t, response.Result)
	assert.Equal(t, "not available", response.Error.Content)
	assert.JSONEq(t, `{"code":"not_available"}`, string(response.Error.StructuredContent))
}

func TestToolResponse_ValueRejectsInvalidOutcomeCount(t *testing.T) {
	for _, response := range []ToolResponse{
		{ToolCallID: "call-1"},
		{ToolCallID: "call-1", Result: &tool.Result{}, Error: tool.NewHandledError("failed")},
	} {
		_, err := response.Value()
		require.Error(t, err)
	}
}

func TestUsageDetails_ValueScan(t *testing.T) {
	original := UsageDetails(model.TokenUsageDetails{
		CachedPromptTokens:        12,
		CacheCreationPromptTokens: 3,
		CacheReadPromptTokens:     9,
		ReasoningTokens:           4,
		ToolUsePromptTokens:       5,
		AudioPromptTokens:         6,
		AudioCompletionTokens:     7,
		AcceptedPredictionTokens:  8,
		RejectedPredictionTokens:  2,
	})

	val, err := original.Value()
	require.NoError(t, err)
	require.IsType(t, "", val)
	assert.JSONEq(t, `{
		"cached_prompt_tokens": 12,
		"cache_creation_prompt_tokens": 3,
		"cache_read_prompt_tokens": 9,
		"reasoning_tokens": 4,
		"tool_use_prompt_tokens": 5,
		"audio_prompt_tokens": 6,
		"audio_completion_tokens": 7,
		"accepted_prediction_tokens": 8,
		"rejected_prediction_tokens": 2
	}`, val.(string))

	var restored UsageDetails
	require.NoError(t, restored.Scan(val))
	assert.Equal(t, original, restored)

	emptyVal, err := UsageDetails{}.Value()
	require.NoError(t, err)
	assert.Equal(t, "", emptyVal)

	require.NoError(t, restored.Scan(nil))
	assert.Equal(t, UsageDetails{}, restored)
	require.NoError(t, restored.Scan(""))
	assert.Equal(t, UsageDetails{}, restored)
}

func TestFromModel_ToModel_UsageDetails(t *testing.T) {
	original := model.Event{
		Author: "assistant",
		Content: model.Content{
			Role:    model.RoleAssistant,
			Content: "answer",
		},
		Usage: &model.TokenUsage{
			PromptTokens:     20,
			CompletionTokens: 10,
			TotalTokens:      30,
			Details: &model.TokenUsageDetails{
				CachedPromptTokens:        12,
				CacheCreationPromptTokens: 3,
				CacheReadPromptTokens:     9,
				ReasoningTokens:           4,
				ToolUsePromptTokens:       5,
				AudioPromptTokens:         6,
				AudioCompletionTokens:     7,
				AcceptedPredictionTokens:  8,
				RejectedPredictionTokens:  2,
			},
		},
	}

	restored := FromModel(original).ToModel()

	require.NotNil(t, restored.Usage)
	assert.Equal(t, original.Usage.PromptTokens, restored.Usage.PromptTokens)
	assert.Equal(t, original.Usage.CompletionTokens, restored.Usage.CompletionTokens)
	assert.Equal(t, original.Usage.TotalTokens, restored.Usage.TotalTokens)
	require.NotNil(t, restored.Usage.Details)
	assert.Equal(t, original.Usage.Details, restored.Usage.Details)
}
