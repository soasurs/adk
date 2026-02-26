package memory

import (
	"context"
	"fmt"
	"strings"

	"soasurs.dev/soasurs/adk/pkg/llm"
)

type Summarizer struct {
	llm       llm.Provider
	modelName string
	prompt    string
}

func NewSummarizer(llm llm.Provider, modelName string) *Summarizer {
	return &Summarizer{
		llm:       llm,
		modelName: modelName,
		prompt: `Please summarize the following conversation in a concise manner. Capture the key points, decisions made, and any important context that should be remembered for future interactions.

Conversation:
%s

Provide a brief summary (3-5 sentences) that captures the essential information:`,
	}
}

func (s *Summarizer) Summarize(ctx context.Context, messages []llm.Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	conversationText := s.formatConversation(messages)

	prompt := fmt.Sprintf(s.prompt, conversationText)

	resp, err := s.llm.Complete(ctx, []llm.Message{
		{
			Role:    llm.RoleSystem,
			Content: "You are a helpful assistant that summarizes conversations concisely.",
		},
		{
			Role:    llm.RoleUser,
			Content: prompt,
		},
	}, llm.WithModel(s.modelName), llm.WithMaxTokens(500), llm.WithTemperature(0.3))

	if err != nil {
		return "", fmt.Errorf("generate summary: %w", err)
	}

	return resp.Content, nil
}

func (s *Summarizer) formatConversation(messages []llm.Message) string {
	var sb strings.Builder

	for i, msg := range messages {
		if msg.Role == llm.RoleSystem {
			continue
		}

		role := string(msg.Role)
		if role == "" {
			role = "unknown"
		}

		sb.WriteString(fmt.Sprintf("%s: %s\n", strings.Title(role), msg.Content))

		if i < len(messages)-1 && i%10 == 9 {
			sb.WriteString("---\n")
		}
	}

	return sb.String()
}

func (s *Summarizer) SummarizeWithLimit(ctx context.Context, messages []llm.Message, maxTokens int) (string, error) {
	counter := llm.NewTokenCounter(s.modelName)
	fittedMessages := counter.Fit(messages, maxTokens/2)
	return s.Summarize(ctx, fittedMessages)
}
