package llmagent

import (
	"context"

	"github.com/soasurs/adk/model"
)

// InstructionProvider builds an ephemeral system instruction for one LLM
// invocation. Implementations may be called concurrently by separate Run calls.
type InstructionProvider func(ctx context.Context, input InstructionInput) (string, error)

// InstructionInput describes the current LLM invocation for an
// InstructionProvider.
type InstructionInput struct {
	// AgentName is the logical name of the agent being run.
	AgentName string
	// Iteration is the 1-based LLM invocation number within the current Run.
	Iteration int
	// Conversation is the canonical conversation without system messages.
	// It is an isolated deep copy and may be modified by the provider.
	Conversation []model.Content
}

func cloneContents(contents []model.Content) []model.Content {
	if contents == nil {
		return nil
	}
	out := make([]model.Content, len(contents))
	for i := range contents {
		out[i] = cloneContent(contents[i])
	}
	return out
}

func cloneContent(content model.Content) model.Content {
	out := content
	if content.Parts != nil {
		out.Parts = append([]model.ContentPart(nil), content.Parts...)
	}
	if content.ToolCalls != nil {
		out.ToolCalls = make([]model.ToolCall, len(content.ToolCalls))
		for i, call := range content.ToolCalls {
			out.ToolCalls[i] = call
			if call.Arguments != nil {
				out.ToolCalls[i].Arguments = append([]byte(nil), call.Arguments...)
			}
			if call.ThoughtSignature != nil {
				out.ToolCalls[i].ThoughtSignature = append([]byte(nil), call.ThoughtSignature...)
			}
		}
	}
	if content.ToolResult != nil {
		result := *content.ToolResult
		if content.ToolResult.StructuredContent != nil {
			result.StructuredContent = append([]byte(nil), content.ToolResult.StructuredContent...)
		}
		out.ToolResult = &result
	}
	return out
}
