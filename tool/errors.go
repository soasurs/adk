package tool

import "encoding/json"

const defaultHandledErrorContent = "tool call failed"

// HandledError is a completed tool failure whose content is safe to send to
// the model. It must not contain internal SDK, transport, or infrastructure
// error details.
type HandledError struct {
	// Content is the plain-text failure message that may be sent to the model.
	Content string `json:"content,omitempty"`
	// StructuredContent is the optional JSON failure payload that may be sent to
	// providers supporting structured tool results.
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
}

// NewHandledError creates a model-visible handled tool failure.
func NewHandledError(content string) *HandledError {
	return &HandledError{Content: content}
}

// Error implements the error interface.
func (e *HandledError) Error() string {
	if e == nil {
		return defaultHandledErrorContent
	}
	if e.Content != "" {
		return e.Content
	}
	if len(e.StructuredContent) > 0 {
		return string(e.StructuredContent)
	}
	return defaultHandledErrorContent
}

func (*HandledError) isOutcome() {}

// Text returns the safe plain-text failure sent to providers that do not
// support structured tool results.
func (e *HandledError) Text() string {
	return e.Error()
}

// Clone returns an independent copy of e.
func (e *HandledError) Clone() *HandledError {
	if e == nil {
		return &HandledError{Content: defaultHandledErrorContent}
	}
	clone := *e
	clone.StructuredContent = append(json.RawMessage(nil), e.StructuredContent...)
	if clone.Content == "" && len(clone.StructuredContent) == 0 {
		clone.Content = defaultHandledErrorContent
	}
	return &clone
}
