package agentool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"

	"soasurs.dev/soasurs/adk/agent"
	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/tool"
)

// agentTool wraps an agent.Agent as a tool.Tool so it can be invoked by an
// orchestrator LlmAgent via the LLM's native function-calling mechanism.
type agentTool struct {
	a   agent.Agent
	def tool.Definition
}

// taskRequest is the JSON-serialisable input schema for the agent tool.
// The LLM passes a single task string when calling the agent as a tool.
type taskRequest struct {
	Task string `json:"task" jsonschema:"The task description to delegate to the agent."`
}

// New wraps the given Agent as a Tool. The tool's name and description are
// taken directly from the agent's Name() and Description() methods.
//
// When invoked, the tool runs the agent with a single user message containing
// the task, collects its final assistant text response, and returns it as the
// tool result string.
func New(a agent.Agent) tool.Tool {
	schema, err := jsonschema.ForType(reflect.TypeFor[taskRequest](), &jsonschema.ForOptions{})
	if err != nil {
		panic(fmt.Sprintf("agentool: build input schema for %q: %v", a.Name(), err))
	}
	return &agentTool{
		a: a,
		def: tool.Definition{
			Name:        a.Name(),
			Description: a.Description(),
			InputSchema: schema,
		},
	}
}

// Definition returns the tool metadata used by the LLM to understand and call
// the agent.
func (t *agentTool) Definition() tool.Definition { return t.def }

// Run executes the wrapped agent with the delegated task and returns the
// agent's final assistant text response. Partial (streaming) events and
// intermediate messages (tool calls, tool results) are consumed silently.
func (t *agentTool) Run(ctx context.Context, _ string, arguments string) (string, error) {
	var req taskRequest
	if err := json.Unmarshal([]byte(arguments), &req); err != nil {
		return "", fmt.Errorf("agentool %q: parse arguments: %w", t.a.Name(), err)
	}

	messages := []model.Message{
		{Role: model.RoleUser, Content: req.Task},
	}

	var result string
	for event, err := range t.a.Run(ctx, messages) {
		if err != nil {
			return "", fmt.Errorf("agentool %q: %w", t.a.Name(), err)
		}
		// Only capture the last complete assistant text response.
		if !event.Partial && event.Message.Role == model.RoleAssistant && event.Message.Content != "" {
			result = event.Message.Content
		}
	}
	return result, nil
}
