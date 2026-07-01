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
	// Deprecated: use ModelV4Flash with EnableThinking=false instead.
	ModelChat = "deepseek-chat"
	// ModelReasoner is DeepSeek's legacy thinking compatibility model.
	//
	// Deprecated: use ModelV4Flash or ModelV4Pro with thinking enabled instead.
	ModelReasoner = "deepseek-reasoner"
)

// ChatCompletion implements model.LLM using DeepSeek's OpenAI-compatible Chat
// Completions API.
type ChatCompletion struct {
	inner *openai.ChatCompletion
}

// New creates a new DeepSeek ChatCompletion instance using the default
// DeepSeek OpenAI-compatible API endpoint.
func New(apiKey, modelName string, retryCfg ...retry.Config) *ChatCompletion {
	return NewWithBaseURL(apiKey, BaseURL, modelName, retryCfg...)
}

// NewWithBaseURL creates a new DeepSeek ChatCompletion instance using a custom
// OpenAI-compatible base URL, which is useful for proxies and tests.
func NewWithBaseURL(apiKey, baseURL, modelName string, retryCfg ...retry.Config) *ChatCompletion {
	opts := []openai.Option{openai.WithDeepSeekCompatibility()}
	if len(retryCfg) > 0 {
		opts = append(opts, openai.WithRetryConfig(retryCfg[0]))
	}
	return &ChatCompletion{
		inner: openai.NewWithOptions(apiKey, baseURL, modelName, opts...),
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
