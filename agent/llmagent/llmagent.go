package llmagent

import (
	"context"
	"fmt"
	"iter"

	"soasurs.dev/soasurs/adk/agent"
	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/tool"
)

// LlmAgentConfig holds the configuration for an LLM-backed agent.
type LlmAgentConfig struct {
	Name        string
	Description string
	Model       model.LLM
	Tools       []tool.Tool
	// SystemPrompt is prepended as a system message on every Run call.
	SystemPrompt string
	// GenerateConfig controls optional LLM generation parameters.
	GenerateConfig *model.GenerateConfig
}

// LlmAgent is a stateless agent that drives an LLM through a tool-call loop.
type LlmAgent struct {
	config *LlmAgentConfig
	tools  map[string]tool.Tool
}

// New creates a new LlmAgent from the given config.
func New(config LlmAgentConfig) agent.Agent {
	tools := make(map[string]tool.Tool, len(config.Tools))
	for _, t := range config.Tools {
		tools[t.Definition().Name] = t
	}
	return &LlmAgent{
		config: &config,
		tools:  tools,
	}
}

func (a *LlmAgent) Name() string {
	return a.config.Name
}

func (a *LlmAgent) Description() string {
	return a.config.Description
}

// Run executes the agent, yielding each message produced during the tool-call
// loop: assistant messages (with or without tool calls) and tool result
// messages. Iteration ends when the LLM produces a stop response.
func (a *LlmAgent) Run(ctx context.Context, messages []model.Message) iter.Seq2[model.Message, error] {
	return func(yield func(model.Message, error) bool) {
		// Prepend system prompt when configured.
		history := make([]model.Message, 0, len(messages)+1)
		if a.config.SystemPrompt != "" {
			history = append(history, model.Message{
				Role:    model.RoleSystem,
				Content: a.config.SystemPrompt,
			})
		}
		history = append(history, messages...)

		req := &model.LLMRequest{
			Model:    a.config.Model.Name(),
			Messages: history,
			Tools:    a.config.Tools,
		}

		for {
			resp, err := a.config.Model.Generate(ctx, req, a.config.GenerateConfig)
			if err != nil {
				yield(model.Message{}, err)
				return
			}

			// Yield the assistant message (may contain tool_calls).
			if !yield(resp.Message, nil) {
				return
			}

			// No tool calls — generation is complete.
			if resp.FinishReason != model.FinishReasonToolCalls {
				return
			}

			// Append the assistant message before executing tools.
			req.Messages = append(req.Messages, resp.Message)

			// Execute each requested tool call and yield its result.
			for _, tc := range resp.Message.ToolCalls {
				toolMsg := a.runToolCall(ctx, tc)
				if !yield(toolMsg, nil) {
					return
				}
				req.Messages = append(req.Messages, toolMsg)
			}
		}
	}
}

// runToolCall executes a single tool call and returns the resulting tool message.
func (a *LlmAgent) runToolCall(ctx context.Context, tc model.ToolCall) model.Message {
	t, ok := a.tools[tc.Name]
	if !ok {
		return model.Message{
			Role:       model.RoleTool,
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("tool %q not found", tc.Name),
		}
	}

	result, err := t.Run(ctx, tc.ID, tc.Arguments)
	if err != nil {
		result = fmt.Sprintf("error: %s", err.Error())
	}
	return model.Message{
		Role:       model.RoleTool,
		ToolCallID: tc.ID,
		Content:    result,
	}
}
