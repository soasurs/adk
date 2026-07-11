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
	Content string `json:"content,omitempty"`
	// StructuredContent is the raw JSON result returned by the tool.
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
}

func (*Result) isOutcome() {}

// Text returns the plain-text form used by providers that do not support
// structured tool results.
func (r *Result) Text() string {
	if r == nil {
		return ""
	}
	if r.Content != "" {
		return r.Content
	}
	return string(r.StructuredContent)
}

// Clone returns an independent copy of r.
func (r *Result) Clone() *Result {
	if r == nil {
		return nil
	}
	clone := *r
	clone.StructuredContent = append(json.RawMessage(nil), r.StructuredContent...)
	return &clone
}

// Outcome is a completed tool invocation outcome. The only implementations
// are Result for success and HandledError for a model-visible failure.
type Outcome interface {
	isOutcome()
	Text() string
}

// Tool is a provider-agnostic interface for tools that can be invoked by an LLM.
type Tool interface {
	// Definition returns the tool's metadata used by the LLM to understand and call the tool.
	Definition() Definition
	// Run executes the tool call and returns a provider-neutral result. A
	// HandledError is a completed, model-visible failure. Any other non-nil error
	// is terminal. A nil result with a nil error is invalid.
	Run(ctx context.Context, call Call) (*Result, error)
}
