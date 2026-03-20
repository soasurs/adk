package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	goopenai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/retry"
	"github.com/soasurs/adk/tool"
)

// ChatCompletion implements model.LLM using the OpenAI Chat Completions API.
type ChatCompletion struct {
	client    goopenai.Client
	modelName string
	retryCfg  retry.Config
}

// New creates a new ChatCompletion instance.
// apiKey is required. baseURL is optional; when non-empty it overrides the
// default OpenAI endpoint, which allows using any OpenAI-compatible provider.
// retryCfg is optional; when provided it enables automatic retry with
// exponential backoff on transient errors (rate limits, 5xx, network issues).
func New(apiKey, baseURL, modelName string, retryCfg ...retry.Config) *ChatCompletion {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	cc := &ChatCompletion{
		client:    goopenai.NewClient(opts...),
		modelName: modelName,
	}
	if len(retryCfg) > 0 {
		cc.retryCfg = retryCfg[0]
	} else {
		cc.retryCfg = retry.DefaultConfig()
	}
	return cc
}

// Name returns the model identifier.
func (c *ChatCompletion) Name() string {
	return c.modelName
}

// GenerateContent sends the request to the OpenAI Chat Completions API.
// When stream is false, exactly one complete *model.LLMResponse is yielded.
// When stream is true, partial text chunks are yielded (Partial=true) followed
// by the assembled complete response (Partial=false, TurnComplete=true).
// Transient errors are automatically retried according to the retry.Config
// provided at construction time.
func (c *ChatCompletion) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, err := convertMessages(req.Messages)
		if err != nil {
			yield(nil, fmt.Errorf("openai: convert messages: %w", err))
			return
		}

		tools, err := convertTools(req.Tools)
		if err != nil {
			yield(nil, fmt.Errorf("openai: convert tools: %w", err))
			return
		}

		params := goopenai.ChatCompletionNewParams{
			Model:    shared.ChatModel(req.Model),
			Messages: messages,
			Tools:    tools,
		}
		if stream {
			// Request usage data in streaming mode; it is delivered in the final chunk.
			params.StreamOptions = goopenai.ChatCompletionStreamOptionsParam{
				IncludeUsage: param.NewOpt(true),
			}
		}

		var reqOpts []option.RequestOption
		if cfg != nil {
			applyConfig(&params, cfg, &reqOpts)
		}

		for resp, err := range retry.Seq2(ctx, c.retryCfg,
			func() iter.Seq2[*model.LLMResponse, error] {
				return c.callAPI(ctx, params, reqOpts, stream)
			},
			func(r *model.LLMResponse) bool { return r != nil && r.Partial },
		) {
			if !yield(resp, err) {
				return
			}
		}
	}
}

// callAPI performs a single (non-retried) call to the OpenAI API and returns
// its result as an iter.Seq2. It is called by GenerateContent, potentially
// multiple times when retries are enabled.
func (c *ChatCompletion) callAPI(ctx context.Context, params goopenai.ChatCompletionNewParams, reqOpts []option.RequestOption, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if !stream {
			resp, err := c.client.Chat.Completions.New(ctx, params, reqOpts...)
			if err != nil {
				yield(nil, fmt.Errorf("openai: chat completion: %w", err))
				return
			}
			if len(resp.Choices) == 0 {
				yield(nil, fmt.Errorf("openai: no choices returned"))
				return
			}
			llmResp := convertResponse(resp.Choices[0], &resp.Usage)
			llmResp.TurnComplete = true
			yield(llmResp, nil)
			return
		}

		// Streaming mode.
		s := c.client.Chat.Completions.NewStreaming(ctx, params, reqOpts...)

		var contentBuf strings.Builder
		var reasoningBuf strings.Builder
		toolCallAcc := make(map[int64]*model.ToolCall) // index → accumulated tool call
		var finishReasonStr string
		var streamUsage *goopenai.CompletionUsage

		for s.Next() {
			chunk := s.Current()

			// The last usage-bearing chunk may have no choices; capture usage first.
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				u := chunk.Usage
				streamUsage = &u
			}

			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			delta := choice.Delta

			// Yield delta text and reasoning_content as partial events.
			if delta.Content != "" {
				contentBuf.WriteString(delta.Content)
				if !yield(&model.LLMResponse{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: delta.Content,
					},
					Partial: true,
				}, nil) {
					return
				}
			}

			// Extract reasoning_content delta from raw JSON when present.
			// This field is not part of the standard OpenAI SDK struct but is returned
			// by reasoning-capable providers (e.g. DeepSeek-R1, compatible endpoints).
			if raw := delta.RawJSON(); raw != "" {
				var envelope struct {
					ReasoningContent string `json:"reasoning_content"`
				}
				if err := json.Unmarshal([]byte(raw), &envelope); err == nil && envelope.ReasoningContent != "" {
					reasoningBuf.WriteString(envelope.ReasoningContent)
					if !yield(&model.LLMResponse{
						Message: model.Message{
							Role:             model.RoleAssistant,
							ReasoningContent: envelope.ReasoningContent,
						},
						Partial: true,
					}, nil) {
						return
					}
				}
			}

			// Accumulate tool call fragments across chunks.
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				if _, ok := toolCallAcc[idx]; !ok {
					toolCallAcc[idx] = &model.ToolCall{}
				}
				acc := toolCallAcc[idx]
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				if tc.Function.Name != "" {
					acc.Name = tc.Function.Name
				}
				acc.Arguments += tc.Function.Arguments
			}

			if choice.FinishReason != "" {
				finishReasonStr = string(choice.FinishReason)
			}
		}

		if err := s.Err(); err != nil {
			yield(nil, fmt.Errorf("openai: stream: %w", err))
			return
		}

		// Build the final complete response.
		msg := model.Message{
			Role:             model.RoleAssistant,
			Content:          contentBuf.String(),
			ReasoningContent: reasoningBuf.String(),
		}
		if len(toolCallAcc) > 0 {
			var maxIdx int64
			for idx := range toolCallAcc {
				if idx > maxIdx {
					maxIdx = idx
				}
			}
			msg.ToolCalls = make([]model.ToolCall, maxIdx+1)
			for idx, tc := range toolCallAcc {
				msg.ToolCalls[idx] = *tc
			}
		}

		var usage *model.TokenUsage
		if streamUsage != nil {
			usage = &model.TokenUsage{
				PromptTokens:     streamUsage.PromptTokens,
				CompletionTokens: streamUsage.CompletionTokens,
				TotalTokens:      streamUsage.TotalTokens,
			}
		}
		yield(&model.LLMResponse{
			Message:      msg,
			FinishReason: convertFinishReason(finishReasonStr),
			TurnComplete: true,
			Usage:        usage,
		}, nil)
	}
}

// convertMessages maps model.Message slice to openai ChatCompletionMessageParamUnion slice.
func convertMessages(msgs []model.Message) ([]goopenai.ChatCompletionMessageParamUnion, error) {
	result := make([]goopenai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		p, err := convertMessage(m)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, nil
}

func convertMessage(m model.Message) (goopenai.ChatCompletionMessageParamUnion, error) {
	switch m.Role {
	case model.RoleSystem:
		return goopenai.SystemMessage(m.Content), nil

	case model.RoleUser:
		if len(m.Parts) > 0 {
			parts := make([]goopenai.ChatCompletionContentPartUnionParam, 0, len(m.Parts))
			for _, p := range m.Parts {
				switch p.Type {
				case model.ContentPartTypeText:
					parts = append(parts, goopenai.TextContentPart(p.Text))
				case model.ContentPartTypeImageURL:
					parts = append(parts, goopenai.ImageContentPart(
						goopenai.ChatCompletionContentPartImageImageURLParam{
							URL:    p.ImageURL,
							Detail: string(p.ImageDetail),
						},
					))
				case model.ContentPartTypeImageBase64:
					dataURI := fmt.Sprintf("data:%s;base64,%s", p.MIMEType, p.ImageBase64)
					parts = append(parts, goopenai.ImageContentPart(
						goopenai.ChatCompletionContentPartImageImageURLParam{
							URL:    dataURI,
							Detail: string(p.ImageDetail),
						},
					))
				default:
					return goopenai.ChatCompletionMessageParamUnion{}, fmt.Errorf("unknown content part type: %q", p.Type)
				}
			}
			return goopenai.UserMessage(parts), nil
		}
		return goopenai.UserMessage(m.Content), nil

	case model.RoleAssistant:
		asst := goopenai.ChatCompletionAssistantMessageParam{}
		if m.Content != "" {
			asst.Content.OfString = param.NewOpt(m.Content)
		}
		if len(m.ToolCalls) > 0 {
			toolCalls := make([]goopenai.ChatCompletionMessageToolCallUnionParam, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				fnParam := goopenai.ChatCompletionMessageFunctionToolCallParam{
					ID: tc.ID,
					Function: goopenai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				}
				toolCalls = append(toolCalls, goopenai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &fnParam,
				})
			}
			asst.ToolCalls = toolCalls
		}
		return goopenai.ChatCompletionMessageParamUnion{OfAssistant: &asst}, nil

	case model.RoleTool:
		return goopenai.ToolMessage(m.Content, m.ToolCallID), nil

	default:
		return goopenai.ChatCompletionMessageParamUnion{}, fmt.Errorf("unknown role: %q", m.Role)
	}
}

// convertTools maps tool.Tool slice to openai ChatCompletionToolUnionParam slice.
func convertTools(tools []tool.Tool) ([]goopenai.ChatCompletionToolUnionParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	result := make([]goopenai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		def := t.Definition()

		// Marshal schema to JSON, then decode into shared.FunctionParameters (map[string]any).
		schemaJSON, err := json.Marshal(def.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: marshal schema: %w", def.Name, err)
		}
		var parameters shared.FunctionParameters
		if err := json.Unmarshal(schemaJSON, &parameters); err != nil {
			return nil, fmt.Errorf("tool %q: unmarshal schema: %w", def.Name, err)
		}

		fnDef := shared.FunctionDefinitionParam{
			Name:        def.Name,
			Description: param.NewOpt(def.Description),
			Parameters:  parameters,
		}
		fnTool := goopenai.ChatCompletionFunctionToolParam{
			Function: fnDef,
		}
		result = append(result, goopenai.ChatCompletionToolUnionParam{
			OfFunction: &fnTool,
		})
	}
	return result, nil
}

// applyConfig transfers GenerateConfig settings to ChatCompletionNewParams and
// optionally appends extra request options (e.g. enable_thinking for compatible
// providers that do not use reasoning_effort).
func applyConfig(p *goopenai.ChatCompletionNewParams, cfg *model.GenerateConfig, opts *[]option.RequestOption) {
	if cfg.MaxTokens > 0 {
		p.MaxCompletionTokens = param.NewOpt(cfg.MaxTokens)
	}
	if cfg.Temperature != 0 {
		p.Temperature = param.NewOpt(cfg.Temperature)
	}
	if cfg.ReasoningEffort != "" {
		p.ReasoningEffort = shared.ReasoningEffort(cfg.ReasoningEffort)
	} else if cfg.EnableThinking != nil && !*cfg.EnableThinking {
		// No explicit effort level set but thinking is disabled: map to "none".
		p.ReasoningEffort = shared.ReasoningEffort(model.ReasoningEffortNone)
	}
	// Inject enable_thinking for providers that use a boolean toggle instead of
	// reasoning_effort (e.g. DeepSeek, Qwen). When ReasoningEffort is already
	// set we trust the caller used the more specific control and skip this.
	if cfg.EnableThinking != nil && cfg.ReasoningEffort == "" {
		*opts = append(*opts, option.WithJSONSet("enable_thinking", *cfg.EnableThinking))
	}
	if cfg.ServiceTier != "" {
		p.ServiceTier = goopenai.ChatCompletionNewParamsServiceTier(cfg.ServiceTier)
	}
}

// convertResponse maps the first OpenAI choice and usage to model.LLMResponse.
func convertResponse(choice goopenai.ChatCompletionChoice, usage *goopenai.CompletionUsage) *model.LLMResponse {
	msg := model.Message{
		Role:    model.RoleAssistant,
		Content: choice.Message.Content,
	}

	// Extract reasoning_content from the raw JSON response when present.
	// This field is not part of the standard OpenAI SDK struct but is returned
	// by reasoning-capable providers (e.g. DeepSeek-R1, compatible endpoints).
	if raw := choice.Message.RawJSON(); raw != "" {
		var envelope struct {
			ReasoningContent string `json:"reasoning_content"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil && envelope.ReasoningContent != "" {
			msg.ReasoningContent = envelope.ReasoningContent
		}
	}

	if len(choice.Message.ToolCalls) > 0 {
		msg.ToolCalls = make([]model.ToolCall, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	return &model.LLMResponse{
		Message:      msg,
		FinishReason: convertFinishReason(choice.FinishReason),
		Usage: &model.TokenUsage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
		},
	}
}

// convertFinishReason maps OpenAI finish_reason string to model.FinishReason.
func convertFinishReason(reason string) model.FinishReason {
	switch reason {
	case "stop":
		return model.FinishReasonStop
	case "tool_calls":
		return model.FinishReasonToolCalls
	case "length":
		return model.FinishReasonLength
	case "content_filter":
		return model.FinishReasonContentFilter
	default:
		return model.FinishReasonStop
	}
}
