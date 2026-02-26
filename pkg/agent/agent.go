package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
	"soasurs.dev/soasurs/adk/pkg/llm"
	"soasurs.dev/soasurs/adk/pkg/memory"
	"soasurs.dev/soasurs/adk/pkg/tool"
)

type Agent interface {
	Run(ctx context.Context, sessionID uuid.UUID, input string) (*RunResult, error)
	RunWithHistory(ctx context.Context, sessionID uuid.UUID, input string, maxHistory int) (*RunResult, error)
}

type RunResult struct {
	RunID     uuid.UUID
	Output    string
	ToolCalls []ToolCallResult
	Duration  time.Duration
}

type ToolCallResult struct {
	Name   string
	Args   map[string]any
	Result any
	Error  string
}

type Config struct {
	ID               string
	Name             string
	Description      string
	LLM              llm.Provider
	LLMOptions       []llm.Option
	ToolRegistry     *tool.Registry
	MaxIterations    int
	MaxHistory       int
	SystemPrompt     string
	MaxContextTokens int
	ContextStrategy  string
	SummaryInterval  int
	MemoryManager    *memory.Manager
}

type agent struct {
	config *Config
	store  storage.Store
}

func NewAgent(config *Config, store storage.Store) Agent {
	return &agent{
		config: config,
		store:  store,
	}
}

func (a *agent) Run(ctx context.Context, sessionID uuid.UUID, input string) (*RunResult, error) {
	return a.RunWithHistory(ctx, sessionID, input, a.config.MaxHistory)
}

func (a *agent) RunWithHistory(ctx context.Context, sessionID uuid.UUID, input string, maxHistory int) (*RunResult, error) {
	startTime := time.Now()

	run := &storage.Run{
		ID:        uuid.New(),
		SessionID: sessionID,
		Status:    storage.RunStatusRunning,
		Input:     input,
		StartedAt: ptrTime(startTime),
		CreatedAt: startTime,
	}

	if err := a.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}

	result, err := a.execute(ctx, sessionID, input, maxHistory, run.ID)

	run.Output = result.Output
	run.CompletedAt = ptrTime(time.Now())
	if err != nil {
		run.Status = storage.RunStatusFailed
		run.Error = err.Error()
	} else {
		run.Status = storage.RunStatusCompleted
	}

	if updateErr := a.store.UpdateRun(ctx, run); updateErr != nil {
		return result, updateErr
	}

	result.Duration = time.Since(startTime)
	return result, err
}

func (a *agent) execute(ctx context.Context, sessionID uuid.UUID, input string, maxHistory int, runID uuid.UUID) (*RunResult, error) {
	messages, err := a.loadConversation(ctx, sessionID, maxHistory)
	if err != nil {
		return nil, err
	}

	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: input,
	})

	if a.config.SystemPrompt != "" {
		hasSystem := false
		for _, msg := range messages {
			if msg.Role == llm.RoleSystem {
				hasSystem = true
				break
			}
		}
		if !hasSystem {
			messages = append([]llm.Message{{
				Role:    llm.RoleSystem,
				Content: a.config.SystemPrompt,
			}}, messages...)
		}
	}

	if a.config.MemoryManager != nil {
		messages, err = a.config.MemoryManager.Prepare(ctx, messages, a.config.MaxContextTokens)
		if err != nil {
			return nil, err
		}
	} else {
		counter := llm.NewTokenCounter("gpt-4o")
		messages = counter.Fit(messages, a.config.MaxContextTokens)
	}

	var toolCallResults []ToolCallResult

	for i := 0; i < a.config.MaxIterations; i++ {
		opts := append([]llm.Option{}, a.config.LLMOptions...)

		if a.config.ToolRegistry != nil && a.config.ToolRegistry.Len() > 0 {
			opts = append(opts, llm.WithTools(a.config.ToolRegistry.ToToolDefinitions()))
		}

		resp, err := a.config.LLM.Complete(ctx, messages, opts...)
		if err != nil {
			return nil, err
		}

		if len(resp.ToolCalls) == 0 {
			if err := a.saveMessage(ctx, sessionID, llm.Message{
				Role:    llm.RoleAssistant,
				Content: resp.Content,
			}); err != nil {
				return nil, err
			}

			return &RunResult{
				Output:    resp.Content,
				ToolCalls: toolCallResults,
			}, nil
		}

		for _, tc := range resp.ToolCalls {
			result := a.executeTool(ctx, runID, tc)
			toolCallResults = append(toolCallResults, result)

			messages = append(messages, llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{tc},
			})

			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    formatToolResult(result.Result, result.Error),
				ToolCallID: tc.ID,
			})
		}
	}

	return nil, ErrMaxIterationsReached
}

func (a *agent) executeTool(ctx context.Context, runID uuid.UUID, tc llm.ToolCall) ToolCallResult {
	result := ToolCallResult{
		Name: tc.Function.Name,
	}

	var args map[string]any
	if err := jsonUnmarshal(tc.Function.Arguments, &args); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Args = args

	if a.config.ToolRegistry == nil {
		result.Error = "no tools registered"
		return result
	}

	output, err := a.config.ToolRegistry.Execute(ctx, tc.Function.Name, args)

	tcStorage := &storage.ToolCall{
		ID:     uuid.New(),
		RunID:  runID,
		Name:   tc.Function.Name,
		Args:   args,
		Result: output,
	}

	if err != nil {
		result.Error = err.Error()
		tcStorage.Error = err.Error()
	} else {
		result.Result = output
	}

	if store, ok := a.store.(interface {
		SaveToolCall(context.Context, *storage.ToolCall) error
	}); ok {
		store.SaveToolCall(ctx, tcStorage)
	}

	return result
}

func (a *agent) loadConversation(ctx context.Context, sessionID uuid.UUID, limit int) ([]llm.Message, error) {
	msgs, err := a.store.GetConversation(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}

	messages := make([]llm.Message, 0, len(msgs))
	for _, msg := range msgs {
		llmMsg := llm.Message{
			Role:    llm.Role(msg.Role),
			Content: msg.Content,
		}
		messages = append(messages, llmMsg)
	}

	return messages, nil
}

func (a *agent) saveMessage(ctx context.Context, sessionID uuid.UUID, msg llm.Message) error {
	storageMsg := &storage.Message{
		ID:        uuid.New(),
		SessionID: sessionID,
		Role:      string(msg.Role),
		Content:   msg.Content,
		CreatedAt: time.Now(),
	}
	return a.store.SaveMessage(ctx, storageMsg)
}

func formatToolResult(result any, err string) string {
	if err != "" {
		return "Error: " + err
	}
	return toString(result)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := jsonMarshal(v)
	return string(b)
}

func jsonUnmarshal(data string, v any) error {
	if data == "" {
		return nil
	}
	b := []byte(data)
	return json.Unmarshal(b, v)
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (a *agent) GetConfig() *Config {
	return a.config
}
