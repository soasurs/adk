package memory

import (
	"soasurs.dev/soasurs/adk/pkg/llm"
)

type SlidingWindowStrategy struct {
	keepLast int
}

func NewSlidingWindowStrategy() *SlidingWindowStrategy {
	return &SlidingWindowStrategy{
		keepLast: 20,
	}
}

func (s *SlidingWindowStrategy) Prepare(messages []llm.Message, maxTokens int, summary string) []llm.Message {
	if len(messages) == 0 {
		return messages
	}

	var systemMsgs []llm.Message
	var otherMsgs []llm.Message

	for _, msg := range messages {
		if msg.Role == llm.RoleSystem {
			systemMsgs = append(systemMsgs, msg)
		} else {
			otherMsgs = append(otherMsgs, msg)
		}
	}

	result := make([]llm.Message, 0, len(systemMsgs)+s.keepLast)
	result = append(result, systemMsgs...)

	if summary != "" {
		result = append(result, llm.Message{
			Role:    llm.RoleSystem,
			Content: "Previous conversation summary:\n" + summary,
		})
	}

	startIdx := 0
	if len(otherMsgs) > s.keepLast {
		startIdx = len(otherMsgs) - s.keepLast
	}

	result = append(result, otherMsgs[startIdx:]...)

	return result
}

func (s *SlidingWindowStrategy) SetKeepLast(n int) {
	s.keepLast = n
}
