package tool

import (
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

// Definition holds the metadata that describes a tool to an LLM.
type Definition struct {
	Name        string
	Description string
	// InputSchema is the JSON Schema describing the tool's input parameters.
	InputSchema *jsonschema.Schema
	// OutputSchema is the JSON Schema describing the tool's structured result.
	OutputSchema *jsonschema.Schema
}

// Call is one model-requested tool invocation.
type Call struct {
	// ID is the provider-supplied tool call identifier used to match the result.
	ID string
	// Name is the requested tool name.
	Name string
	// Arguments is the raw JSON payload supplied by the model.
	Arguments json.RawMessage
}

// Result is the provider-neutral result of a tool invocation.
type Result struct {
	// Content is the plain-text fallback returned to providers that do not
	// support structured tool results.
	Content string
	// StructuredContent is the raw JSON result returned by the tool.
	StructuredContent json.RawMessage
	// IsError reports that the invocation completed with a handled failure whose
	// content is safe to send back to the model as a tool response.
	IsError bool
}

// Tool is a provider-agnostic interface for tools that can be invoked by an LLM.
type Tool interface {
	// Definition returns the tool's metadata used by the LLM to understand and call the tool.
	Definition() Definition
	// Run executes the tool call and returns a provider-neutral result. A Result
	// with IsError set is a handled failure that may be sent to the model. A
	// non-nil error means the invocation did not produce a valid result; callers
	// must ignore the Result and terminate the current execution.
	Run(ctx context.Context, call Call) (Result, error)
}
