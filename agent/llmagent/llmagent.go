package llmagent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/soasurs/adk/agent"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
	adktrace "github.com/soasurs/adk/trace"
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
	// use the returned result instead. A returned ToolCallResult with Err set, or
	// a non-nil error return, aborts execution.
	BeforeToolCall func(ctx context.Context, call *ToolCall) (*ToolCallResult, error)
	// AfterToolCall runs after each tool invocation completes, including handled
	// lookup failures and terminal tool execution errors. Returning an error
	// aborts execution.
	AfterToolCall func(ctx context.Context, call *ToolCall, result *ToolCallResult) error
	// Instruction is included in the leading system message for every LLM call.
	Instruction string
	// InstructionProvider builds an ephemeral instruction before each LLM call.
	// Its output affects only the current request and is never persisted.
	InstructionProvider InstructionProvider
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
	// (tools may still be bounded by the call context or tool.WithTimeout). A
	// resulting context error is terminal under the tool failure contract.
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

		latestSummary := ""
		if lastSummaryIdx >= 0 {
			latestSummary = events[lastSummaryIdx].Content.Content
		}

		// Append all non-system event content, preserving conversation order.
		// All session-sourced system events are dropped here: the last one has
		// been retained as latestSummary, and earlier ones are stale compaction
		// artifacts.
		conversation := make([]model.Content, 0, len(events))
		for _, event := range events {
			if event.Content.Role == model.RoleSystem {
				continue
			}
			conversation = append(conversation, cloneContent(event.Content))
		}

		modelName := a.config.Model.Name()

		iteration := 0
		for {
			iteration++
			iterationCtx, iterationSpan := adktrace.Start(ctx, adktrace.Event{
				Kind:      adktrace.KindLLMIteration,
				AgentName: a.Name(),
				Model:     modelName,
				Iteration: iteration,
				Stream:    a.config.Stream,
			})
			iterationEnd := adktrace.Event{
				Kind:      adktrace.KindLLMIteration,
				AgentName: a.Name(),
				Model:     modelName,
				Iteration: iteration,
				Stream:    a.config.Stream,
			}
			if a.config.MaxIterations > 0 && iteration > a.config.MaxIterations {
				err := &MaxIterationsError{MaxIterations: a.config.MaxIterations}
				iterationEnd.Err = err
				iterationSpan.End(iterationCtx, iterationEnd)
				yield(nil, err)
				return
			}

			dynamicInstruction := ""
			if a.config.InstructionProvider != nil {
				var err error
				dynamicInstruction, err = a.config.InstructionProvider(iterationCtx, InstructionInput{
					AgentName:    a.Name(),
					Iteration:    iteration,
					Conversation: cloneContents(conversation),
				})
				if err != nil {
					err = fmt.Errorf("llmagent: build instruction: %w", err)
					iterationEnd.Err = err
					iterationSpan.End(iterationCtx, iterationEnd)
					yield(nil, err)
					return
				}
			}

			systemParts := make([]string, 0, 3)
			for _, instruction := range []string{a.config.Instruction, dynamicInstruction, latestSummary} {
				if instruction != "" {
					systemParts = append(systemParts, instruction)
				}
			}
			requestContents := make([]model.Content, 0, len(conversation)+1)
			if len(systemParts) > 0 {
				requestContents = append(requestContents, model.Content{
					Role:    model.RoleSystem,
					Content: strings.Join(systemParts, "\n\n"),
				})
			}
			requestContents = append(requestContents, cloneContents(conversation)...)
			req := &model.LLMRequest{
				Model:    modelName,
				Contents: requestContents,
				Tools:    append([]tool.Tool(nil), a.config.Tools...),
			}
			call := &LLMCall{
				AgentName:      a.Name(),
				Iteration:      iteration,
				Request:        req,
				GenerateConfig: a.config.GenerateConfig,
				Stream:         a.config.Stream,
			}
			llmCtx, llmSpan := adktrace.Start(iterationCtx, adktrace.Event{
				Kind:      adktrace.KindLLMCall,
				AgentName: a.Name(),
				Model:     modelName,
				Iteration: iteration,
				Stream:    a.config.Stream,
			})
			llmStartedAt := time.Now()
			llmEnd := adktrace.Event{
				Kind:      adktrace.KindLLMCall,
				AgentName: a.Name(),
				Model:     req.Model,
				Iteration: iteration,
				Stream:    a.config.Stream,
			}
			skipResp, err := a.beforeLLMCall(llmCtx, call)
			if err != nil {
				llmEnd.Err = err
				llmEnd.Duration = time.Since(llmStartedAt)
				llmSpan.End(llmCtx, llmEnd)
				iterationEnd.Err = err
				iterationSpan.End(iterationCtx, iterationEnd)
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
				llmEnd.Attributes = map[string]any{"skipped": true}
			} else {
				for resp, err := range a.config.Model.GenerateContent(llmCtx, req, a.config.GenerateConfig, a.config.Stream) {
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

			duration := time.Since(startedAt)
			llmEnd.Duration = time.Since(llmStartedAt)
			llmEnd.Err = llmErr
			llmEnd.PartialResponses = partialResponses
			llmEnd.StoppedEarly = stoppedEarly
			if completeResp != nil {
				llmEnd.FinishReason = completeResp.FinishReason
				addUsageToTraceEvent(&llmEnd, completeResp.Usage)
				iterationEnd.FinishReason = completeResp.FinishReason
				addUsageToTraceEvent(&iterationEnd, completeResp.Usage)
			}
			if err := a.afterLLMCall(llmCtx, call, &LLMCallResult{
				Response:         completeResp,
				Err:              llmErr,
				PartialResponses: partialResponses,
				Duration:         duration,
				StoppedEarly:     stoppedEarly,
			}); err != nil {
				llmEnd.Err = err
				llmSpan.End(llmCtx, llmEnd)
				iterationEnd.Err = err
				iterationSpan.End(iterationCtx, iterationEnd)
				yield(nil, err)
				return
			}
			llmSpan.End(llmCtx, llmEnd)

			if llmErr != nil {
				iterationEnd.Err = llmErr
				iterationSpan.End(iterationCtx, iterationEnd)
				yield(nil, llmErr)
				return
			}

			if stoppedEarly {
				iterationEnd.StoppedEarly = true
				iterationSpan.End(iterationCtx, iterationEnd)
				return
			}

			if completeResp == nil {
				iterationSpan.End(iterationCtx, iterationEnd)
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
				iterationEnd.StoppedEarly = true
				iterationSpan.End(iterationCtx, iterationEnd)
				return
			}
			conversation = append(conversation, cloneContent(completeResp.Content))

			// No tool calls — generation is complete.
			if completeResp.FinishReason != model.FinishReasonToolCalls {
				iterationSpan.End(iterationCtx, iterationEnd)
				return
			}

			// Execute all tool calls in parallel, preserving original order.
			toolCtx, cancelTools := context.WithCancel(iterationCtx)
			toolEvents := make([]model.Event, len(completeResp.Content.ToolCalls))
			toolErrs := make([]error, len(completeResp.Content.ToolCalls))
			var wg sync.WaitGroup
			for i, tc := range completeResp.Content.ToolCalls {
				wg.Add(1)
				go func(i int, tc model.ToolCall) {
					defer wg.Done()
					toolEvent, err := a.runToolCall(toolCtx, cancelTools, iteration, i, tc)
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

			if err := firstToolCallError(toolErrs); err != nil {
				iterationEnd.Err = err
				iterationSpan.End(iterationCtx, iterationEnd)
				yield(nil, err)
				return
			}

			for _, toolEvent := range toolEvents {
				toolContent := cloneContent(toolEvent.Content)
				if !yield(&toolEvent, nil) {
					iterationEnd.StoppedEarly = true
					iterationSpan.End(iterationCtx, iterationEnd)
					return
				}
				conversation = append(conversation, toolContent)
			}
			iterationSpan.End(iterationCtx, iterationEnd)
		}
	}
}

// runToolCall executes a single tool call and returns the resulting tool event.
// It cancels the parallel tool batch before invoking hooks for an execution
// failure so hook latency cannot delay sibling cancellation.
func (a *LlmAgent) runToolCall(ctx context.Context, cancelTools context.CancelFunc, iteration, toolIndex int, tc model.ToolCall) (event model.Event, err error) {
	ctx, toolSpan := adktrace.Start(ctx, adktrace.Event{
		Kind:       adktrace.KindToolCall,
		AgentName:  a.Name(),
		Iteration:  iteration,
		ToolName:   tc.Name,
		ToolCallID: tc.ID,
		ToolIndex:  toolIndex,
	})
	toolEnd := adktrace.Event{
		Kind:       adktrace.KindToolCall,
		AgentName:  a.Name(),
		Iteration:  iteration,
		ToolName:   tc.Name,
		ToolCallID: tc.ID,
		ToolIndex:  toolIndex,
	}
	defer func() {
		if event.Content.Role != "" {
			toolEnd.EventAuthor = event.Author
			toolEnd.EventRole = event.Content.Role
			if event.Content.ToolResult != nil {
				toolEnd.IsError = event.Content.ToolResult.IsError
			}
		}
		if err != nil {
			toolEnd.Err = err
		}
		toolSpan.End(ctx, toolEnd)
	}()

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
		toolEnd.Attributes = map[string]any{"skipped": true}
		toolEnd.Duration = time.Since(startedAt)
		if skipResult.Err != nil {
			executionErr := fmt.Errorf("llmagent: skip tool %q: %w", tc.Name, skipResult.Err)
			callResult := &ToolCallResult{
				Err:      executionErr,
				Duration: toolEnd.Duration,
			}
			cancelTools()
			return model.Event{}, joinAfterToolCallError(tc.Name, executionErr, a.afterToolCall(ctx, call, callResult))
		}
		toolEnd.IsError = skipResult.Result.IsError
		if hookErr := a.afterToolCall(ctx, call, skipResult); hookErr != nil {
			return model.Event{}, joinAfterToolCallError(tc.Name, nil, hookErr)
		}
		return skipResult.Event, nil
	}

	if !ok {
		toolErr := &ToolNotFoundError{Name: tc.Name}
		result := tool.Result{
			Content: toolErr.Error(),
			IsError: true,
		}
		event := model.Event{
			Author:  tc.Name,
			Content: toolResultContent(tc, result),
		}
		if err := a.afterToolCall(ctx, call, &ToolCallResult{
			Event:    event,
			Result:   result,
			Duration: time.Since(startedAt),
		}); err != nil {
			return model.Event{}, joinAfterToolCallError(tc.Name, nil, err)
		}
		toolEnd.Duration = time.Since(startedAt)
		toolEnd.IsError = true
		return event, nil
	}

	result, toolErr := t.Run(ctx, tool.Call{
		ID:        tc.ID,
		Name:      tc.Name,
		Arguments: toolCallArguments(tc.Arguments),
	})
	if toolErr != nil {
		toolEnd.Duration = time.Since(startedAt)
		executionErr := fmt.Errorf("llmagent: run tool %q: %w", tc.Name, toolErr)
		callResult := &ToolCallResult{
			Err:      executionErr,
			Duration: toolEnd.Duration,
		}
		cancelTools()
		return model.Event{}, joinAfterToolCallError(tc.Name, executionErr, a.afterToolCall(ctx, call, callResult))
	}
	event = model.Event{
		Author:  t.Definition().Name,
		Content: toolResultContent(tc, result),
	}
	if hookErr := a.afterToolCall(ctx, call, &ToolCallResult{
		Event:    event,
		Result:   result,
		Duration: time.Since(startedAt),
	}); hookErr != nil {
		return model.Event{}, joinAfterToolCallError(tc.Name, nil, hookErr)
	}
	toolEnd.Duration = time.Since(startedAt)
	toolEnd.IsError = result.IsError
	return event, nil
}

func firstToolCallError(errs []error) error {
	var firstErr error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		if !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return firstErr
}

func joinAfterToolCallError(toolName string, executionErr, hookErr error) error {
	if hookErr == nil {
		return executionErr
	}
	hookErr = fmt.Errorf("llmagent: after tool %q: %w", toolName, hookErr)
	if executionErr == nil {
		return hookErr
	}
	return errors.Join(executionErr, hookErr)
}

func toolCallArguments(args []byte) []byte {
	if len(args) == 0 {
		return []byte("{}")
	}
	return args
}

func addUsageToTraceEvent(event *adktrace.Event, usage *model.TokenUsage) {
	if usage == nil {
		return
	}
	event.PromptTokens = usage.PromptTokens
	event.CompletionTokens = usage.CompletionTokens
	event.TotalTokens = usage.TotalTokens
}

func toolResultContent(tc model.ToolCall, result tool.Result) model.Content {
	content := result.Content
	if content == "" && len(result.StructuredContent) > 0 {
		content = string(result.StructuredContent)
	}
	return model.Content{
		Role:       model.RoleTool,
		Content:    content,
		ToolCallID: tc.ID,
		ToolResult: &model.ToolResult{
			ToolCallID:        tc.ID,
			Name:              tc.Name,
			Content:           content,
			StructuredContent: result.StructuredContent,
			IsError:           result.IsError,
		},
	}
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
