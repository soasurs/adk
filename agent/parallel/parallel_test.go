package parallel

import (
	"context"
	"fmt"
	"iter"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"soasurs.dev/soasurs/adk/agent"
	"soasurs.dev/soasurs/adk/agent/llmagent"
	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/model/openai"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mockLLM replays a fixed sequence of responses for deterministic unit tests.
type mockLLM struct {
	name      string
	responses []*model.LLMResponse
	callIdx   int
}

func (m *mockLLM) Name() string { return m.name }

func (m *mockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.callIdx >= len(m.responses) {
			yield(nil, fmt.Errorf("mockLLM %q: no more responses (call %d)", m.name, m.callIdx))
			return
		}
		resp := m.responses[m.callIdx]
		m.callIdx++
		yield(resp, nil)
	}
}

// blockingMockLLM signals on ready when Generate is called, then waits for
// the gate channel to be closed before returning. Used to verify true parallelism.
type blockingMockLLM struct {
	name     string
	ready    chan<- struct{}
	gate     <-chan struct{}
	response *model.LLMResponse
}

func (b *blockingMockLLM) Name() string { return b.name }

func (b *blockingMockLLM) GenerateContent(ctx context.Context, _ *model.LLMRequest, _ *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// Signal that this goroutine has started.
		b.ready <- struct{}{}
		// Wait until the test releases all agents, or the context is cancelled.
		select {
		case <-b.gate:
			yield(b.response, nil)
		case <-ctx.Done():
			yield(nil, ctx.Err())
		}
	}
}

// newLLMFromEnv creates a real LLM from environment variables.
// Required: OPENAI_API_KEY — test is skipped when absent.
// Optional: OPENAI_BASE_URL — overrides the default OpenAI endpoint.
// Optional: OPENAI_MODEL   — model name; defaults to "gpt-4o-mini" when absent.
func newLLMFromEnv(t *testing.T) model.LLM {
	t.Helper()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}
	return openai.New(apiKey, baseURL, modelName)
}

// logMessage prints a single message in a concise one-line format.
func logMessage(t *testing.T, idx int, m model.Message) {
	t.Helper()
	if len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			t.Logf("  [%d] %-9s tool_call  name=%s args=%s", idx, m.Role, tc.Name, tc.Arguments)
		}
		return
	}
	if m.ToolCallID != "" {
		t.Logf("  [%d] %-9s result     id=%s content=%s", idx, m.Role, m.ToolCallID, m.Content)
		return
	}
	t.Logf("  [%d] %-9s %s", idx, m.Role, m.Content)
}

// ---------------------------------------------------------------------------
// Unit tests (no API required)
// ---------------------------------------------------------------------------

// TestParallelAgent_Name verifies that Name and Description are forwarded.
func TestParallelAgent_Name(t *testing.T) {
	a1 := llmagent.New(llmagent.Config{Name: "a1", Description: "first", Model: &mockLLM{name: "m1"}})
	pa := New(Config{Name: "my-fanout", Description: "a test fanout", Agents: []agent.Agent{a1}})
	assert.Equal(t, "my-fanout", pa.Name())
	assert.Equal(t, "a test fanout", pa.Description())
}

// TestParallelAgent_PanicOnEmpty verifies that New panics with no agents.
func TestParallelAgent_PanicOnEmpty(t *testing.T) {
	assert.Panics(t, func() {
		New(Config{Name: "empty", Description: "no agents"})
	})
}

// TestParallelAgent_SingleAgent verifies that wrapping a single agent yields
// exactly one merged assistant message whose content follows DefaultMergeFunc
// format: "[agent-name]\ncontent".
func TestParallelAgent_SingleAgent(t *testing.T) {
	llm := &mockLLM{
		name: "mock",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "Hello!"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	a := llmagent.New(llmagent.Config{Name: "solo", Description: "d", Model: llm})
	pa := New(Config{Name: "fanout", Description: "single-agent fanout", Agents: []agent.Agent{a}})

	var msgs []model.Message
	for event, err := range pa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "Hi"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, "[solo]\nHello!", msgs[0].Content)
}

// TestParallelAgent_TwoAgents_Merged verifies that two agents produce a single
// merged assistant message with both results in definition order.
func TestParallelAgent_TwoAgents_Merged(t *testing.T) {
	llm1 := &mockLLM{
		name: "mock-1",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "from-agent-1"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "from-agent-2"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	pa := New(Config{
		Name:        "fanout",
		Description: "two-agent fanout",
		Agents:      []agent.Agent{a1, a2},
	})

	var msgs []model.Message
	for event, err := range pa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "go"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	// Always exactly one merged message.
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.Equal(t, "[agent-1]\nfrom-agent-1\n\n[agent-2]\nfrom-agent-2", msgs[0].Content)
}

// TestParallelAgent_TrueParallelism verifies that agents actually execute
// concurrently rather than sequentially.
//
// Two blockingMockLLMs each signal "ready" on a shared channel, then wait for
// a gate channel to be closed. Both ready signals must arrive before the gate
// is opened, proving that both agents started before either finished.
func TestParallelAgent_TrueParallelism(t *testing.T) {
	ready := make(chan struct{}, 2)
	gate := make(chan struct{})

	resp := func(content string) *model.LLMResponse {
		return &model.LLMResponse{
			Message:      model.Message{Role: model.RoleAssistant, Content: content},
			FinishReason: model.FinishReasonStop,
		}
	}

	llm1 := &blockingMockLLM{name: "m1", ready: ready, gate: gate, response: resp("result-1")}
	llm2 := &blockingMockLLM{name: "m2", ready: ready, gate: gate, response: resp("result-2")}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	pa := New(Config{
		Name:        "fanout",
		Description: "parallelism test",
		Agents:      []agent.Agent{a1, a2},
	})

	// Run the parallel agent in a background goroutine.
	var collectedMsgs []model.Message
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event, err := range pa.Run(context.Background(), []model.Message{
			{Role: model.RoleUser, Content: "go"},
		}) {
			if err != nil {
				runErr = err
				return
			}
			if !event.Partial {
				collectedMsgs = append(collectedMsgs, event.Message)
			}
		}
	}()

	// Both agents must signal "ready" before either returns — proving true
	// parallelism. If agents ran sequentially, the second ready signal would
	// never arrive while the first agent is blocked on the gate.
	timeout := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case <-timeout:
			t.Fatalf("agent %d did not start within 2s — agents may not be running in parallel", i+1)
		}
	}

	// Release both agents to complete.
	close(gate)
	<-done

	require.NoError(t, runErr)
	// Always one merged message; content contains both results in definition order.
	require.Len(t, collectedMsgs, 1)
	assert.Contains(t, collectedMsgs[0].Content, "result-1")
	assert.Contains(t, collectedMsgs[0].Content, "result-2")
}

// TestParallelAgent_EarlyStop verifies that breaking out of the iterator after
// the merged message is received stops further iteration cleanly.
func TestParallelAgent_EarlyStop(t *testing.T) {
	llm1 := &mockLLM{
		name: "mock-1",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "first"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "second"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	pa := New(Config{
		Name:        "fanout",
		Description: "early-stop test",
		Agents:      []agent.Agent{a1, a2},
	})

	var msgs []model.Message
	for event, err := range pa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "go"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
		break // stop after the first (and only) merged message
	}

	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "first")
}

// TestParallelAgent_ErrorPropagation verifies that when one agent errors,
// the error is yielded and other agents are cancelled via context.
func TestParallelAgent_ErrorPropagation(t *testing.T) {
	// llm1 has no responses and will immediately error.
	llm1 := &mockLLM{name: "mock-1"}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "from-agent-2"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	pa := New(Config{
		Name:        "fanout",
		Description: "error propagation test",
		Agents:      []agent.Agent{a1, a2},
	})

	var gotErr error
	for _, err := range pa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "go"},
	}) {
		if err != nil {
			gotErr = err
			break
		}
	}

	require.Error(t, gotErr, "expected an error from agent-1")
}

// TestParallelAgent_CustomMergeFunc verifies that a caller-provided MergeFunc
// fully controls the shape of the merged output message.
func TestParallelAgent_CustomMergeFunc(t *testing.T) {
	llm1 := &mockLLM{
		name: "mock-1",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "answer-1"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "answer-2"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})

	// Custom merger: concatenate contents with " | " separator.
	customMerge := func(results []AgentOutput) model.Message {
		var texts []string
		for _, r := range results {
			for i := len(r.Messages) - 1; i >= 0; i-- {
				if r.Messages[i].Role == model.RoleAssistant && r.Messages[i].Content != "" {
					texts = append(texts, r.Messages[i].Content)
					break
				}
			}
		}
		joined := ""
		for i, t := range texts {
			if i > 0 {
				joined += " | "
			}
			joined += t
		}
		return model.Message{Role: model.RoleAssistant, Content: joined}
	}

	pa := New(Config{
		Name:        "fanout",
		Description: "custom merge test",
		Agents:      []agent.Agent{a1, a2},
		MergeFunc:   customMerge,
	})

	var msgs []model.Message
	for event, err := range pa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "go"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	require.Len(t, msgs, 1)
	assert.Equal(t, "answer-1 | answer-2", msgs[0].Content)
}

// TestParallelAgent_DefaultMergeFunc_OmitsEmptyAgents verifies that agents
// producing no assistant text are omitted from the merged output.
func TestParallelAgent_DefaultMergeFunc_OmitsEmptyAgents(t *testing.T) {
	// agent-1 produces no assistant content (tool-call only, never a text reply).
	// We simulate this with a mock that returns only a tool-call message.
	llm1 := &mockLLM{
		name: "mock-1",
		responses: []*model.LLMResponse{
			{
				// FinishReasonStop but empty content — simulates a no-text response.
				Message:      model.Message{Role: model.RoleAssistant, Content: ""},
				FinishReason: model.FinishReasonStop,
			},
		},
	}
	llm2 := &mockLLM{
		name: "mock-2",
		responses: []*model.LLMResponse{
			{
				Message:      model.Message{Role: model.RoleAssistant, Content: "only me"},
				FinishReason: model.FinishReasonStop,
			},
		},
	}

	a1 := llmagent.New(llmagent.Config{Name: "agent-1", Description: "first", Model: llm1})
	a2 := llmagent.New(llmagent.Config{Name: "agent-2", Description: "second", Model: llm2})
	pa := New(Config{
		Name:        "fanout",
		Description: "omit empty agents test",
		Agents:      []agent.Agent{a1, a2},
	})

	var msgs []model.Message
	for event, err := range pa.Run(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "go"},
	}) {
		require.NoError(t, err)
		if !event.Partial {
			msgs = append(msgs, event.Message)
		}
	}

	require.Len(t, msgs, 1)
	// agent-1 had no text; only agent-2's content should appear.
	assert.Equal(t, "[agent-2]\nonly me", msgs[0].Content)
	assert.NotContains(t, msgs[0].Content, "agent-1")
}

// ---------------------------------------------------------------------------
// Integration tests (require OPENAI_API_KEY)
// ---------------------------------------------------------------------------

// TestParallelAgent_Integration_FanOut runs two independent translation agents
// in parallel on the same input, verifying that:
//   - Both agents produce a non-empty result.
//   - The output is a single merged assistant message.
//   - Each agent's result appears in the merged content (attribution headers).
//
// Required env var: OPENAI_API_KEY
// Optional env vars: OPENAI_BASE_URL, OPENAI_MODEL
func TestParallelAgent_Integration_FanOut(t *testing.T) {
	llm := newLLMFromEnv(t)

	frenchAgent := llmagent.New(llmagent.Config{
		Name:        "french-translator",
		Description: "Translates text into French.",
		Model:       llm,
		Instruction: "You are a French translator. Translate the user's text into French. Reply with only the translation.",
	})

	spanishAgent := llmagent.New(llmagent.Config{
		Name:        "spanish-translator",
		Description: "Translates text into Spanish.",
		Model:       llm,
		Instruction: "You are a Spanish translator. Translate the user's text into Spanish. Reply with only the translation.",
	})

	fanout := New(Config{
		Name:        "multi-translator",
		Description: "Translates text into multiple languages in parallel.",
		Agents:      []agent.Agent{frenchAgent, spanishAgent},
	})

	input := []model.Message{
		{Role: model.RoleUser, Content: "Hello, world!"},
	}

	t.Log("=== input ===")
	logMessage(t, 0, input[0])
	t.Log("=== output ===")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var msgs []model.Message
	for event, err := range fanout.Run(ctx, input) {
		require.NoError(t, err)
		if event.Partial {
			continue
		}
		logMessage(t, len(msgs), event.Message)
		msgs = append(msgs, event.Message)
	}

	// Always exactly one merged assistant message.
	require.Len(t, msgs, 1, "expected a single merged message")
	assert.Equal(t, model.RoleAssistant, msgs[0].Role)
	assert.NotEmpty(t, msgs[0].Content)
	// Both translators' attribution headers should be present.
	assert.Contains(t, msgs[0].Content, "[french-translator]")
	assert.Contains(t, msgs[0].Content, "[spanish-translator]")
}
