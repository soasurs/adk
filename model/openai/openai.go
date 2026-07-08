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
	client     goopenai.Client
	baseURL    string
	modelName  string
	retryCfg   retry.Config
	provider   providerOptions
	generation generationOptions
}

type providerOptions struct {
	thinkingParam                    thinkingParam
	includeAssistantReasoningContent bool
	explicit                         bool
}

type thinkingParam int

const (
	thinkingParamEnableThinking thinkingParam = iota
	thinkingParamDeepSeek
)

// ReasoningEffort controls OpenAI-compatible reasoning effort.
type ReasoningEffort string

const (
	// ReasoningEffortNone disables reasoning effort when supported by the provider.
	ReasoningEffortNone ReasoningEffort = "none"
	// ReasoningEffortMinimal requests minimal reasoning effort.
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	// ReasoningEffortLow requests low reasoning effort.
	ReasoningEffortLow ReasoningEffort = "low"
	// ReasoningEffortMedium requests medium reasoning effort.
	ReasoningEffortMedium ReasoningEffort = "medium"
	// ReasoningEffortHigh requests high reasoning effort.
	ReasoningEffortHigh ReasoningEffort = "high"
	// ReasoningEffortXhigh requests extra-high reasoning effort on compatible providers.
	ReasoningEffortXhigh ReasoningEffort = "xhigh"
)

// ServiceTier specifies the OpenAI-compatible service tier for a request.
type ServiceTier string

const (
	// ServiceTierAuto lets the provider choose the service tier.
	ServiceTierAuto ServiceTier = "auto"
	// ServiceTierDefault uses the provider default service tier.
	ServiceTierDefault ServiceTier = "default"
	// ServiceTierFlex uses the flex service tier.
	ServiceTierFlex ServiceTier = "flex"
	// ServiceTierScale uses the scale service tier.
	ServiceTierScale ServiceTier = "scale"
	// ServiceTierPriority uses the priority service tier.
	ServiceTierPriority ServiceTier = "priority"
)

type generationOptions struct {
	reasoningEffort ReasoningEffort
	serviceTier     ServiceTier
	enableThinking  *bool
}

func detectProviderOptions(baseURL, modelName string) providerOptions {
	if isDeepSeekCompatible(baseURL, modelName) {
		return deepSeekProviderOptions()
	}
	return providerOptions{thinkingParam: thinkingParamEnableThinking}
}

func deepSeekProviderOptions() providerOptions {
	return providerOptions{
		thinkingParam:                    thinkingParamDeepSeek,
		includeAssistantReasoningContent: true,
	}
}

func isDeepSeekCompatible(baseURL, modelName string) bool {
	baseURL = strings.ToLower(baseURL)
	modelName = strings.ToLower(modelName)
	return strings.Contains(baseURL, "deepseek") || strings.HasPrefix(modelName, "deepseek-")
}

// Option configures a ChatCompletion instance.
type Option func(*ChatCompletion)

// WithDeepSeekCompatibility configures ChatCompletion for DeepSeek's
// OpenAI-compatible Chat Completions API.
func WithDeepSeekCompatibility() Option {
	return func(c *ChatCompletion) {
		c.provider = deepSeekProviderOptions()
		c.provider.explicit = true
	}
}

// WithRetryConfig sets the retry behavior for transient API errors.
func WithRetryConfig(cfg retry.Config) Option {
	return func(c *ChatCompletion) {
		c.retryCfg = cfg
	}
}

// WithReasoningEffort sets OpenAI-compatible reasoning effort for every call.
func WithReasoningEffort(effort ReasoningEffort) Option {
	return func(c *ChatCompletion) {
		c.generation.reasoningEffort = effort
	}
}

// WithServiceTier sets the OpenAI-compatible service tier for every call.
func WithServiceTier(tier ServiceTier) Option {
	return func(c *ChatCompletion) {
		c.generation.serviceTier = tier
	}
}

// WithThinkingEnabled explicitly enables or disables provider-specific
// thinking controls for every call. A disabled value maps to reasoning_effort
// "none" for providers that do not expose a separate thinking toggle.
func WithThinkingEnabled(enabled bool) Option {
	return func(c *ChatCompletion) {
		c.generation.enableThinking = new(bool)
		*c.generation.enableThinking = enabled
	}
}

// New creates a new ChatCompletion instance.
// apiKey is required. baseURL is optional; when non-empty it overrides the
// default OpenAI endpoint, which allows using any OpenAI-compatible provider.
// retryCfg is optional; when provided it enables automatic retry with
// exponential backoff on transient errors (rate limits, 5xx, network issues).
func New(apiKey, baseURL, modelName string, retryCfg ...retry.Config) *ChatCompletion {
	c := newChatCompletion(apiKey, baseURL, modelName)
	if len(retryCfg) > 0 {
		c.retryCfg = retryCfg[0]
	}
	return c
}

// NewWithOptions creates a new ChatCompletion instance with explicit provider
// compatibility, retry, and generation options.
func NewWithOptions(apiKey, baseURL, modelName string, opts ...Option) *ChatCompletion {
	c := newChatCompletion(apiKey, baseURL, modelName)
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func newChatCompletion(apiKey, baseURL, modelName string) *ChatCompletion {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &ChatCompletion{
		client:    goopenai.NewClient(opts...),
		baseURL:   baseURL,
		modelName: modelName,
		retryCfg:  retry.DefaultConfig(),
		provider:  detectProviderOptions(baseURL, modelName),
	}
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
		provider := c.providerOptions(req.Model)

		messages, err := convertMessagesWithOptions(req.Contents, provider)
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
			applyConfigWithOptions(&params, cfg, &reqOpts, provider, c.generation)
		} else {
			applyConfigWithOptions(&params, nil, &reqOpts, provider, c.generation)
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

func (c *ChatCompletion) providerOptions(modelName string) providerOptions {
	if c.provider.explicit {
		return c.provider
	}
	return detectProviderOptions(c.baseURL, modelName)
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
			llmResp := convertResponse(resp.Choices[0], tokenUsageFromCompletion(resp.Usage, resp.JSON.Usage.Valid()))
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
			if chunk.JSON.Usage.Valid() {
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
					Content: model.Content{
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
						Content: model.Content{
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
				acc.Arguments = append(acc.Arguments, tc.Function.Arguments...)
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
		msg := model.Content{
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
				if len(tc.Arguments) == 0 {
					tc.Arguments = json.RawMessage(`{}`)
				}
				msg.ToolCalls[idx] = *tc
			}
		}

		var usage *model.TokenUsage
		if streamUsage != nil {
			usage = tokenUsageFromCompletion(*streamUsage, true)
		}
		yield(&model.LLMResponse{
			Content:      msg,
			FinishReason: convertFinishReason(finishReasonStr),
			TurnComplete: true,
			Usage:        usage,
		}, nil)
	}
}

func convertMessagesWithOptions(msgs []model.Content, opts providerOptions) ([]goopenai.ChatCompletionMessageParamUnion, error) {
	result := make([]goopenai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		if skipMessageForProvider(m, opts) {
			continue
		}
		p, err := convertMessageWithOptions(m, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, nil
}

func convertMessage(m model.Content) (goopenai.ChatCompletionMessageParamUnion, error) {
	return convertMessageWithOptions(m, providerOptions{})
}

func skipMessageForProvider(m model.Content, opts providerOptions) bool {
	return opts.thinkingParam == thinkingParamDeepSeek &&
		m.Role == model.RoleAssistant &&
		m.Content == "" &&
		len(m.ToolCalls) == 0 &&
		m.ReasoningContent != ""
}

func includeAssistantReasoningContent(m model.Content, opts providerOptions) bool {
	return opts.includeAssistantReasoningContent &&
		m.ReasoningContent != "" &&
		len(m.ToolCalls) > 0
}

func convertMessageWithOptions(m model.Content, opts providerOptions) (goopenai.ChatCompletionMessageParamUnion, error) {
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
		if includeAssistantReasoningContent(m, opts) {
			asst.SetExtraFields(map[string]any{
				"reasoning_content": m.ReasoningContent,
			})
		}
		if len(m.ToolCalls) > 0 {
			toolCalls := make([]goopenai.ChatCompletionMessageToolCallUnionParam, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				fnParam := goopenai.ChatCompletionMessageFunctionToolCallParam{
					ID: tc.ID,
					Function: goopenai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: toolArgumentsString(tc.Arguments),
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
		toolResult := m.ToolResultValue()
		return goopenai.ToolMessage(toolResult.Text(), toolResult.ToolCallID), nil

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

func applyConfigWithOptions(
	p *goopenai.ChatCompletionNewParams,
	cfg *model.GenerateConfig,
	opts *[]option.RequestOption,
	provider providerOptions,
	generation generationOptions,
) {
	if cfg != nil {
		if cfg.MaxTokens > 0 {
			p.MaxCompletionTokens = param.NewOpt(cfg.MaxTokens)
		}
		if cfg.Temperature != 0 {
			p.Temperature = param.NewOpt(cfg.Temperature)
		}
	}
	if generation.reasoningEffort != "" {
		if provider.thinkingParam == thinkingParamDeepSeek {
			applyDeepSeekReasoningEffort(opts, generation.reasoningEffort)
		} else {
			p.ReasoningEffort = shared.ReasoningEffort(generation.reasoningEffort)
		}
	} else if generation.enableThinking != nil && !*generation.enableThinking && provider.thinkingParam != thinkingParamDeepSeek {
		// No explicit effort level set but thinking is disabled: map to "none".
		p.ReasoningEffort = shared.ReasoningEffort(ReasoningEffortNone)
	}
	// Inject the provider-specific thinking toggle only when ReasoningEffort is
	// not set; an explicit effort is the more specific control.
	if generation.enableThinking != nil && generation.reasoningEffort == "" {
		switch provider.thinkingParam {
		case thinkingParamDeepSeek:
			thinkingType := "disabled"
			if *generation.enableThinking {
				thinkingType = "enabled"
			}
			*opts = append(*opts, option.WithJSONSet("thinking.type", thinkingType))
		default:
			*opts = append(*opts, option.WithJSONSet("enable_thinking", *generation.enableThinking))
		}
	}
	if generation.serviceTier != "" {
		p.ServiceTier = goopenai.ChatCompletionNewParamsServiceTier(generation.serviceTier)
	}
}

func applyDeepSeekReasoningEffort(opts *[]option.RequestOption, effort ReasoningEffort) {
	if effort == ReasoningEffortNone {
		*opts = append(*opts, option.WithJSONSet("thinking.type", "disabled"))
		return
	}
	*opts = append(*opts, option.WithJSONSet("thinking.type", "enabled"))
	*opts = append(*opts, option.WithJSONSet("thinking.reasoning_effort", deepSeekReasoningEffort(effort)))
}

func deepSeekReasoningEffort(effort ReasoningEffort) string {
	switch effort {
	case ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh:
		return string(ReasoningEffortHigh)
	case ReasoningEffortXhigh, ReasoningEffort("max"):
		return "max"
	default:
		return string(effort)
	}
}

// convertResponse maps the first OpenAI choice and usage to model.LLMResponse.
func convertResponse(choice goopenai.ChatCompletionChoice, usage *model.TokenUsage) *model.LLMResponse {
	msg := model.Content{
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
				Arguments: toolArgumentsRaw(tc.Function.Arguments),
			})
		}
	}

	return &model.LLMResponse{
		Content:      msg,
		FinishReason: convertFinishReason(choice.FinishReason),
		Usage:        usage,
	}
}

func tokenUsageFromCompletion(usage goopenai.CompletionUsage, valid bool) *model.TokenUsage {
	if !valid {
		return nil
	}
	details := model.TokenUsageDetails{
		CachedPromptTokens:       usage.PromptTokensDetails.CachedTokens,
		AudioPromptTokens:        usage.PromptTokensDetails.AudioTokens,
		AudioCompletionTokens:    usage.CompletionTokensDetails.AudioTokens,
		ReasoningTokens:          usage.CompletionTokensDetails.ReasoningTokens,
		AcceptedPredictionTokens: usage.CompletionTokensDetails.AcceptedPredictionTokens,
		RejectedPredictionTokens: usage.CompletionTokensDetails.RejectedPredictionTokens,
	}
	return &model.TokenUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		Details:          tokenUsageDetailsPtr(details),
	}
}

func tokenUsageDetailsPtr(details model.TokenUsageDetails) *model.TokenUsageDetails {
	if details.IsZero() {
		return nil
	}
	return &details
}

func toolArgumentsString(args json.RawMessage) string {
	if len(args) == 0 {
		return "{}"
	}
	return string(args)
}

func toolArgumentsRaw(args string) json.RawMessage {
	if args == "" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(args)
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
