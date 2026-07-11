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

func TestToolResponse_JSONRejectsMissingOrAmbiguousOutcome(t *testing.T) {
	for _, data := range []string{
		`{"tool_call_id":"call-1"}`,
		`{"tool_call_id":"call-1","result":{},"error":{"content":"failed"}}`,
	} {
		var response ToolResponse
		require.Error(t, json.Unmarshal([]byte(data), &response))
	}
}
