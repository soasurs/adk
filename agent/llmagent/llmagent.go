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
	// BeforeLLMCall runs immediately before each GenerateContent call.
	// Return a non-nil *model.LLMResponse to skip the actual LLM call and use
	// the returned response instead. Return an error to abort execution.
	BeforeLLMCall func(ctx context.Context, call *LLMCall) (*model.LLMResponse, error)
	// AfterLLMCall runs after each GenerateContent call completes or fails.
	AfterLLMCall func(ctx context.Context, call *LLMCall, result *LLMCallResult) error
	// BeforeToolCall runs immediately before each tool invocation.
	// Return a non-nil *ToolCallResult to skip the actual tool execution and
	// use the returned result instead. Return an error to abort execution.
	BeforeToolCall func(ctx context.Context, call *ToolCall) (*ToolCallResult, error)
	// AfterToolCall runs after each tool invocation completes, including tool
	// lookup failures and tool execution errors.
	AfterToolCall func(ctx context.Context, call *ToolCall, result *ToolCallResult) error
	// Instruction is prepended as a system message on every Run call.
	Instruction string
	// GenerateConfig controls optional LLM generation parameters.
	GenerateConfig *model.GenerateConfig
	// MaxIterations limits the number of LLM calls in the tool-call loop.
	// Each call to the model — whether or not it results in tool calls —
	// counts as one iteration. Zero means no limit.
	// When the limit is reached, Run yields an error and stops.
	MaxIterations int
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

		iteration := 0
		for {
			iteration++
			if a.config.MaxIterations > 0 && iteration > a.config.MaxIterations {
				yield(nil, fmt.Errorf("llmagent: max iterations exceeded (%d)", a.config.MaxIterations))
				return
			}
			call := &LLMCall{
				AgentName:      a.Name(),
				Iteration:      iteration,
				Request:        req,
				GenerateConfig: a.config.GenerateConfig,
				Stream:         a.config.Stream,
			}
			skipResp, err := a.beforeLLMCall(ctx, call)
			if err != nil {
				yield(nil, err)
				return
			}

			// Collect the LLM response, yielding partial events along the way.
			var completeResp *model.LLMResponse
			var llmErr error
			partialResponses := 0
			stoppedEarly := false
			startedAt := time.Now()
			if skipResp != nil {
				completeResp = skipResp
			} else {
				for resp, err := range a.config.Model.GenerateContent(ctx, req, a.config.GenerateConfig, a.config.Stream) {
					if err != nil {
						llmErr = err
						break
					}
					if resp.Partial {
						partialResponses++
						// Yield streaming fragment for real-time display.
						if !yield(&model.Event{Message: resp.Message, Partial: true}, nil) {
							stoppedEarly = true
							break
						}
					} else {
						completeResp = resp
					}
				}
			}

			if err := a.afterLLMCall(ctx, call, &LLMCallResult{
				Response:         completeResp,
				Err:              llmErr,
				PartialResponses: partialResponses,
				Duration:         time.Since(startedAt),
				StoppedEarly:     stoppedEarly,
			}); err != nil {
				yield(nil, err)
				return
			}

			if llmErr != nil {
				yield(nil, llmErr)
				return
			}

			if stoppedEarly {
				return
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
			toolCtx, cancelTools := context.WithCancel(ctx)
			toolMsgs := make([]model.Message, len(completeResp.Message.ToolCalls))
			toolErrs := make([]error, len(completeResp.Message.ToolCalls))
			var wg sync.WaitGroup
			for i, tc := range completeResp.Message.ToolCalls {
				wg.Add(1)
				go func(i int, tc model.ToolCall) {
					defer wg.Done()
					toolMsg, err := a.runToolCall(toolCtx, iteration, i, tc)
					if err != nil {
						toolErrs[i] = err
						cancelTools()
						return
					}
					toolMsgs[i] = toolMsg
				}(i, tc)
			}
			wg.Wait()
			cancelTools()

			for _, err := range toolErrs {
				if err != nil {
					yield(nil, err)
					return
				}
			}

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
func (a *LlmAgent) runToolCall(ctx context.Context, iteration, toolIndex int, tc model.ToolCall) (model.Message, error) {
	if a.config.ToolTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.config.ToolTimeout)
		defer cancel()
	}

	t, ok := a.tools[tc.Name]
	def := tool.Definition{Name: tc.Name}
	if ok {
		def = t.Definition()
	}

	call := &ToolCall{
		AgentName:  a.Name(),
		Iteration:  iteration,
		ToolIndex:  toolIndex,
		Request:    tc,
		Tool:       t,
		Definition: def,
	}
	skipResult, err := a.beforeToolCall(ctx, call)
	if err != nil {
		return model.Message{}, err
	}

	startedAt := time.Now()
	if skipResult != nil {
		if err := a.afterToolCall(ctx, call, skipResult); err != nil {
			return model.Message{}, err
		}
		return skipResult.Message, nil
	}

	if !ok {
		toolErr := fmt.Errorf("tool %q not found", tc.Name)
		msg := model.Message{
			Role:       model.RoleTool,
			ToolCallID: tc.ID,
			Content:    toolErr.Error(),
		}
		if err := a.afterToolCall(ctx, call, &ToolCallResult{
			Message:  msg,
			Err:      toolErr,
			Duration: time.Since(startedAt),
		}); err != nil {
			return model.Message{}, err
		}
		return msg, nil
	}

	result, toolErr := t.Run(ctx, tc.ID, tc.Arguments)
	msg := model.Message{
		Role:       model.RoleTool,
		ToolCallID: tc.ID,
		Content:    result,
	}
	if toolErr != nil {
		msg.Content = fmt.Sprintf("error: %s", toolErr.Error())
	}
	if err := a.afterToolCall(ctx, call, &ToolCallResult{
		Message:  msg,
		Result:   result,
		Err:      toolErr,
		Duration: time.Since(startedAt),
	}); err != nil {
		return model.Message{}, err
	}
	return msg, nil
}

func (a *LlmAgent) beforeLLMCall(ctx context.Context, call *LLMCall) (*model.LLMResponse, error) {
	if a.config.BeforeLLMCall == nil {
		return nil, nil
	}
	return a.config.BeforeLLMCall(ctx, call)
}

func (a *LlmAgent) afterLLMCall(ctx context.Context, call *LLMCall, result *LLMCallResult) error {
	if a.config.AfterLLMCall == nil {
		return nil
	}
	return a.config.AfterLLMCall(ctx, call, result)
}

func (a *LlmAgent) beforeToolCall(ctx context.Context, call *ToolCall) (*ToolCallResult, error) {
	if a.config.BeforeToolCall == nil {
		return nil, nil
	}
	return a.config.BeforeToolCall(ctx, call)
}

func (a *LlmAgent) afterToolCall(ctx context.Context, call *ToolCall, result *ToolCallResult) error {
	if a.config.AfterToolCall == nil {
		return nil
	}
	return a.config.AfterToolCall(ctx, call, result)
}
