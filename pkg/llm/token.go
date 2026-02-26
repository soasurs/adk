package llm

import (
	"strings"
	"unicode"
)

type TokenCounter interface {
	Count(messages []Message) int
	Fit(messages []Message, maxTokens int) []Message
}

type SimpleTokenCounter struct {
	model ModelInfo
}

type ModelInfo struct {
	Name            string
	TokensPerWord   float64
	ExtraPerMessage int
}

var modelPresets = map[string]ModelInfo{
	"gpt-3.5":  {Name: "gpt-3.5", TokensPerWord: 1.3, ExtraPerMessage: 4},
	"gpt-4":    {Name: "gpt-4", TokensPerWord: 1.3, ExtraPerMessage: 4},
	"gpt-4o":   {Name: "gpt-4o", TokensPerWord: 1.3, ExtraPerMessage: 4},
	"claude-3": {Name: "claude-3", TokensPerWord: 1.4, ExtraPerMessage: 5},
	"default":  {Name: "default", TokensPerWord: 1.3, ExtraPerMessage: 4},
}

func NewTokenCounter(modelName string) *SimpleTokenCounter {
	info, ok := modelPresets[modelName]
	if !ok {
		info = modelPresets["default"]
	}
	return &SimpleTokenCounter{model: info}
}

func (c *SimpleTokenCounter) Count(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += c.countMessage(msg)
	}
	return total
}

func (c *SimpleTokenCounter) countMessage(msg Message) int {
	tokens := c.model.ExtraPerMessage

	switch msg.Role {
	case RoleSystem:
		tokens += 2
	case RoleUser:
		tokens += 1
	case RoleAssistant:
		tokens += 1
	case RoleTool:
		tokens += 2
	}

	tokens += c.countText(msg.Content)

	if msg.ToolCallID != "" {
		tokens += c.countText(msg.ToolCallID)
	}

	if msg.Name != "" {
		tokens += c.countText(msg.Name)
	}

	for _, tc := range msg.ToolCalls {
		tokens += 5
		tokens += c.countText(tc.Function.Name)
		tokens += c.countText(tc.Function.Arguments)
	}

	return tokens
}

func (c *SimpleTokenCounter) countText(text string) int {
	if text == "" {
		return 0
	}

	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	if len(words) == 0 {
		return int(float64(len(text)) * 0.5)
	}

	return int(float64(len(words)) * c.model.TokensPerWord)
}

func (c *SimpleTokenCounter) Fit(messages []Message, maxTokens int) []Message {
	if len(messages) == 0 {
		return messages
	}

	currentTokens := c.Count(messages)
	if currentTokens <= maxTokens {
		return messages
	}

	var systemMsg []Message
	var otherMsgs []Message

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			systemMsg = append(systemMsg, msg)
		} else {
			otherMsgs = append(otherMsgs, msg)
		}
	}

	systemTokens := c.Count(systemMsg)
	availableTokens := maxTokens - systemTokens

	if availableTokens <= 0 {
		return systemMsg
	}

	result := make([]Message, 0, len(systemMsg)+len(otherMsgs))
	result = append(result, systemMsg...)

	for i := len(otherMsgs) - 1; i >= 0; i-- {
		msg := otherMsgs[i]
		msgTokens := c.countMessage(msg)

		if msgTokens > availableTokens {
			truncated := c.truncateMessage(msg, availableTokens)
			if truncated != nil {
				result = append([]Message{*truncated}, result...)
			}
			break
		}

		result = append([]Message{msg}, result...)
		availableTokens -= msgTokens
	}

	return result
}

func (c *SimpleTokenCounter) truncateMessage(msg Message, maxTokens int) *Message {
	if maxTokens <= 0 {
		return nil
	}

	baseTokens := c.model.ExtraPerMessage + 3
	availableForContent := maxTokens - baseTokens

	if availableForContent <= 0 {
		return &msg
	}

	currentTokens := c.countText(msg.Content)
	if currentTokens <= availableForContent {
		return &msg
	}

	ratio := float64(availableForContent) / float64(currentTokens)
	newLength := int(float64(len(msg.Content)) * ratio * 0.9)

	if newLength < 10 {
		newLength = 10
	}

	truncated := msg
	truncated.Content = msg.Content[:newLength] + "..."
	return &truncated
}

func EstimateTokens(text string) int {
	counter := NewTokenCounter("default")
	return counter.countText(text)
}
