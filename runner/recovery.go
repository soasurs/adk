package runner

import "github.com/soasurs/adk/model"

// findUnknownToolExecution returns the first assistant tool-call group that
// does not have a matching durable result for every call. Results are matched
// only within the contiguous tool-result events following that assistant event
// so provider call IDs reused by later turns cannot close an older group.
func findUnknownToolExecution(sessionID string, events []model.Event) *ToolExecutionUnknownError {
	for i := 0; i < len(events); i++ {
		assistantEvent := events[i]
		if assistantEvent.Content.Role != model.RoleAssistant || len(assistantEvent.Content.ToolCalls) == 0 {
			continue
		}

		matched := make([]bool, len(assistantEvent.Content.ToolCalls))
		remaining := len(matched)
		j := i + 1
		for ; j < len(events) && events[j].Content.Role == model.RoleTool; j++ {
			if assistantEvent.TurnID != events[j].TurnID {
				continue
			}
			resultID := events[j].Content.ToolResultValue().ToolCallID
			for callIndex, call := range assistantEvent.Content.ToolCalls {
				if !matched[callIndex] && call.ID == resultID {
					matched[callIndex] = true
					remaining--
					break
				}
			}
		}

		if remaining > 0 {
			toolCalls := make([]model.ToolCall, 0, remaining)
			for callIndex, call := range assistantEvent.Content.ToolCalls {
				if !matched[callIndex] {
					toolCalls = append(toolCalls, cloneToolCall(call))
				}
			}
			return &ToolExecutionUnknownError{
				SessionID: sessionID,
				TurnID:    assistantEvent.TurnID,
				EventID:   assistantEvent.ID,
				ToolCalls: toolCalls,
			}
		}

		i = j - 1
	}
	return nil
}

func cloneToolCall(call model.ToolCall) model.ToolCall {
	call.Arguments = append([]byte(nil), call.Arguments...)
	call.ThoughtSignature = append([]byte(nil), call.ThoughtSignature...)
	return call
}
