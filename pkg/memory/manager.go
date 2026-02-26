package memory

import (
	"context"

	"soasurs.dev/soasurs/adk/pkg/llm"
)

type ContextManager interface {
	Prepare(ctx context.Context, messages []llm.Message, maxTokens int) ([]llm.Message, error)
	AddSummary(ctx context.Context, sessionID string, content string) error
	GetSummary(ctx context.Context, sessionID string) (string, error)
	Search(ctx context.Context, sessionID string, query string, limit int) ([]llm.Message, error)
}

type Config struct {
	MaxContextTokens int
	Strategy         StrategyType
	SummaryInterval  int
	LLM              llm.Provider
	ModelName        string
	EnableLongTerm   bool
	VectorStore      VectorStore
}

type StrategyType string

const (
	StrategySliding StrategyType = "sliding"
	StrategySummary StrategyType = "summary"
	StrategyHybrid  StrategyType = "hybrid"
)

type VectorStore interface {
	Insert(ctx context.Context, sessionID string, messageID string, text string, vector []float32) error
	Search(ctx context.Context, sessionID string, query string, limit int) ([]VectorResult, error)
	Delete(ctx context.Context, sessionID string, messageID string) error
}

type VectorResult struct {
	MessageID string
	Text      string
	Score     float32
}

type Manager struct {
	config      Config
	counter     *llm.SimpleTokenCounter
	strategy    ContextStrategy
	summarizer  *Summarizer
	vectorStore VectorStore
}

type ContextStrategy interface {
	Prepare(messages []llm.Message, maxTokens int, summary string) []llm.Message
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.MaxContextTokens <= 0 {
		cfg.MaxContextTokens = 8000
	}

	counter := llm.NewTokenCounter(cfg.ModelName)

	var strategy ContextStrategy
	switch cfg.Strategy {
	case StrategySliding:
		strategy = NewSlidingWindowStrategy()
	case StrategySummary, StrategyHybrid:
		strategy = NewHybridStrategy(cfg.SummaryInterval)
	default:
		strategy = NewSlidingWindowStrategy()
	}

	var vectorStore VectorStore
	if cfg.EnableLongTerm && cfg.VectorStore != nil {
		vectorStore = cfg.VectorStore
	}

	m := &Manager{
		config:      cfg,
		counter:     counter,
		strategy:    strategy,
		vectorStore: vectorStore,
	}

	if cfg.Strategy == StrategySummary || cfg.Strategy == StrategyHybrid {
		m.summarizer = NewSummarizer(cfg.LLM, cfg.ModelName)
	}

	return m, nil
}

func (m *Manager) Prepare(ctx context.Context, messages []llm.Message, maxTokens int) ([]llm.Message, error) {
	if maxTokens <= 0 {
		maxTokens = m.config.MaxContextTokens
	}

	var summary string
	if m.summarizer != nil && m.config.Strategy != StrategySliding {
		summary = ""
	}

	result := m.strategy.Prepare(messages, maxTokens, summary)

	if m.counter != nil {
		result = m.counter.Fit(result, maxTokens)
	}

	return result, nil
}

func (m *Manager) AddSummary(ctx context.Context, sessionID string, content string) error {
	if m.vectorStore == nil {
		return nil
	}

	vector, err := m.embedText(ctx, content)
	if err != nil {
		return err
	}

	return m.vectorStore.Insert(ctx, sessionID, "summary:"+sessionID, content, vector)
}

func (m *Manager) GetSummary(ctx context.Context, sessionID string) (string, error) {
	return "", nil
}

func (m *Manager) Search(ctx context.Context, sessionID string, query string, limit int) ([]llm.Message, error) {
	if m.vectorStore == nil {
		return nil, nil
	}

	results, err := m.vectorStore.Search(ctx, sessionID, query, limit)
	if err != nil {
		return nil, err
	}

	messages := make([]llm.Message, 0, len(results))
	for _, r := range results {
		messages = append(messages, llm.Message{
			Role:    llm.RoleUser,
			Content: r.Text,
			Metadata: map[string]any{
				"message_id": r.MessageID,
				"score":      r.Score,
			},
		})
	}

	return messages, nil
}

func (m *Manager) ShouldSummarize(messageCount int) bool {
	if m.config.SummaryInterval <= 0 {
		return false
	}
	return messageCount > 0 && messageCount%m.config.SummaryInterval == 0
}

func (m *Manager) GenerateSummary(ctx context.Context, messages []llm.Message) (string, error) {
	if m.summarizer == nil {
		return "", nil
	}
	return m.summarizer.Summarize(ctx, messages)
}

func (m *Manager) embedText(ctx context.Context, text string) ([]float32, error) {
	if m.config.LLM == nil {
		return nil, nil
	}

	return make([]float32, 1536), nil
}
