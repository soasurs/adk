package runner

import "github.com/soasurs/adk/model"

// ToolProtocolIssue describes assistant tool calls without matching results.
type ToolProtocolIssue struct {
	// SessionID identifies the affected session when available.
	SessionID string
	// TurnID identifies the turn containing the assistant tool calls.
	TurnID string
	// EventID identifies the assistant event containing the calls.
	EventID int64
	// ToolCalls contains only calls that have no matching result.
	ToolCalls []model.ToolCall
}

// InspectToolProtocol returns every assistant tool-call group that does not
// have a matching result for every call. Results are matched only within the
// contiguous tool-result events following the assistant event, and only within
// the same Turn, so reused provider call IDs cannot close an older group.
func InspectToolProtocol(events []model.Event) []ToolProtocolIssue {
	issues := make([]ToolProtocolIssue, 0)
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
			resultID := events[j].Content.ToolResponseValue().ToolCallID
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
			issues = append(issues, ToolProtocolIssue{
				SessionID: assistantEvent.SessionID,
				TurnID:    assistantEvent.TurnID,
				EventID:   assistantEvent.ID,
				ToolCalls: toolCalls,
			})
		}

		i = j - 1
	}
	return issues
}

// ValidateToolProtocol returns ErrToolExecutionUnknown when events contain an
// assistant tool call without a matching result. Runner applies this check
// after every context projection, including custom Projector implementations.
func ValidateToolProtocol(events []model.Event) error {
	issues := InspectToolProtocol(events)
	if len(issues) == 0 {
		return nil
	}
	issue := issues[0]
	return &ToolExecutionUnknownError{
		SessionID: issue.SessionID,
		TurnID:    issue.TurnID,
		EventID:   issue.EventID,
		ToolCalls: issue.ToolCalls,
	}
}

func cloneToolCall(call model.ToolCall) model.ToolCall {
	call.Arguments = append([]byte(nil), call.Arguments...)
	call.ThoughtSignature = append([]byte(nil), call.ThoughtSignature...)
	return call
}
