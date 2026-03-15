package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	goanthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/tool"
)

// defaultMaxTokens is the default maximum number of tokens for a response.
const defaultMaxTokens = 4096

// defaultThinkingBudget is the default token budget for extended thinking.
// Must be ≥1024 and less than maxTokens.
const defaultThinkingBudget = 3000

// Messages implements model.LLM using the Anthropic Messages API.
type Messages struct {
	client    goanthropic.Client
	modelName string
}

// New creates a new Messages instance.
// apiKey is required. modelName is the identifier of the Anthropic model to use
// (e.g. "claude-sonnet-4-5", "claude-opus-4-5").
func New(apiKey, modelName string) *Messages {
	client := goanthropic.NewClient(option.WithAPIKey(apiKey))
	return &Messages{
		client:    client,
		modelName: modelName,
	}
}

// Name returns the model identifier.
func (m *Messages) Name() string {
	return m.modelName
}

// GenerateContent sends the request to the Anthropic Messages API.
// When stream is false (or true, as streaming is not yet implemented),
// exactly one complete *model.LLMResponse is yielded.
func (m *Messages) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, system, err := convertMessages(req.Messages)
		if err != nil {
			yield(nil, fmt.Errorf("anthropic: convert messages: %w", err))
			return
		}

		tools, err := convertTools(req.Tools)
		if err != nil {
			yield(nil, fmt.Errorf("anthropic: convert tools: %w", err))
			return
		}

		maxTokens := int64(defaultMaxTokens)
		if cfg != nil && cfg.MaxTokens > 0 {
			maxTokens = cfg.MaxTokens
		}

		params := goanthropic.MessageNewParams{
			Model:     goanthropic.Model(req.Model),
			Messages:  messages,
			System:    system,
			MaxTokens: maxTokens,
		}
		if len(tools) > 0 {
			params.Tools = tools
		}

		if cfg != nil {
			applyConfig(&params, cfg)
		}

		resp, err := m.client.Messages.New(ctx, params)
		if err != nil {
			yield(nil, fmt.Errorf("anthropic: messages new: %w", err))
			return
		}

		llmResp := convertResponse(resp)
		llmResp.TurnComplete = true
		yield(llmResp, nil)
	}
}

// convertMessages maps model.Message slice to Anthropic MessageParam slice,
// extracting system messages into the top-level system prompt.
// Consecutive RoleTool messages are batched into a single user message.
func convertMessages(msgs []model.Message) ([]goanthropic.MessageParam, []goanthropic.TextBlockParam, error) {
	var system []goanthropic.TextBlockParam
	var messages []goanthropic.MessageParam

	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch m.Role {
		case model.RoleSystem:
			system = append(system, goanthropic.TextBlockParam{Text: m.Content})
			i++

		case model.RoleUser:
			blocks, err := convertUserBlocks(m)
			if err != nil {
				return nil, nil, fmt.Errorf("user message: %w", err)
			}
			messages = append(messages, goanthropic.NewUserMessage(blocks...))
			i++

		case model.RoleAssistant:
			blocks := convertAssistantBlocks(m)
			messages = append(messages, goanthropic.NewAssistantMessage(blocks...))
			i++

		case model.RoleTool:
			// Batch consecutive tool-result messages into one user message.
			var blocks []goanthropic.ContentBlockParamUnion
			for i < len(msgs) && msgs[i].Role == model.RoleTool {
				tm := msgs[i]
				toolResult := goanthropic.ToolResultBlockParam{
					ToolUseID: tm.ToolCallID,
					Content: []goanthropic.ToolResultBlockParamContentUnion{
						{OfText: &goanthropic.TextBlockParam{Text: tm.Content}},
					},
				}
				blocks = append(blocks, goanthropic.ContentBlockParamUnion{
					OfToolResult: &toolResult,
				})
				i++
			}
			messages = append(messages, goanthropic.NewUserMessage(blocks...))

		default:
			return nil, nil, fmt.Errorf("unknown message role: %q", m.Role)
		}
	}

	return messages, system, nil
}

// convertUserBlocks converts a RoleUser model.Message to Anthropic ContentBlockParamUnion slice.
func convertUserBlocks(m model.Message) ([]goanthropic.ContentBlockParamUnion, error) {
	if len(m.Parts) == 0 {
		return []goanthropic.ContentBlockParamUnion{
			{OfText: &goanthropic.TextBlockParam{Text: m.Content}},
		}, nil
	}

	blocks := make([]goanthropic.ContentBlockParamUnion, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case model.ContentPartTypeText:
			blocks = append(blocks, goanthropic.ContentBlockParamUnion{
				OfText: &goanthropic.TextBlockParam{Text: p.Text},
			})
		case model.ContentPartTypeImageURL:
			urlSrc := &goanthropic.URLImageSourceParam{URL: p.ImageURL}
			imgBlock := &goanthropic.ImageBlockParam{
				Source: goanthropic.ImageBlockParamSourceUnion{OfURL: urlSrc},
			}
			blocks = append(blocks, goanthropic.ContentBlockParamUnion{OfImage: imgBlock})
		case model.ContentPartTypeImageBase64:
			b64Src := &goanthropic.Base64ImageSourceParam{
				Data:      p.ImageBase64,
				MediaType: goanthropic.Base64ImageSourceMediaType(p.MIMEType),
			}
			imgBlock := &goanthropic.ImageBlockParam{
				Source: goanthropic.ImageBlockParamSourceUnion{OfBase64: b64Src},
			}
			blocks = append(blocks, goanthropic.ContentBlockParamUnion{OfImage: imgBlock})
		default:
			return nil, fmt.Errorf("unsupported content part type: %q", p.Type)
		}
	}
	return blocks, nil
}

// convertAssistantBlocks converts a RoleAssistant model.Message to Anthropic ContentBlockParamUnion slice.
func convertAssistantBlocks(m model.Message) []goanthropic.ContentBlockParamUnion {
	var blocks []goanthropic.ContentBlockParamUnion
	if m.Content != "" {
		blocks = append(blocks, goanthropic.ContentBlockParamUnion{
			OfText: &goanthropic.TextBlockParam{Text: m.Content},
		})
	}
	for _, tc := range m.ToolCalls {
		var input any
		_ = json.Unmarshal([]byte(tc.Arguments), &input)
		toolUseBlock := &goanthropic.ToolUseBlockParam{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: input,
		}
		blocks = append(blocks, goanthropic.ContentBlockParamUnion{OfToolUse: toolUseBlock})
	}
	// Anthropic requires at least one block; fall back to empty text.
	if len(blocks) == 0 {
		blocks = []goanthropic.ContentBlockParamUnion{
			{OfText: &goanthropic.TextBlockParam{Text: ""}},
		}
	}
	return blocks
}

// convertTools maps tool.Tool slice to Anthropic ToolUnionParam slice.
func convertTools(tools []tool.Tool) ([]goanthropic.ToolUnionParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	result := make([]goanthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		def := t.Definition()

		// Marshal schema to JSON, then decode into ToolInputSchemaParam.
		schemaJSON, err := json.Marshal(def.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: marshal schema: %w", def.Name, err)
		}
		var inputSchema goanthropic.ToolInputSchemaParam
		if err := json.Unmarshal(schemaJSON, &inputSchema); err != nil {
			return nil, fmt.Errorf("tool %q: unmarshal schema: %w", def.Name, err)
		}

		toolParam := &goanthropic.ToolParam{
			Name:        def.Name,
			Description: param.NewOpt(def.Description),
			InputSchema: inputSchema,
		}
		result = append(result, goanthropic.ToolUnionParam{OfTool: toolParam})
	}
	return result, nil
}

// applyConfig transfers GenerateConfig settings to MessageNewParams.
func applyConfig(p *goanthropic.MessageNewParams, cfg *model.GenerateConfig) {
	if cfg.Temperature != 0 {
		p.Temperature = param.NewOpt(cfg.Temperature)
	}

	// Map EnableThinking → Anthropic ThinkingConfig.
	// ReasoningEffort is not directly supported by Anthropic; EnableThinking is used instead.
	if cfg.EnableThinking != nil && *cfg.EnableThinking {
		budget := int64(defaultThinkingBudget)
		if cfg.ThinkingBudget > 0 {
			budget = cfg.ThinkingBudget
		}
		p.Thinking = goanthropic.ThinkingConfigParamOfEnabled(budget)
	} else if cfg.EnableThinking != nil && !*cfg.EnableThinking {
		disabled := goanthropic.NewThinkingConfigDisabledParam()
		p.Thinking = goanthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
	}
}

// convertResponse maps an Anthropic Message to a provider-agnostic LLMResponse.
func convertResponse(resp *goanthropic.Message) *model.LLMResponse {
	msg := model.Message{Role: model.RoleAssistant}

	var textParts []string
	var reasoningParts []string

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "thinking":
			if block.Thinking != "" {
				reasoningParts = append(reasoningParts, block.Thinking)
			}
		case "tool_use":
			argsJSON := string(block.Input)
			if argsJSON == "" || argsJSON == "null" {
				argsJSON = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: argsJSON,
			})
		}
	}

	msg.Content = strings.Join(textParts, "")
	msg.ReasoningContent = strings.Join(reasoningParts, "")

	finishReason := convertStopReason(resp.StopReason)
	if len(msg.ToolCalls) > 0 {
		finishReason = model.FinishReasonToolCalls
	}

	usage := &model.TokenUsage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}

	return &model.LLMResponse{
		Message:      msg,
		FinishReason: finishReason,
		Usage:        usage,
	}
}

// convertStopReason maps Anthropic StopReason to model.FinishReason.
func convertStopReason(reason goanthropic.StopReason) model.FinishReason {
	switch reason {
	case goanthropic.StopReasonEndTurn, goanthropic.StopReasonStopSequence:
		return model.FinishReasonStop
	case goanthropic.StopReasonMaxTokens:
		return model.FinishReasonLength
	case goanthropic.StopReasonToolUse:
		return model.FinishReasonToolCalls
	default:
		return model.FinishReasonStop
	}
}
