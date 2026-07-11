package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// Handler is a strongly typed Go function wrapped as a Tool by NewFunc. Return
// a HandledError for a handled failure that is safe to send to the model; other
// errors are terminal.
type Handler[In, Out any] func(context.Context, In) (Out, error)

// NewFunc wraps a strongly typed Go function as a Tool. If Definition does not
// provide schemas, they are inferred from In and Out. Ordinary Handler errors
// propagate to the caller. LlmAgent recognizes HandledError as a completed,
// model-visible failure; other errors are terminal.
func NewFunc[In, Out any](def Definition, handler Handler[In, Out]) (Tool, error) {
	if handler == nil {
		return nil, fmt.Errorf("tool %q: handler must not be nil", def.Name)
	}

	inputSchema, err := schemaFor[In](def.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("tool %q: infer input schema: %w", def.Name, err)
	}
	outputSchema, err := schemaFor[Out](def.OutputSchema)
	if err != nil {
		return nil, fmt.Errorf("tool %q: infer output schema: %w", def.Name, err)
	}

	def.InputSchema = inputSchema
	def.OutputSchema = outputSchema

	return &funcTool[In, Out]{
		def:     def,
		handler: handler,
	}, nil
}

type funcTool[In, Out any] struct {
	def     Definition
	handler Handler[In, Out]
}

func (t *funcTool[In, Out]) Definition() Definition {
	return t.def
}

func (t *funcTool[In, Out]) Run(ctx context.Context, call Call) (*Result, error) {
	var input In
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return nil, NewHandledError(fmt.Sprintf("error: parse arguments: %s", err.Error()))
	}

	output, err := t.handler(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("tool %q: run handler: %w", t.def.Name, err)
	}

	raw, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("tool %q: marshal result: %w", t.def.Name, err)
	}

	return &Result{
		Content:           resultContent(output, raw),
		StructuredContent: raw,
	}, nil
}

func resultContent(output any, raw []byte) string {
	value := reflect.ValueOf(output)
	if value.IsValid() && value.Kind() == reflect.String {
		return value.String()
	}
	return string(raw)
}

func schemaFor[T any](override *jsonschema.Schema) (*jsonschema.Schema, error) {
	if override != nil {
		return override, nil
	}
	return jsonschema.ForType(reflect.TypeFor[T](), &jsonschema.ForOptions{})
}
