package openai

import (
	"context"
	"encoding/json"
	"fmt"

	goopenai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/tool"
)

// ChatCompletion implements model.LLM using the OpenAI Chat Completions API.
type ChatCompletion struct {
	client    goopenai.Client
	modelName string
}

// New creates a new ChatCompletion instance.
// apiKey is required. baseURL is optional; when non-empty it overrides the
// default OpenAI endpoint, which allows using any OpenAI-compatible provider.
func New(apiKey, baseURL, modelName string) *ChatCompletion {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &ChatCompletion{
		client:    goopenai.NewClient(opts...),
		modelName: modelName,
	}
}

// Name returns the model identifier.
func (c *ChatCompletion) Name() string {
	return c.modelName
}

// Generate sends the request to the OpenAI Chat Completions API and returns
// the first choice as a provider-agnostic LLMResponse.
func (c *ChatCompletion) Generate(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig) (*model.LLMResponse, error) {
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("openai: convert messages: %w", err)
	}

	tools, err := convertTools(req.Tools)
	if err != nil {
		return nil, fmt.Errorf("openai: convert tools: %w", err)
	}

	params := goopenai.ChatCompletionNewParams{
		Model:    shared.ChatModel(req.Model),
		Messages: messages,
		Tools:    tools,
	}

	var reqOpts []option.RequestOption
	if cfg != nil {
		applyConfig(&params, cfg, &reqOpts)
	}

	resp, err := c.client.Chat.Completions.New(ctx, params, reqOpts...)
	if err != nil {
		return nil, fmt.Errorf("openai: chat completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices returned")
	}

	return convertResponse(resp.Choices[0], &resp.Usage), nil
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
	if cfg.Temperature != 0 {
		p.Temperature = param.NewOpt(cfg.Temperature)
	}
	if cfg.TopP != 0 {
		p.TopP = param.NewOpt(cfg.TopP)
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
