package tool

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tool is a provider-agnostic interface for tools that can be invoked by an LLM.
type Tool interface {
	Name() string
	Description() string
	// InputSchema returns the JSON Schema describing the tool's input parameters.
	InputSchema() (*jsonschema.Schema, error)
	// Run executes the tool with the given arguments JSON string and returns the result as a string.
	Run(ctx context.Context, toolCallID string, arguments string) (string, error)
}
