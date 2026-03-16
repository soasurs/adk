package llmagent

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/soasurs/adk/agent"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
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
	// ToolTimeout bounds every individual tool Run call. When non-zero, each
	// tool invocation gets a fresh context derived from the call context with
	// this deadline. The shorter of ToolTimeout and any deadline already
	// present in the incoming context wins. Zero means no per-agent timeout
	// (tools may still be bounded by the call context or tool.WithTimeout).
	ToolTimeout time.Duration
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
		// Find the last system message in the session history, which is the most
		// recent compaction summary. Earlier system messages, if any, have already
		// been subsumed by a subsequent compaction and should be dropped.
		lastSummaryIdx := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == model.RoleSystem {
				lastSummaryIdx = i
				break
			}
		}

		// Build the leading system message by merging the agent instruction with
		// the compaction summary (if present).
		systemParts := make([]string, 0, 2)
		if a.config.Instruction != "" {
			systemParts = append(systemParts, a.config.Instruction)
		}
		if lastSummaryIdx >= 0 {
			systemParts = append(systemParts, messages[lastSummaryIdx].Content)
		}

		history := make([]model.Message, 0, len(messages)+1)
		if len(systemParts) > 0 {
			history = append(history, model.Message{
				Role:    model.RoleSystem,
				Content: strings.Join(systemParts, "\n\n"),
			})
		}

		// Append all non-system messages, preserving conversation order.
		// All session-sourced system messages are dropped here: the last one has
		// been merged above, and earlier ones are stale compaction artifacts.
		for _, m := range messages {
			if m.Role == model.RoleSystem {
				continue
			}
			history = append(history, m)
		}

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

			// Execute all tool calls in parallel, preserving original order.
			toolMsgs := make([]model.Message, len(completeResp.Message.ToolCalls))
			var wg sync.WaitGroup
			for i, tc := range completeResp.Message.ToolCalls {
				wg.Add(1)
				go func(i int, tc model.ToolCall) {
					defer wg.Done()
					toolMsgs[i] = a.runToolCall(ctx, tc)
				}(i, tc)
			}
			wg.Wait()

			for _, toolMsg := range toolMsgs {
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
	if a.config.ToolTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.config.ToolTimeout)
		defer cancel()
	}

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
