package llmagent

import (
	"context"
	"fmt"
	"iter"
	"reflect"
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
// It panics when config is invalid. Call NewWithError when configuration is
// assembled dynamically and validation errors should be handled by the caller.
func New(config Config) agent.Agent {
	a, err := NewWithError(config)
	if err != nil {
		panic(err)
	}
	return a
}

// NewWithError validates config and creates a new LlmAgent.
func NewWithError(config Config) (agent.Agent, error) {
	if isNil(config.Model) {
		return nil, &ConfigError{Field: "model", Reason: "must not be nil"}
	}
	if config.MaxIterations < 0 {
		return nil, &ConfigError{Field: "max_iterations", Reason: "must not be negative"}
	}
	if config.ToolTimeout < 0 {
		return nil, &ConfigError{Field: "tool_timeout", Reason: "must not be negative"}
	}

	tools := make(map[string]tool.Tool, len(config.Tools))
	for i, t := range config.Tools {
		if isNil(t) {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("tools[%d]", i),
				Reason: "must not be nil",
			}
		}
		name := t.Definition().Name
		if name == "" {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("tools[%d].definition.name", i),
				Reason: "must not be empty",
			}
		}
		if _, exists := tools[name]; exists {
			return nil, &ConfigError{
				Field:  "tools",
				Reason: fmt.Sprintf("duplicate tool name %q", name),
			}
		}
		tools[name] = t
	}
	config.Tools = append([]tool.Tool(nil), config.Tools...)
	return &LlmAgent{
		config: &config,
		tools:  tools,
	}, nil
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
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
func (a *LlmAgent) Run(ctx context.Context, events []model.Event) iter.Seq2[*model.Event, error] {
	return func(yield func(*model.Event, error) bool) {
		// Find the last system message in the session history, which is the most
		// recent compaction summary. Earlier system messages, if any, have already
		// been subsumed by a subsequent compaction and should be dropped.
		lastSummaryIdx := -1
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Content.Role == model.RoleSystem {
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
			systemParts = append(systemParts, events[lastSummaryIdx].Content.Content)
		}

		history := make([]model.Content, 0, len(events)+1)
		if len(systemParts) > 0 {
			history = append(history, model.Content{
				Role:    model.RoleSystem,
				Content: strings.Join(systemParts, "\n\n"),
			})
		}

		// Append all non-system event content, preserving conversation order.
		// All session-sourced system events are dropped here: the last one has
		// been merged above, and earlier ones are stale compaction artifacts.
		for _, event := range events {
			if event.Content.Role == model.RoleSystem {
				continue
			}
			history = append(history, event.Content)
		}

		req := &model.LLMRequest{
			Model:    a.config.Model.Name(),
			Contents: history,
			Tools:    a.config.Tools,
		}

		iteration := 0
		for {
			iteration++
			if a.config.MaxIterations > 0 && iteration > a.config.MaxIterations {
				yield(nil, &MaxIterationsError{MaxIterations: a.config.MaxIterations})
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
						if !yield(&model.Event{Author: a.Name(), Content: resp.Content, Partial: true}, nil) {
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

			completeEvent := model.Event{
				Author:       a.Name(),
				Content:      completeResp.Content,
				FinishReason: completeResp.FinishReason,
				Usage:        completeResp.Usage,
			}

			// Yield the complete assistant event (may contain tool_calls).
			if !yield(&completeEvent, nil) {
				return
			}

			// No tool calls — generation is complete.
			if completeResp.FinishReason != model.FinishReasonToolCalls {
				return
			}

			// Append the assistant content before executing tools.
			req.Contents = append(req.Contents, completeResp.Content)

			// Execute all tool calls in parallel, preserving original order.
			toolCtx, cancelTools := context.WithCancel(ctx)
			toolEvents := make([]model.Event, len(completeResp.Content.ToolCalls))
			toolErrs := make([]error, len(completeResp.Content.ToolCalls))
			var wg sync.WaitGroup
			for i, tc := range completeResp.Content.ToolCalls {
				wg.Add(1)
				go func(i int, tc model.ToolCall) {
					defer wg.Done()
					toolEvent, err := a.runToolCall(toolCtx, iteration, i, tc)
					if err != nil {
						toolErrs[i] = err
						cancelTools()
						return
					}
					toolEvents[i] = toolEvent
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

			for _, toolEvent := range toolEvents {
				if !yield(&toolEvent, nil) {
					return
				}
				req.Contents = append(req.Contents, toolEvent.Content)
			}
		}
	}
}

// runToolCall executes a single tool call and returns the resulting tool event.
func (a *LlmAgent) runToolCall(ctx context.Context, iteration, toolIndex int, tc model.ToolCall) (model.Event, error) {
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
		return model.Event{}, err
	}

	startedAt := time.Now()
	if skipResult != nil {
		if err := a.afterToolCall(ctx, call, skipResult); err != nil {
			return model.Event{}, err
		}
		return skipResult.Event, nil
	}

	if !ok {
		toolErr := &ToolNotFoundError{Name: tc.Name}
		event := model.Event{
			Author: tc.Name,
			Content: model.Content{
				Role:       model.RoleTool,
				ToolCallID: tc.ID,
				Content:    toolErr.Error(),
			},
		}
		if err := a.afterToolCall(ctx, call, &ToolCallResult{
			Event:    event,
			Err:      toolErr,
			Duration: time.Since(startedAt),
		}); err != nil {
			return model.Event{}, err
		}
		return event, nil
	}

	result, toolErr := t.Run(ctx, tc.ID, tc.Arguments)
	event := model.Event{
		Author: t.Definition().Name,
		Content: model.Content{
			Role:       model.RoleTool,
			ToolCallID: tc.ID,
			Content:    result,
		},
	}
	if toolErr != nil {
		event.Content.Content = fmt.Sprintf("error: %s", toolErr.Error())
	}
	if err := a.afterToolCall(ctx, call, &ToolCallResult{
		Event:    event,
		Result:   result,
		Err:      toolErr,
		Duration: time.Since(startedAt),
	}); err != nil {
		return model.Event{}, err
	}
	return event, nil
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
