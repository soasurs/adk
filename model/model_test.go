package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/tool"
)

func TestToolResponse_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		outcome  tool.Outcome
		field    string
		wantType any
	}{
		{name: "result", outcome: &tool.Result{Content: "ok"}, field: `"result"`, wantType: (*tool.Result)(nil)},
		{name: "error", outcome: tool.NewHandledError("failed"), field: `"error"`, wantType: (*tool.HandledError)(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := ToolResponse{ToolCallID: "call-1", Name: "lookup", Outcome: tt.outcome}
			data, err := json.Marshal(original)
			require.NoError(t, err)
			assert.Contains(t, string(data), tt.field)
			assert.NotContains(t, string(data), "is_error")

			var restored ToolResponse
			require.NoError(t, json.Unmarshal(data, &restored))
			assert.Equal(t, original.ToolCallID, restored.ToolCallID)
			assert.Equal(t, original.Name, restored.Name)
			switch tt.wantType.(type) {
			case *tool.Result:
				_, ok := restored.Outcome.(*tool.Result)
				assert.True(t, ok)
			case *tool.HandledError:
				_, ok := restored.Outcome.(*tool.HandledError)
				assert.True(t, ok)
			}
		})
	}
}

func TestToolResponseValue_EmptyOutcomeFallback(t *testing.T) {
	tests := []struct {
		name     string
		content  Content
		wantText string
	}{
		{
			name:     "nil tool response uses c.Content",
			content:  Content{Role: RoleTool, ToolCallID: "c-1", Content: "legacy fallback", ToolResponse: nil},
			wantText: "legacy fallback",
		},
		{
			name: "explicit empty result uses c.Content",
			content: Content{
				Role:       RoleTool,
				Content:    "text fallback",
				ToolCallID: "c-2",
				ToolResponse: &ToolResponse{
					ToolCallID: "c-2",
					Name:       "echo",
					Outcome:    &tool.Result{},
				},
			},
			wantText: "text fallback",
		},
		{
			name: "non-empty result preserved",
			content: Content{
				Role:    RoleTool,
				Content: "should not be used",
				ToolResponse: &ToolResponse{
					ToolCallID: "c-3",
					Name:       "echo",
					Outcome:    &tool.Result{Content: "explicit content"},
				},
			},
			wantText: "explicit content",
		},
		{
			name: "structured content only no fallback needed",
			content: Content{
				Role:    RoleTool,
				Content: "",
				ToolResponse: &ToolResponse{
					ToolCallID: "c-4",
					Name:       "echo",
					Outcome:    &tool.Result{StructuredContent: json.RawMessage(`{"k":"v"}`)},
				},
			},
			wantText: `{"k":"v"}`,
		},
		{
			name: "handled error not overwritten by c.Content",
			content: Content{
				Role:    RoleTool,
				Content: "should not be used",
				ToolResponse: &ToolResponse{
					ToolCallID: "c-5",
					Name:       "lookup",
					Outcome:    tool.NewHandledError("record not found"),
				},
			},
			wantText: "record not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := tt.content.ToolResponseValue()
			assert.Equal(t, tt.wantText, response.Text())
		})
	}
}

func TestToolResponse_JSONRejectsMissingOrAmbiguousOutcome(t *testing.T) {
	for _, data := range []string{
		`{"tool_call_id":"call-1"}`,
		`{"tool_call_id":"call-1","result":{},"error":{"content":"failed"}}`,
	} {
		var response ToolResponse
		require.Error(t, json.Unmarshal([]byte(data), &response))
	}
}
