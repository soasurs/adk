package tool

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
)

// Definition holds the metadata that describes a tool to an LLM.
type Definition struct {
	Name        string
	Description string
	// InputSchema is the JSON Schema describing the tool's input parameters.
	InputSchema *jsonschema.Schema
}

// Tool is a provider-agnostic interface for tools that can be invoked by an LLM.
type Tool interface {
	// Definition returns the tool's metadata used by the LLM to understand and call the tool.
	Definition() Definition
	// Run executes the tool with the given arguments JSON string and returns the result as a string.
	Run(ctx context.Context, toolCallID string, arguments string) (string, error)
}
