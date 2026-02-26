package llm

import (
	"context"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role           `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
	Model     string
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type StreamChunk struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
	Error     error
}

type Provider interface {
	Complete(ctx context.Context, messages []Message, opts ...Option) (*Response, error)
	Stream(ctx context.Context, messages []Message, opts ...Option) (<-chan StreamChunk, error)
}

type Option func(*Options)

type Options struct {
	Model       string
	Temperature float32
	MaxTokens   int
	Tools       []ToolDefinition
	ToolChoice  string
	Stream      bool
	Timeout     time.Duration
}

func WithModel(model string) Option {
	return func(o *Options) {
		o.Model = model
	}
}

func WithTemperature(temp float32) Option {
	return func(o *Options) {
		o.Temperature = temp
	}
}

func WithMaxTokens(tokens int) Option {
	return func(o *Options) {
		o.MaxTokens = tokens
	}
}

func WithTools(tools []ToolDefinition) Option {
	return func(o *Options) {
		o.Tools = tools
	}
}

func WithToolChoice(choice string) Option {
	return func(o *Options) {
		o.ToolChoice = choice
	}
}

func DefaultOptions() *Options {
	return &Options{
		Model:       "gpt-4o",
		Temperature: 0.7,
		MaxTokens:   4096,
	}
}

func (o *Options) Apply(opts ...Option) {
	for _, opt := range opts {
		opt(o)
	}
}
