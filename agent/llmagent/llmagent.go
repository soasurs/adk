package llmagent

import (
	"context"
	"fmt"
	"iter"

	"soasurs.dev/soasurs/adk/agent"
	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/tool"
)

// Config holds the configuration for an LLM-backed agent.
type Config struct {
	Name        string
	Description string
	Model       model.LLM
	Tools       []tool.Tool
	// Instruction is prepended as a system message on every Run call.
	Instruction string
	// GenerateConfig controls optional LLM generation parameters.
	GenerateConfig *model.GenerateConfig
	// Stream enables streaming responses. When true, the agent yields partial
	// Events (Event.Partial=true) with incremental text as the LLM generates,
	// followed by complete Events (Event.Partial=false) for each full message.
	Stream bool
}

// LlmAgent is a stateless agent that drives an LLM through a tool-call loop.
type LlmAgent struct {
	config *Config
	tools  map[string]tool.Tool
}

// New creates a new LlmAgent from the given config.
func New(config Config) agent.Agent {
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

// Run executes the agent, yielding each Event produced during the tool-call
// loop. When Stream is true, partial events with incremental text are yielded
// before each complete assistant message. Tool result messages are always
// yielded as complete events. Iteration ends when the LLM stops calling tools.
func (a *LlmAgent) Run(ctx context.Context, messages []model.Message) iter.Seq2[*model.Event, error] {
	return func(yield func(*model.Event, error) bool) {
		// Prepend system prompt when configured.
		history := make([]model.Message, 0, len(messages)+1)
		if a.config.Instruction != "" {
			history = append(history, model.Message{
				Role:    model.RoleSystem,
				Content: a.config.Instruction,
			})
		}
		history = append(history, messages...)

		req := &model.LLMRequest{
			Model:    a.config.Model.Name(),
			Messages: history,
			Tools:    a.config.Tools,
		}

		for {
			// Collect the LLM response, yielding partial events along the way.
			var completeResp *model.LLMResponse
			for resp, err := range a.config.Model.GenerateContent(ctx, req, a.config.GenerateConfig, a.config.Stream) {
				if err != nil {
					yield(nil, err)
					return
				}
				if resp.Partial {
					// Yield streaming fragment for real-time display.
					if !yield(&model.Event{Message: resp.Message, Partial: true}, nil) {
						return
					}
				} else {
					completeResp = resp
				}
			}

			if completeResp == nil {
				return
			}

			// Attach token usage to the complete assistant message.
			completeResp.Message.Usage = completeResp.Usage

			// Yield the complete assistant message (may contain tool_calls).
			if !yield(&model.Event{Message: completeResp.Message, Partial: false}, nil) {
				return
			}

			// No tool calls — generation is complete.
			if completeResp.FinishReason != model.FinishReasonToolCalls {
				return
			}

			// Append the assistant message before executing tools.
			req.Messages = append(req.Messages, completeResp.Message)

			// Execute each requested tool call and yield its result.
			for _, tc := range completeResp.Message.ToolCalls {
				toolMsg := a.runToolCall(ctx, tc)
				if !yield(&model.Event{Message: toolMsg, Partial: false}, nil) {
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
