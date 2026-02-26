package openai

import (
	"context"
	"errors"
	"io"

	"github.com/sashabaranov/go-openai"
	"soasurs.dev/soasurs/adk/pkg/llm"
)

func init() {
	llm.RegisterProvider(llm.ProviderOpenAI, NewProvider)
}

type Config struct {
	APIKey  string
	BaseURL string
	OrgID   string
	APIType openai.APIType
	Timeout int
}

type Provider struct {
	client *openai.Client
}

func NewProvider(cfg llm.Config) (llm.Provider, error) {
	c, ok := cfg.(*Config)
	if !ok {
		return nil, errors.New("invalid config type")
	}

	oc := openai.DefaultConfig(c.APIKey)
	if c.BaseURL != "" {
		oc.BaseURL = c.BaseURL
	}
	if c.OrgID != "" {
		oc.OrgID = c.OrgID
	}

	return &Provider{client: openai.NewClientWithConfig(oc)}, nil
}

func (p *Provider) Complete(ctx context.Context, messages []llm.Message, opts ...llm.Option) (*llm.Response, error) {
	options := llm.DefaultOptions()
	options.Apply(opts...)

	req := p.buildChatRequest(messages, options)

	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	return p.convertResponse(resp), nil
}

func (p *Provider) Stream(ctx context.Context, messages []llm.Message, opts ...llm.Option) (<-chan llm.StreamChunk, error) {
	options := llm.DefaultOptions()
	options.Apply(opts...)

	req := p.buildChatRequest(messages, options)
	req.Stream = true

	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	return p.streamResponse(stream), nil
}

func (p *Provider) buildChatRequest(messages []llm.Message, opts *llm.Options) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:       opts.Model,
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		Messages:    p.convertMessages(messages),
	}

	if len(opts.Tools) > 0 {
		req.Tools = p.convertTools(opts.Tools)
		req.ToolChoice = p.convertToolChoice(opts.ToolChoice)
	}

	return req
}

func (p *Provider) convertMessages(messages []llm.Message) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		om := openai.ChatCompletionMessage{
			Role: string(msg.Role),
		}
		if msg.Content != "" {
			om.Content = msg.Content
		}
		if msg.Name != "" {
			om.Name = msg.Name
		}
		if msg.ToolCallID != "" {
			om.ToolCallID = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			om.ToolCalls = p.convertToolCalls(msg.ToolCalls)
		}
		result = append(result, om)
	}
	return result
}

func (p *Provider) convertTools(tools []llm.ToolDefinition) []openai.Tool {
	result := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		ot := openai.Tool{
			Type: openai.ToolType(t.Type),
			Function: &openai.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
			},
		}
		if t.Function.Parameters != nil {
			ot.Function.Parameters = t.Function.Parameters
		}
		result = append(result, ot)
	}
	return result
}

func (p *Provider) convertToolCalls(toolCalls []llm.ToolCall) []openai.ToolCall {
	result := make([]openai.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		otc := openai.ToolCall{
			ID:   tc.ID,
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		}
		result = append(result, otc)
	}
	return result
}

func (p *Provider) convertToolChoice(choice string) any {
	if choice == "" || choice == "auto" {
		return nil
	}
	if choice == "none" || choice == "required" {
		return choice
	}
	return openai.ToolChoice{
		Type: openai.ToolTypeFunction,
		Function: openai.ToolFunction{
			Name: choice,
		},
	}
}

func (p *Provider) convertResponse(resp openai.ChatCompletionResponse) *llm.Response {
	if len(resp.Choices) == 0 {
		return &llm.Response{}
	}

	choice := resp.Choices[0]
	result := &llm.Response{
		Content: choice.Message.Content,
		Usage: llm.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
		Model: resp.Model,
	}

	if len(choice.Message.ToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, llm.ToolCall{
				ID:   tc.ID,
				Type: string(tc.Type),
				Function: llm.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	return result
}

func (p *Provider) streamResponse(stream *openai.ChatCompletionStream) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk, 16)

	go func() {
		defer close(ch)
		defer stream.Close()

		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				ch <- llm.StreamChunk{Done: true}
				return
			}
			if err != nil {
				ch <- llm.StreamChunk{Error: err, Done: true}
				return
			}

			result := llm.StreamChunk{}

			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta
				result.Content = delta.Content

				if len(delta.ToolCalls) > 0 {
					result.ToolCalls = make([]llm.ToolCall, 0, len(delta.ToolCalls))
					for _, tc := range delta.ToolCalls {
						result.ToolCalls = append(result.ToolCalls, llm.ToolCall{
							ID:   tc.ID,
							Type: string(tc.Type),
							Function: llm.FunctionCall{
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							},
						})
					}
				}

				if chunk.Choices[0].FinishReason != "" {
					result.Done = true
				}
			}

			ch <- result
		}
	}()

	return ch
}

func ToConfig(apiKey string, opts ...func(*Config)) *Config {
	cfg := &Config{
		APIKey: apiKey,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

func WithBaseURL(url string) func(*Config) {
	return func(c *Config) {
		c.BaseURL = url
	}
}

func WithOrgID(orgID string) func(*Config) {
	return func(c *Config) {
		c.OrgID = orgID
	}
}
