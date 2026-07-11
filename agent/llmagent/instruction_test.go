package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/tool"
	adktrace "github.com/soasurs/adk/trace"
)

type instructionScriptLLM struct {
	name      string
	mu        sync.Mutex
	calls     [][]*model.LLMResponse
	requests  []*model.LLMRequest
	callCount int
	onCall    func(int)
}

func (m *instructionScriptLLM) Name() string { return m.name }

func (m *instructionScriptLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	callIndex := m.callCount
	m.callCount++
	m.requests = append(m.requests, &model.LLMRequest{
		Model:    req.Model,
		Contents: cloneContents(req.Contents),
		Tools:    append([]tool.Tool(nil), req.Tools...),
	})
	var responses []*model.LLMResponse
	if callIndex < len(m.calls) {
		responses = m.calls[callIndex]
	}
	m.mu.Unlock()
	if m.onCall != nil {
		m.onCall(callIndex)
	}

	return func(yield func(*model.LLMResponse, error) bool) {
		if responses == nil {
			yield(nil, fmt.Errorf("instructionScriptLLM: no responses for call %d", callIndex))
			return
		}
		for _, response := range responses {
			if !yield(response, nil) {
				return
			}
		}
	}
}

func (m *instructionScriptLLM) snapshot() (int, []*model.LLMRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount, append([]*model.LLMRequest(nil), m.requests...)
}

type instructionTool struct {
	name string
	run  func(context.Context, tool.Call) (tool.Result, error)
}

func (t *instructionTool) Definition() tool.Definition {
	return tool.Definition{Name: t.name, Description: "instruction provider test tool"}
}

func (t *instructionTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	return t.run(ctx, call)
}

func instructionToolResponse(calls ...model.ToolCall) *model.LLMResponse {
	return &model.LLMResponse{
		Content: model.Content{
			Role:      model.RoleAssistant,
			ToolCalls: calls,
		},
		FinishReason: model.FinishReasonToolCalls,
	}
}

func instructionStopResponse(content string) *model.LLMResponse {
	return &model.LLMResponse{
		Content:      model.Content{Role: model.RoleAssistant, Content: content},
		FinishReason: model.FinishReasonStop,
	}
}

func runInstructionAgent(t *testing.T, a *LlmAgent, contents []model.Content) ([]model.Event, error) {
	t.Helper()
	var events []model.Event
	for event, err := range a.Run(t.Context(), model.EventsFromContents(contents)) {
		if err != nil {
			return events, err
		}
		events = append(events, *event)
	}
	return events, nil
}

func TestInstructionProvider_NilPreservesStaticInstructionStreamingAndToolLoop(t *testing.T) {
	t.Parallel()

	echo := &instructionTool{name: "echo", run: func(context.Context, tool.Call) (tool.Result, error) {
		return tool.Result{Content: "echoed"}, nil
	}}
	llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{
		{
			{Content: model.Content{Role: model.RoleAssistant, Content: "thinking"}, Partial: true},
			instructionToolResponse(model.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)}),
		},
		{instructionStopResponse("done")},
	}}
	a := New(Config{
		Name:        "agent",
		Model:       llm,
		Tools:       []tool.Tool{echo},
		Instruction: "static",
		Stream:      true,
	}).(*LlmAgent)

	events, err := runInstructionAgent(t, a, []model.Content{
		{Role: model.RoleSystem, Content: "ignored system event"},
		{Role: model.RoleUser, Content: "hello"},
	})

	require.NoError(t, err)
	require.Len(t, events, 4)
	assert.True(t, events[0].Partial)
	assert.Equal(t, []model.Role{model.RoleAssistant, model.RoleTool, model.RoleAssistant}, []model.Role{
		events[1].Content.Role, events[2].Content.Role, events[3].Content.Role,
	})
	callCount, requests := llm.snapshot()
	assert.Equal(t, 2, callCount)
	require.Len(t, requests, 2)
	assert.Equal(t, "static", requests[0].Contents[0].Content)
	assert.Equal(t, model.RoleUser, requests[0].Contents[1].Role)
	assert.Equal(t, "static", requests[1].Contents[0].Content)
	require.Len(t, requests[1].Contents, 4)
	assert.Equal(t, model.RoleAssistant, requests[1].Contents[2].Role)
	assert.Equal(t, model.RoleTool, requests[1].Contents[3].Role)
}

func TestInstructionProvider_OncePerInvocationAndMaxIterations(t *testing.T) {
	t.Parallel()

	var iterations []int
	var mu sync.Mutex
	provider := func(_ context.Context, input InstructionInput) (string, error) {
		mu.Lock()
		iterations = append(iterations, input.Iteration)
		mu.Unlock()
		return fmt.Sprintf("dynamic-%d", input.Iteration), nil
	}
	missingCall := model.ToolCall{ID: "missing", Name: "missing", Arguments: json.RawMessage(`{}`)}
	llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{
		{
			{Content: model.Content{Role: model.RoleAssistant, Content: "a"}, Partial: true},
			{Content: model.Content{Role: model.RoleAssistant, Content: "b"}, Partial: true},
			instructionToolResponse(missingCall),
		},
		{instructionToolResponse(missingCall)},
	}}
	a := New(Config{
		Name:                "agent",
		Model:               llm,
		InstructionProvider: provider,
		MaxIterations:       2,
		Stream:              true,
	}).(*LlmAgent)

	_, err := runInstructionAgent(t, a, []model.Content{{Role: model.RoleUser, Content: "go"}})

	require.ErrorIs(t, err, ErrMaxIterationsExceeded)
	assert.Equal(t, []int{1, 2}, iterations)
	callCount, _ := llm.snapshot()
	assert.Equal(t, 2, callCount)
}

func TestInstructionProvider_SystemMessageOrderAndEmptyOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		dynamic  string
		expected string
	}{
		{name: "dynamic instruction", dynamic: "dynamic", expected: "static\n\ndynamic"},
		{name: "empty dynamic instruction", dynamic: "", expected: "static"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{{instructionStopResponse("done")}}}
			a := New(Config{
				Name:        "agent",
				Model:       llm,
				Instruction: "static",
				InstructionProvider: func(context.Context, InstructionInput) (string, error) {
					return tc.dynamic, nil
				},
			}).(*LlmAgent)

			_, err := runInstructionAgent(t, a, []model.Content{
				{Role: model.RoleSystem, Content: "ignored system event"},
				{Role: model.RoleUser, Content: "go"},
			})

			require.NoError(t, err)
			_, requests := llm.snapshot()
			require.Len(t, requests, 1)
			require.Len(t, requests[0].Contents, 2)
			assert.Equal(t, model.RoleSystem, requests[0].Contents[0].Role)
			assert.Equal(t, tc.expected, requests[0].Contents[0].Content)
		})
	}
}

func TestInstructionProvider_SeesStateAfterToolHooksWithoutStaleInstruction(t *testing.T) {
	t.Parallel()

	var state atomic.Int64
	var providerConversations [][]model.Content
	var providerMu sync.Mutex
	stateTool := &instructionTool{name: "state", run: func(context.Context, tool.Call) (tool.Result, error) {
		return tool.Result{Content: "updated"}, nil
	}}
	llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{
		{instructionToolResponse(model.ToolCall{ID: "state-1", Name: "state", Arguments: json.RawMessage(`{}`)})},
		{instructionStopResponse("done")},
	}}
	a := New(Config{
		Name:        "agent",
		Model:       llm,
		Tools:       []tool.Tool{stateTool},
		Instruction: "static",
		InstructionProvider: func(_ context.Context, input InstructionInput) (string, error) {
			providerMu.Lock()
			providerConversations = append(providerConversations, cloneContents(input.Conversation))
			providerMu.Unlock()
			return fmt.Sprintf("state-%d", state.Load()), nil
		},
		AfterToolCall: func(context.Context, *ToolCall, *ToolCallResult) error {
			state.Store(1)
			return nil
		},
	}).(*LlmAgent)

	_, err := runInstructionAgent(t, a, []model.Content{{Role: model.RoleUser, Content: "update"}})

	require.NoError(t, err)
	_, requests := llm.snapshot()
	require.Len(t, requests, 2)
	assert.Equal(t, "static\n\nstate-0", requests[0].Contents[0].Content)
	assert.Equal(t, "static\n\nstate-1", requests[1].Contents[0].Content)
	assert.NotContains(t, requests[1].Contents[0].Content, "state-0")
	require.Len(t, providerConversations, 2)
	assert.Len(t, providerConversations[0], 1)
	require.Len(t, providerConversations[1], 3)
	assert.Equal(t, []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool}, []model.Role{
		providerConversations[1][0].Role,
		providerConversations[1][1].Role,
		providerConversations[1][2].Role,
	})
}

func TestInstructionProvider_OrderingWaitsForParallelToolsAndHooks(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var order []string
	appendOrder := func(value string) {
		mu.Lock()
		order = append(order, value)
		mu.Unlock()
	}
	secondStarted := make(chan struct{})
	first := &instructionTool{name: "first", run: func(context.Context, tool.Call) (tool.Result, error) {
		<-secondStarted
		appendOrder("tool-0")
		return tool.Result{Content: "zero"}, nil
	}}
	second := &instructionTool{name: "second", run: func(context.Context, tool.Call) (tool.Result, error) {
		close(secondStarted)
		appendOrder("tool-1")
		return tool.Result{Content: "one"}, nil
	}}
	llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{
		{instructionToolResponse(
			model.ToolCall{ID: "call-0", Name: "first", Arguments: json.RawMessage(`{}`)},
			model.ToolCall{ID: "call-1", Name: "second", Arguments: json.RawMessage(`{}`)},
		)},
		{instructionStopResponse("done")},
	}, onCall: func(index int) { appendOrder(fmt.Sprintf("model-%d", index+1)) }}
	a := New(Config{
		Name:  "agent",
		Model: llm,
		Tools: []tool.Tool{first, second},
		InstructionProvider: func(_ context.Context, input InstructionInput) (string, error) {
			appendOrder(fmt.Sprintf("provider-%d", input.Iteration))
			if input.Iteration == 2 {
				assert.Equal(t, 2, countOrderPrefix(order, "after-tool-"))
			}
			return "dynamic", nil
		},
		BeforeLLMCall: func(_ context.Context, call *LLMCall) (*model.LLMResponse, error) {
			appendOrder(fmt.Sprintf("before-llm-%d", call.Iteration))
			return nil, nil
		},
		AfterLLMCall: func(_ context.Context, call *LLMCall, _ *LLMCallResult) error {
			appendOrder(fmt.Sprintf("after-llm-%d", call.Iteration))
			return nil
		},
		BeforeToolCall: func(_ context.Context, call *ToolCall) (*ToolCallResult, error) {
			appendOrder(fmt.Sprintf("before-tool-%d", call.ToolIndex))
			return nil, nil
		},
		AfterToolCall: func(_ context.Context, call *ToolCall, _ *ToolCallResult) error {
			appendOrder(fmt.Sprintf("after-tool-%d", call.ToolIndex))
			return nil
		},
	}).(*LlmAgent)

	events, err := runInstructionAgent(t, a, []model.Content{{Role: model.RoleUser, Content: "run"}})

	require.NoError(t, err)
	require.Len(t, events, 4)
	assert.Equal(t, "call-0", events[1].Content.ToolCallID)
	assert.Equal(t, "call-1", events[2].Content.ToolCallID)
	mu.Lock()
	defer mu.Unlock()
	assert.Less(t, indexOfOrder(order, "provider-1"), indexOfOrder(order, "before-llm-1"))
	assert.Less(t, indexOfOrder(order, "before-llm-1"), indexOfOrder(order, "model-1"))
	assert.Less(t, indexOfOrder(order, "model-1"), indexOfOrder(order, "after-llm-1"))
	assert.Less(t, indexOfOrder(order, "after-tool-0"), indexOfOrder(order, "provider-2"))
	assert.Less(t, indexOfOrder(order, "after-tool-1"), indexOfOrder(order, "provider-2"))
	assert.Less(t, indexOfOrder(order, "provider-2"), indexOfOrder(order, "before-llm-2"))
}

func countOrderPrefix(order []string, prefix string) int {
	count := 0
	for _, item := range order {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			count++
		}
	}
	return count
}

func indexOfOrder(order []string, target string) int {
	for i, item := range order {
		if item == target {
			return i
		}
	}
	return -1
}

type instructionProviderError struct {
	code int
}

func (e *instructionProviderError) Error() string { return fmt.Sprintf("provider code %d", e.code) }

func TestInstructionProvider_ErrorWrapsAndStopsBeforeHookAndModel(t *testing.T) {
	t.Parallel()

	providerErr := &instructionProviderError{code: 42}
	var beforeCalls atomic.Int64
	llm := &instructionScriptLLM{name: "never"}
	tracer := new(recordingTraceTracer)
	a := New(Config{
		Name:  "agent",
		Model: llm,
		InstructionProvider: func(context.Context, InstructionInput) (string, error) {
			return "", providerErr
		},
		BeforeLLMCall: func(context.Context, *LLMCall) (*model.LLMResponse, error) {
			beforeCalls.Add(1)
			return nil, nil
		},
	}).(*LlmAgent)
	ctx := adktrace.ContextWithTracer(t.Context(), tracer)

	var runErr error
	for _, err := range a.Run(ctx, model.EventsFromContents([]model.Content{{Role: model.RoleUser, Content: "go"}})) {
		runErr = err
	}

	require.ErrorIs(t, runErr, providerErr)
	var typedErr *instructionProviderError
	require.ErrorAs(t, runErr, &typedErr)
	assert.Equal(t, 42, typedErr.code)
	assert.EqualError(t, runErr, "llmagent: build instruction: provider code 42")
	assert.Zero(t, beforeCalls.Load())
	callCount, _ := llm.snapshot()
	assert.Zero(t, callCount)
	iterationEnd, ok := traceEndByKind(tracer, adktrace.KindLLMIteration)
	require.True(t, ok)
	assert.ErrorIs(t, iterationEnd.Err, providerErr)
}

func TestInstructionProvider_EarlyStopAndSkipDoNotStartAnotherIteration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		responses   [][]*model.LLMResponse
		before      func(context.Context, *LLMCall) (*model.LLMResponse, error)
		stopOnRole  model.Role
		stopPartial bool
	}{
		{
			name: "hook skip response",
			before: func(context.Context, *LLMCall) (*model.LLMResponse, error) {
				return instructionStopResponse("skipped"), nil
			},
		},
		{
			name: "partial event",
			responses: [][]*model.LLMResponse{{
				{Content: model.Content{Role: model.RoleAssistant, Content: "part"}, Partial: true},
				instructionStopResponse("complete"),
			}},
			stopPartial: true,
		},
		{
			name:       "complete assistant event",
			responses:  [][]*model.LLMResponse{{instructionStopResponse("complete")}},
			stopOnRole: model.RoleAssistant,
		},
		{
			name: "tool event",
			responses: [][]*model.LLMResponse{{instructionToolResponse(
				model.ToolCall{ID: "missing", Name: "missing", Arguments: json.RawMessage(`{}`)},
			)}},
			stopOnRole: model.RoleTool,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var providerCalls atomic.Int64
			llm := &instructionScriptLLM{name: "script", calls: tt.responses}
			a := New(Config{
				Name:          "agent",
				Model:         llm,
				BeforeLLMCall: tt.before,
				InstructionProvider: func(context.Context, InstructionInput) (string, error) {
					providerCalls.Add(1)
					return "dynamic", nil
				},
			}).(*LlmAgent)

			for event, err := range a.Run(t.Context(), model.EventsFromContents([]model.Content{{Role: model.RoleUser, Content: "go"}})) {
				require.NoError(t, err)
				if (tt.stopPartial && event.Partial) || (tt.stopOnRole != "" && event.Content.Role == tt.stopOnRole) {
					break
				}
			}

			assert.Equal(t, int64(1), providerCalls.Load())
		})
	}
}

func TestInstructionProvider_ProviderAndHookCannotMutateCanonicalConversation(t *testing.T) {
	t.Parallel()

	original := model.Content{
		Role:  model.RoleUser,
		Parts: []model.ContentPart{{Type: model.ContentPartTypeText, Text: "original part"}},
		ToolCalls: []model.ToolCall{{
			ID:               "original-call",
			Name:             "original-tool",
			Arguments:        json.RawMessage(`{"value":"original"}`),
			ThoughtSignature: []byte("original-signature"),
		}},
		ToolResult: &model.ToolResult{
			ToolCallID:        "original-call",
			StructuredContent: json.RawMessage(`{"result":"original"}`),
		},
	}
	var secondInput []model.Content
	llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{
		{instructionToolResponse(model.ToolCall{ID: "missing", Name: "missing", Arguments: json.RawMessage(`{"round":1}`), ThoughtSignature: []byte("round-1")})},
		{instructionStopResponse("done")},
	}}
	unusedTool := &instructionTool{name: "unused", run: func(context.Context, tool.Call) (tool.Result, error) {
		return tool.Result{}, nil
	}}
	a := New(Config{
		Name:  "agent",
		Model: llm,
		Tools: []tool.Tool{unusedTool},
		InstructionProvider: func(_ context.Context, input InstructionInput) (string, error) {
			if input.Iteration == 1 {
				input.Conversation[0].Parts[0].Text = "provider mutation"
				input.Conversation[0].ToolCalls[0].Arguments[0] = 'x'
				input.Conversation[0].ToolCalls[0].ThoughtSignature[0] = 'x'
				input.Conversation[0].ToolResult.StructuredContent[0] = 'x'
			} else {
				secondInput = cloneContents(input.Conversation)
			}
			return "dynamic", nil
		},
		BeforeLLMCall: func(_ context.Context, call *LLMCall) (*model.LLMResponse, error) {
			originalRequest := call.Request
			require.Equal(t, "original part", originalRequest.Contents[1].Parts[0].Text)
			require.Len(t, originalRequest.Tools, 1)
			require.NotNil(t, originalRequest.Tools[0])
			originalRequest.Contents[1].Parts[0].Text = fmt.Sprintf("hook mutation %d", call.Iteration)
			originalRequest.Contents[1].ToolCalls[0].Arguments[0] = 'y'
			originalRequest.Contents[1].ToolCalls[0].ThoughtSignature[0] = 'y'
			originalRequest.Contents[1].ToolResult.StructuredContent[0] = 'y'
			originalRequest.Tools[0] = nil
			originalRequest.Model = fmt.Sprintf("iteration-%d", call.Iteration)
			call.Request = &model.LLMRequest{Model: "unsupported replacement"}
			return nil, nil
		},
	}).(*LlmAgent)

	_, err := runInstructionAgent(t, a, []model.Content{original})

	require.NoError(t, err)
	require.Len(t, secondInput, 3)
	assert.Equal(t, "original part", secondInput[0].Parts[0].Text)
	assert.JSONEq(t, `{"value":"original"}`, string(secondInput[0].ToolCalls[0].Arguments))
	assert.Equal(t, []byte("original-signature"), secondInput[0].ToolCalls[0].ThoughtSignature)
	assert.JSONEq(t, `{"result":"original"}`, string(secondInput[0].ToolResult.StructuredContent))
	_, requests := llm.snapshot()
	require.Len(t, requests, 2)
	assert.Equal(t, "iteration-1", requests[0].Model)
	assert.Equal(t, "iteration-2", requests[1].Model)
	assert.Equal(t, "hook mutation 1", requests[0].Contents[1].Parts[0].Text)
	assert.Equal(t, "hook mutation 2", requests[1].Contents[1].Parts[0].Text)
	assert.Nil(t, requests[0].Tools[0])
	assert.Nil(t, requests[1].Tools[0])
	assert.Equal(t, original.Parts[0].Text, "original part")
	assert.JSONEq(t, `{"value":"original"}`, string(original.ToolCalls[0].Arguments))
}

func TestInstructionProvider_ToolFailureControlsNextInvocation(t *testing.T) {
	t.Parallel()

	t.Run("handled failure continues", func(t *testing.T) {
		var providerCalls atomic.Int64
		handled := &instructionTool{name: "lookup", run: func(context.Context, tool.Call) (tool.Result, error) {
			return tool.Result{Content: "not found", IsError: true}, nil
		}}
		llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{
			{instructionToolResponse(model.ToolCall{ID: "lookup-1", Name: "lookup", Arguments: json.RawMessage(`{}`)})},
			{instructionStopResponse("done")},
		}}
		a := New(Config{
			Name:  "agent",
			Model: llm,
			Tools: []tool.Tool{handled},
			InstructionProvider: func(context.Context, InstructionInput) (string, error) {
				providerCalls.Add(1)
				return "dynamic", nil
			},
		}).(*LlmAgent)

		_, err := runInstructionAgent(t, a, []model.Content{{Role: model.RoleUser, Content: "go"}})

		require.NoError(t, err)
		assert.Equal(t, int64(2), providerCalls.Load())
	})

	for _, tc := range []struct {
		name       string
		toolErr    error
		afterError error
	}{
		{name: "terminal tool error", toolErr: errors.New("tool failed")},
		{name: "terminal hook error", afterError: errors.New("hook failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var providerCalls atomic.Int64
			terminal := &instructionTool{name: "lookup", run: func(context.Context, tool.Call) (tool.Result, error) {
				return tool.Result{}, tc.toolErr
			}}
			llm := &instructionScriptLLM{name: "script", calls: [][]*model.LLMResponse{
				{instructionToolResponse(model.ToolCall{ID: "lookup-1", Name: "lookup", Arguments: json.RawMessage(`{}`)})},
				{instructionStopResponse("must not run")},
			}}
			a := New(Config{
				Name:  "agent",
				Model: llm,
				Tools: []tool.Tool{terminal},
				InstructionProvider: func(context.Context, InstructionInput) (string, error) {
					providerCalls.Add(1)
					return "dynamic", nil
				},
				AfterToolCall: func(context.Context, *ToolCall, *ToolCallResult) error {
					return tc.afterError
				},
			}).(*LlmAgent)

			_, err := runInstructionAgent(t, a, []model.Content{{Role: model.RoleUser, Content: "go"}})

			require.Error(t, err)
			assert.Equal(t, int64(1), providerCalls.Load())
			callCount, _ := llm.snapshot()
			assert.Equal(t, 1, callCount)
		})
	}
}

type concurrentInstructionLLM struct {
	calls atomic.Int64
}

func (m *concurrentInstructionLLM) Name() string { return "concurrent" }

func (m *concurrentInstructionLLM) GenerateContent(context.Context, *model.LLMRequest, *model.GenerateConfig, bool) iter.Seq2[*model.LLMResponse, error] {
	m.calls.Add(1)
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(instructionStopResponse("done"), nil)
	}
}

func TestInstructionProvider_ConcurrentRunsShareStatelessProvider(t *testing.T) {
	const runs = 32
	var providerCalls atomic.Int64
	provider := func(_ context.Context, input InstructionInput) (string, error) {
		providerCalls.Add(1)
		return fmt.Sprintf("agent=%s iteration=%d messages=%d", input.AgentName, input.Iteration, len(input.Conversation)), nil
	}
	llm := new(concurrentInstructionLLM)
	a := New(Config{Name: "agent", Model: llm, InstructionProvider: provider}).(*LlmAgent)

	var wg sync.WaitGroup
	errs := make(chan error, runs)
	for i := range runs {
		wg.Go(func() {
			for _, err := range a.Run(t.Context(), model.EventsFromContents([]model.Content{{Role: model.RoleUser, Content: fmt.Sprintf("run-%d", i)}})) {
				if err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int64(runs), providerCalls.Load())
	assert.Equal(t, int64(runs), llm.calls.Load())
}
