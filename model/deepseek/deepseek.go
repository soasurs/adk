// Package deepseek provides a DeepSeek adapter for model.LLM.
package deepseek

import (
	"context"
	"iter"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/openai"
	"github.com/soasurs/adk/model/retry"
)

// BaseURL is the default DeepSeek OpenAI-compatible API endpoint.
const BaseURL = "https://api.deepseek.com"

const (
	// ModelV4Flash is DeepSeek's fast general-purpose model.
	ModelV4Flash = "deepseek-v4-flash"
	// ModelV4Pro is DeepSeek's higher-capability general-purpose model.
	ModelV4Pro = "deepseek-v4-pro"
	// ModelChat is DeepSeek's legacy non-thinking compatibility model.
	//
	// Deprecated: use ModelV4Flash with WithThinkingEnabled(false) instead.
	ModelChat = "deepseek-chat"
	// ModelReasoner is DeepSeek's legacy thinking compatibility model.
	//
	// Deprecated: use ModelV4Flash or ModelV4Pro with WithThinkingEnabled(true) instead.
	ModelReasoner = "deepseek-reasoner"
)

// ChatCompletion implements model.LLM using DeepSeek's OpenAI-compatible Chat
// Completions API.
type ChatCompletion struct {
	inner *openai.ChatCompletion
}

// Option configures a DeepSeek ChatCompletion.
type Option = openai.Option

// ReasoningEffort controls DeepSeek thinking-mode reasoning effort.
type ReasoningEffort string

const (
	// ReasoningEffortHigh uses DeepSeek's default reasoning effort.
	ReasoningEffortHigh ReasoningEffort = "high"
	// ReasoningEffortMax requests DeepSeek's maximum reasoning effort.
	ReasoningEffortMax ReasoningEffort = "max"
)

// WithRetryConfig sets the retry behavior for transient API errors.
func WithRetryConfig(cfg retry.Config) Option {
	return openai.WithRetryConfig(cfg)
}

// WithThinkingEnabled explicitly enables or disables DeepSeek thinking.
func WithThinkingEnabled(enabled bool) Option {
	return openai.WithThinkingEnabled(enabled)
}

// WithReasoningEffort sets DeepSeek thinking-mode reasoning effort.
func WithReasoningEffort(effort ReasoningEffort) Option {
	return openai.WithReasoningEffort(openai.ReasoningEffort(effort))
}

// New creates a new DeepSeek ChatCompletion instance using the default
// DeepSeek OpenAI-compatible API endpoint.
func New(apiKey, modelName string, retryCfg ...retry.Config) *ChatCompletion {
	return NewWithBaseURL(apiKey, BaseURL, modelName, retryCfg...)
}

// NewWithOptions creates a new DeepSeek ChatCompletion instance using the
// default DeepSeek OpenAI-compatible API endpoint and explicit adapter options.
func NewWithOptions(apiKey, modelName string, opts ...Option) *ChatCompletion {
	return NewWithBaseURLOptions(apiKey, BaseURL, modelName, opts...)
}

// NewWithBaseURL creates a new DeepSeek ChatCompletion instance using a custom
// OpenAI-compatible base URL, which is useful for proxies and tests.
func NewWithBaseURL(apiKey, baseURL, modelName string, retryCfg ...retry.Config) *ChatCompletion {
	opts := make([]openai.Option, 0, 1)
	if len(retryCfg) > 0 {
		opts = append(opts, openai.WithRetryConfig(retryCfg[0]))
	}
	return NewWithBaseURLOptions(apiKey, baseURL, modelName, opts...)
}

// NewWithBaseURLOptions creates a new DeepSeek ChatCompletion instance using a
// custom OpenAI-compatible base URL and explicit adapter options.
func NewWithBaseURLOptions(apiKey, baseURL, modelName string, opts ...Option) *ChatCompletion {
	allOpts := []openai.Option{openai.WithDeepSeekCompatibility()}
	allOpts = append(allOpts, opts...)
	return &ChatCompletion{
		inner: openai.NewWithOptions(apiKey, baseURL, modelName, allOpts...),
	}
}

// Name returns the DeepSeek model identifier.
func (c *ChatCompletion) Name() string {
	return c.inner.Name()
}

// GenerateContent sends the request to the DeepSeek Chat Completions API.
func (c *ChatCompletion) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return c.inner.GenerateContent(ctx, req, cfg, stream)
}
