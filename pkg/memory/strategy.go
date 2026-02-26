package memory

import (
	"soasurs.dev/soasurs/adk/pkg/llm"
)

type HybridStrategy struct {
	sliding       *SlidingWindowStrategy
	summaryWeight int
}

func NewHybridStrategy(summaryInterval int) *HybridStrategy {
	sliding := NewSlidingWindowStrategy()
	if summaryInterval > 0 {
		sliding.keepLast = summaryInterval * 2
	}
	return &HybridStrategy{
		sliding:       sliding,
		summaryWeight: 10,
	}
}

func (h *HybridStrategy) Prepare(messages []llm.Message, maxTokens int, summary string) []llm.Message {
	return h.sliding.Prepare(messages, maxTokens, summary)
}

func (h *HybridStrategy) SetSummaryWeight(weight int) {
	h.summaryWeight = weight
}
