package agent

import (
	"errors"

	"soasurs.dev/soasurs/adk/pkg/llm"
	"soasurs.dev/soasurs/adk/pkg/memory"
	"soasurs.dev/soasurs/adk/pkg/tool"
)

var (
	ErrMaxIterationsReached = errors.New("max iterations reached")
	ErrSessionNotFound      = errors.New("session not found")
	ErrLLMNotConfigured     = errors.New("LLM not configured")
)

type Option func(*Config)

func WithID(id string) Option {
	return func(c *Config) {
		c.ID = id
	}
}

func WithName(name string) Option {
	return func(c *Config) {
		c.Name = name
	}
}

func WithDescription(desc string) Option {
	return func(c *Config) {
		c.Description = desc
	}
}

func WithLLM(provider llm.Provider) Option {
	return func(c *Config) {
		c.LLM = provider
	}
}

func WithLLMOptions(opts ...llm.Option) Option {
	return func(c *Config) {
		c.LLMOptions = opts
	}
}

func WithToolRegistry(registry *tool.Registry) Option {
	return func(c *Config) {
		c.ToolRegistry = registry
	}
}

func WithMaxIterations(n int) Option {
	return func(c *Config) {
		c.MaxIterations = n
	}
}

func WithMaxHistory(n int) Option {
	return func(c *Config) {
		c.MaxHistory = n
	}
}

func WithSystemPrompt(prompt string) Option {
	return func(c *Config) {
		c.SystemPrompt = prompt
	}
}

func WithMaxContextTokens(n int) Option {
	return func(c *Config) {
		c.MaxContextTokens = n
	}
}

func WithContextStrategy(strategy string) Option {
	return func(c *Config) {
		c.ContextStrategy = strategy
	}
}

func WithSummaryInterval(n int) Option {
	return func(c *Config) {
		c.SummaryInterval = n
	}
}

func WithMemoryManager(mgr *memory.Manager) Option {
	return func(c *Config) {
		c.MemoryManager = mgr
	}
}

func DefaultConfig() *Config {
	return &Config{
		ID:               "default",
		MaxIterations:    10,
		MaxHistory:       20,
		MaxContextTokens: 8000,
		ContextStrategy:  "sliding",
		SummaryInterval:  10,
	}
}

func NewConfig(opts ...Option) *Config {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}
