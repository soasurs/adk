package tool

import "encoding/json"

const defaultFuncErrorContent = "tool call failed"

// FuncError is a handled failure returned by a Handler. NewFunc converts it
// into a Result with IsError set, so its content must be safe to send to the
// model. Ordinary Handler errors are terminal and propagate to the caller.
type FuncError struct {
	// Content is the plain-text failure message that may be sent to the model.
	Content string
	// StructuredContent is the optional JSON failure payload that may be sent to
	// providers supporting structured tool results.
	StructuredContent json.RawMessage
}

// NewFuncError creates a model-visible handled failure for a NewFunc Handler.
func NewFuncError(content string) *FuncError {
	return &FuncError{Content: content}
}

// Error implements the error interface.
func (e *FuncError) Error() string {
	if e == nil {
		return defaultFuncErrorContent
	}
	if e.Content != "" {
		return e.Content
	}
	if len(e.StructuredContent) > 0 {
		return string(e.StructuredContent)
	}
	return defaultFuncErrorContent
}

func (e *FuncError) result() Result {
	if e == nil {
		return Result{Content: defaultFuncErrorContent, IsError: true}
	}
	structured := append(json.RawMessage(nil), e.StructuredContent...)
	content := e.Content
	if content == "" && len(structured) == 0 {
		content = defaultFuncErrorContent
	}
	return Result{
		Content:           content,
		StructuredContent: structured,
		IsError:           true,
	}
}
