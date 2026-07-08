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
	goresponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/retry"
	"github.com/soasurs/adk/tool"
)

// Responses implements model.LLM using the OpenAI Responses API.
//
// By default it sends the full ADK-provided history on every request with
// store=false, preserving ADK's own session ledger as the source of truth.
type Responses struct {
	client     goopenai.Client
	baseURL    string
	modelName  string
	retryCfg   retry.Config
	generation generationOptions
	store      bool
}

// ResponsesOption configures a Responses instance.
type ResponsesOption func(*Responses)

// WithResponsesRetryConfig sets the retry behavior for transient API errors.
func WithResponsesRetryConfig(cfg retry.Config) ResponsesOption {
	return func(r *Responses) {
		r.retryCfg = cfg
	}
}

// WithResponsesReasoningEffort sets OpenAI Responses reasoning effort for every call.
func WithResponsesReasoningEffort(effort ReasoningEffort) ResponsesOption {
	return func(r *Responses) {
		r.generation.reasoningEffort = effort
	}
}

// WithResponsesServiceTier sets the OpenAI Responses service tier for every call.
func WithResponsesServiceTier(tier ServiceTier) ResponsesOption {
	return func(r *Responses) {
		r.generation.serviceTier = tier
	}
}

// WithResponsesThinkingEnabled controls OpenAI Responses reasoning for every
// call. Disabling maps to reasoning effort "none"; enabling leaves the effort
// level to the provider unless WithResponsesReasoningEffort is also set.
func WithResponsesThinkingEnabled(enabled bool) ResponsesOption {
	return func(r *Responses) {
		r.generation.enableThinking = new(bool)
		*r.generation.enableThinking = enabled
	}
}

// WithResponsesStore controls whether OpenAI stores generated responses for
// later retrieval. The default is false so ADK remains the state owner.
func WithResponsesStore(store bool) ResponsesOption {
	return func(r *Responses) {
		r.store = store
	}
}

// NewResponses creates a new Responses instance.
// apiKey is required. baseURL is optional; when non-empty it overrides the
// default OpenAI endpoint. retryCfg is optional; when provided it enables
// automatic retry with exponential backoff on transient errors.
func NewResponses(apiKey, baseURL, modelName string, retryCfg ...retry.Config) *Responses {
	r := newResponses(apiKey, baseURL, modelName)
	if len(retryCfg) > 0 {
		r.retryCfg = retryCfg[0]
	}
	return r
}

// NewResponsesWithOptions creates a new Responses instance with explicit retry
// and generation options.
func NewResponsesWithOptions(apiKey, baseURL, modelName string, opts ...ResponsesOption) *Responses {
	r := newResponses(apiKey, baseURL, modelName)
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

func newResponses(apiKey, baseURL, modelName string) *Responses {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Responses{
		client:    goopenai.NewClient(opts...),
		baseURL:   baseURL,
		modelName: modelName,
		retryCfg:  retry.DefaultConfig(),
	}
}

// Name returns the model identifier.
func (r *Responses) Name() string {
	return r.modelName
}

// GenerateContent sends the request to the OpenAI Responses API.
// When stream is false, exactly one complete *model.LLMResponse is yielded.
// When stream is true, partial text chunks are yielded (Partial=true) followed
// by the assembled complete response (Partial=false, TurnComplete=true).
// Transient errors are automatically retried according to the retry.Config
// provided at construction time.
func (r *Responses) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		input, err := convertResponsesInput(req.Contents)
		if err != nil {
			yield(nil, fmt.Errorf("openai: convert responses input: %w", err))
			return
		}

		tools, err := convertResponsesTools(req.Tools)
		if err != nil {
			yield(nil, fmt.Errorf("openai: convert responses tools: %w", err))
			return
		}

		params := goresponses.ResponseNewParams{
			Model: shared.ResponsesModel(req.Model),
			Input: goresponses.ResponseNewParamsInputUnion{
				OfInputItemList: goresponses.ResponseInputParam(input),
			},
			Store: goopenai.Bool(r.store),
			Tools: tools,
		}
		applyResponsesConfig(&params, cfg, r.generation)

		for resp, err := range retry.Seq2(ctx, r.retryCfg,
			func() iter.Seq2[*model.LLMResponse, error] {
				return r.callAPI(ctx, params, stream)
			},
			func(resp *model.LLMResponse) bool { return resp != nil && resp.Partial },
		) {
			if !yield(resp, err) {
				return
			}
		}
	}
}

func (r *Responses) callAPI(ctx context.Context, params goresponses.ResponseNewParams, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if !stream {
			resp, err := r.client.Responses.New(ctx, params)
			if err != nil {
				yield(nil, fmt.Errorf("openai: response: %w", err))
				return
			}
			if err := responseStatusError(resp); err != nil {
				yield(nil, err)
				return
			}
			llmResp := convertResponsesResponse(resp)
			llmResp.TurnComplete = true
			yield(llmResp, nil)
			return
		}

		s := r.client.Responses.NewStreaming(ctx, params)
		var contentBuf strings.Builder
		var finalResp *goresponses.Response

		for s.Next() {
			event := s.Current()
			switch event.Type {
			case "response.output_text.delta":
				if event.Delta == "" {
					continue
				}
				contentBuf.WriteString(event.Delta)
				if !yield(&model.LLMResponse{
					Content: model.Content{
						Role:    model.RoleAssistant,
						Content: event.Delta,
					},
					Partial: true,
				}, nil) {
					return
				}
			case "response.completed":
				resp := event.Response
				finalResp = &resp
			case "response.failed", "response.incomplete":
				resp := event.Response
				if err := responseStatusError(&resp); err != nil {
					yield(nil, err)
					return
				}
				finalResp = &resp
			case "error":
				msg := event.Message
				if msg == "" {
					msg = "stream error"
				}
				yield(nil, fmt.Errorf("openai: response stream: %s", msg))
				return
			}
		}

		if err := s.Err(); err != nil {
			yield(nil, fmt.Errorf("openai: response stream: %w", err))
			return
		}
		if finalResp == nil {
			yield(nil, fmt.Errorf("openai: response stream: missing completed response"))
			return
		}

		llmResp := convertResponsesResponse(finalResp)
		if llmResp.Content.Content == "" {
			llmResp.Content.Content = contentBuf.String()
		}
		llmResp.TurnComplete = true
		yield(llmResp, nil)
	}
}

func responseStatusError(resp *goresponses.Response) error {
	if resp == nil {
		return fmt.Errorf("openai: response: nil response")
	}
	if resp.Status != goresponses.ResponseStatusFailed {
		return nil
	}
	if resp.Error.Message != "" {
		return fmt.Errorf("openai: response failed: %s", resp.Error.Message)
	}
	return fmt.Errorf("openai: response failed")
}

func convertResponsesInput(contents []model.Content) ([]goresponses.ResponseInputItemUnionParam, error) {
	input := make([]goresponses.ResponseInputItemUnionParam, 0, len(contents))
	for _, content := range contents {
		items, err := convertResponsesContent(content)
		if err != nil {
			return nil, err
		}
		input = append(input, items...)
	}
	return input, nil
}

func convertResponsesContent(content model.Content) ([]goresponses.ResponseInputItemUnionParam, error) {
	switch content.Role {
	case model.RoleSystem:
		return []goresponses.ResponseInputItemUnionParam{
			goresponses.ResponseInputItemParamOfMessage(content.Content, goresponses.EasyInputMessageRoleSystem),
		}, nil
	case model.RoleUser:
		if len(content.Parts) == 0 {
			return []goresponses.ResponseInputItemUnionParam{
				goresponses.ResponseInputItemParamOfMessage(content.Content, goresponses.EasyInputMessageRoleUser),
			}, nil
		}
		parts, err := convertResponsesContentParts(content.Parts)
		if err != nil {
			return nil, err
		}
		return []goresponses.ResponseInputItemUnionParam{
			goresponses.ResponseInputItemParamOfInputMessage(parts, string(goresponses.ResponseInputMessageItemRoleUser)),
		}, nil
	case model.RoleAssistant:
		items := make([]goresponses.ResponseInputItemUnionParam, 0, 1+len(content.ToolCalls))
		if content.Content != "" {
			items = append(items, goresponses.ResponseInputItemParamOfMessage(content.Content, goresponses.EasyInputMessageRoleAssistant))
		}
		for _, tc := range content.ToolCalls {
			items = append(items, goresponses.ResponseInputItemParamOfFunctionCall(
				toolArgumentsString(tc.Arguments),
				tc.ID,
				tc.Name,
			))
		}
		return items, nil
	case model.RoleTool:
		toolResult := content.ToolResultValue()
		return []goresponses.ResponseInputItemUnionParam{
			goresponses.ResponseInputItemParamOfFunctionCallOutput(toolResult.ToolCallID, toolResult.Text()),
		}, nil
	default:
		return nil, fmt.Errorf("unknown role: %q", content.Role)
	}
}

func convertResponsesContentParts(parts []model.ContentPart) (goresponses.ResponseInputMessageContentListParam, error) {
	result := make(goresponses.ResponseInputMessageContentListParam, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case model.ContentPartTypeText:
			result = append(result, goresponses.ResponseInputContentParamOfInputText(p.Text))
		case model.ContentPartTypeImageURL:
			result = append(result, responsesImageContent(p.ImageURL, p.ImageDetail))
		case model.ContentPartTypeImageBase64:
			dataURI := fmt.Sprintf("data:%s;base64,%s", p.MIMEType, p.ImageBase64)
			result = append(result, responsesImageContent(dataURI, p.ImageDetail))
		default:
			return nil, fmt.Errorf("unknown content part type: %q", p.Type)
		}
	}
	return result, nil
}

func responsesImageContent(imageURL string, detail model.ImageDetail) goresponses.ResponseInputContentUnionParam {
	inputImage := goresponses.ResponseInputImageParam{
		Detail:   responsesImageDetail(detail),
		ImageURL: param.NewOpt(imageURL),
	}
	return goresponses.ResponseInputContentUnionParam{OfInputImage: &inputImage}
}

func responsesImageDetail(detail model.ImageDetail) goresponses.ResponseInputImageDetail {
	switch detail {
	case model.ImageDetailLow:
		return goresponses.ResponseInputImageDetailLow
	case model.ImageDetailHigh:
		return goresponses.ResponseInputImageDetailHigh
	case model.ImageDetailAuto, "":
		return goresponses.ResponseInputImageDetailAuto
	default:
		return goresponses.ResponseInputImageDetail(detail)
	}
}

func convertResponsesTools(tools []tool.Tool) ([]goresponses.ToolUnionParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	result := make([]goresponses.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		def := t.Definition()

		schemaJSON, err := json.Marshal(def.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: marshal schema: %w", def.Name, err)
		}
		var parameters map[string]any
		if err := json.Unmarshal(schemaJSON, &parameters); err != nil {
			return nil, fmt.Errorf("tool %q: unmarshal schema: %w", def.Name, err)
		}

		fnTool := goresponses.FunctionToolParam{
			Name:        def.Name,
			Description: param.NewOpt(def.Description),
			Parameters:  parameters,
			Strict:      param.NewOpt(false),
		}
		result = append(result, goresponses.ToolUnionParam{
			OfFunction: &fnTool,
		})
	}
	return result, nil
}

func applyResponsesConfig(params *goresponses.ResponseNewParams, cfg *model.GenerateConfig, generation generationOptions) {
	if cfg != nil {
		if cfg.MaxTokens > 0 {
			params.MaxOutputTokens = param.NewOpt(cfg.MaxTokens)
		}
		if cfg.Temperature != 0 {
			params.Temperature = param.NewOpt(cfg.Temperature)
		}
	}
	if generation.reasoningEffort != "" {
		params.Reasoning.Effort = shared.ReasoningEffort(generation.reasoningEffort)
	} else if generation.enableThinking != nil && !*generation.enableThinking {
		params.Reasoning.Effort = shared.ReasoningEffort(ReasoningEffortNone)
	}
	if generation.serviceTier != "" {
		params.ServiceTier = goresponses.ResponseNewParamsServiceTier(generation.serviceTier)
	}
}

func convertResponsesResponse(resp *goresponses.Response) *model.LLMResponse {
	msg := model.Content{
		Role:    model.RoleAssistant,
		Content: resp.OutputText(),
	}
	for _, item := range resp.Output {
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			ID:        call.CallID,
			Name:      call.Name,
			Arguments: toolArgumentsRaw(call.Arguments),
		})
	}

	return &model.LLMResponse{
		Content:      msg,
		FinishReason: convertResponsesFinishReason(resp),
		Usage:        tokenUsageFromResponses(resp.Usage, resp.JSON.Usage.Valid()),
	}
}

func tokenUsageFromResponses(usage goresponses.ResponseUsage, valid bool) *model.TokenUsage {
	if !valid {
		return nil
	}
	return &model.TokenUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

func convertResponsesFinishReason(resp *goresponses.Response) model.FinishReason {
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			return model.FinishReasonToolCalls
		}
	}
	if resp.Status == goresponses.ResponseStatusIncomplete {
		switch resp.IncompleteDetails.Reason {
		case "max_output_tokens":
			return model.FinishReasonLength
		case "content_filter":
			return model.FinishReasonContentFilter
		}
	}
	return model.FinishReasonStop
}
