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

	if cfg != nil {
		applyConfig(&params, cfg)
	}

	resp, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai: chat completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices returned")
	}

	return convertResponse(resp.Choices[0]), nil
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
		schema, err := t.InputSchema()
		if err != nil {
			return nil, fmt.Errorf("tool %q: get schema: %w", t.Name(), err)
		}

		// Marshal schema to JSON, then decode into shared.FunctionParameters (map[string]any).
		schemaJSON, err := json.Marshal(schema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: marshal schema: %w", t.Name(), err)
		}
		var parameters shared.FunctionParameters
		if err := json.Unmarshal(schemaJSON, &parameters); err != nil {
			return nil, fmt.Errorf("tool %q: unmarshal schema: %w", t.Name(), err)
		}

		fnDef := shared.FunctionDefinitionParam{
			Name:        t.Name(),
			Description: param.NewOpt(t.Description()),
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

// applyConfig transfers GenerateConfig settings to ChatCompletionNewParams.
func applyConfig(p *goopenai.ChatCompletionNewParams, cfg *model.GenerateConfig) {
	if cfg.Temperature != 0 {
		p.Temperature = param.NewOpt(cfg.Temperature)
	}
	if cfg.TopP != 0 {
		p.TopP = param.NewOpt(cfg.TopP)
	}
	if cfg.ReasoningEffort != "" {
		p.ReasoningEffort = shared.ReasoningEffort(cfg.ReasoningEffort)
	}
	if cfg.ServiceTier != "" {
		p.ServiceTier = goopenai.ChatCompletionNewParamsServiceTier(cfg.ServiceTier)
	}
}

// convertResponse maps the first OpenAI choice to model.LLMResponse.
func convertResponse(choice goopenai.ChatCompletionChoice) *model.LLMResponse {
	msg := model.Message{
		Role:    model.RoleAssistant,
		Content: choice.Message.Content,
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
