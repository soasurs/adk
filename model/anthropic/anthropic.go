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

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/retry"
	"github.com/soasurs/adk/tool"
)

// defaultMaxTokens is the default maximum number of tokens for a response.
const defaultMaxTokens = 4096

// defaultThinkingBudget is the default token budget for extended thinking.
// Must be ≥1024 and less than maxTokens.
const defaultThinkingBudget = 3000

// Model implements model.LLM using the Anthropic Messages API.
type Model struct {
	client     goanthropic.Client
	baseURL    string
	modelName  string
	retryCfg   retry.Config
	generation generationOptions
}

type generationOptions struct {
	enableThinking *bool
	thinkingBudget int64
}

// Option configures an Anthropic Model.
type Option func(*Model)

// WithRetryConfig sets the retry behavior for transient API errors.
func WithRetryConfig(cfg retry.Config) Option {
	return func(m *Model) {
		m.retryCfg = cfg
	}
}

// WithBaseURL overrides the Anthropic API endpoint for this adapter.
func WithBaseURL(baseURL string) Option {
	return func(m *Model) {
		m.baseURL = baseURL
	}
}

// WithThinkingEnabled explicitly enables or disables Anthropic extended thinking.
func WithThinkingEnabled(enabled bool) Option {
	return func(m *Model) {
		m.generation.enableThinking = new(bool)
		*m.generation.enableThinking = enabled
	}
}

// WithThinkingBudget sets the token budget for Anthropic extended thinking.
// A positive budget enables thinking even when WithThinkingEnabled is not set.
func WithThinkingBudget(tokens int64) Option {
	return func(m *Model) {
		m.generation.thinkingBudget = tokens
	}
}

// New creates a new Model instance.
// apiKey is required. modelName is the identifier of the Anthropic model to use
// (e.g. "claude-sonnet-4-5", "claude-opus-4-5").
// retryCfg is optional; when provided it enables automatic retry with
// exponential backoff on transient errors (rate limits, 5xx, network issues).
func New(apiKey, modelName string, retryCfg ...retry.Config) *Model {
	m := newModel(apiKey, modelName)
	if len(retryCfg) > 0 {
		m.retryCfg = retryCfg[0]
	}
	return m
}

// NewWithOptions creates a new Model instance with explicit adapter options.
func NewWithOptions(apiKey, modelName string, opts ...Option) *Model {
	return newModel(apiKey, modelName, opts...)
}

func newModel(apiKey, modelName string, opts ...Option) *Model {
	m := &Model{
		modelName: modelName,
		retryCfg:  retry.DefaultConfig(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}

	requestOpts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if m.baseURL != "" {
		requestOpts = append(requestOpts, option.WithBaseURL(m.baseURL))
	}
	m.client = goanthropic.NewClient(requestOpts...)
	return m
}

// Name returns the model identifier.
func (m *Model) Name() string {
	return m.modelName
}

// GenerateContent sends the request to the Anthropic Messages API.
// When stream is false, exactly one complete *model.LLMResponse is yielded.
// When stream is true, partial text/thinking chunks are yielded (Partial=true)
// followed by the assembled complete response (Partial=false, TurnComplete=true).
// Transient errors are automatically retried according to the retry.Config
// provided at construction time.
func (m *Model) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, system, err := convertMessages(req.Contents)
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

		applyConfig(&params, cfg, m.generation)

		for resp, err := range retry.Seq2(ctx, m.retryCfg,
			func() iter.Seq2[*model.LLMResponse, error] {
				if stream {
					return m.callAPIStreaming(ctx, params)
				}
				return m.callAPI(ctx, params)
			},
			func(r *model.LLMResponse) bool { return r != nil && r.Partial },
		) {
			if !yield(resp, err) {
				return
			}
		}
	}
}

// callAPI performs a single (non-retried) call to the Anthropic Messages API
// and returns its result as an iter.Seq2. It is called by GenerateContent,
// potentially multiple times when retries are enabled.
func (m *Model) callAPI(ctx context.Context, params goanthropic.MessageNewParams) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
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

// callAPIStreaming performs a single (non-retried) streaming call to the
// Anthropic Messages API and yields partial events followed by a complete
// response. It is called by GenerateContent when stream=true.
func (m *Model) callAPIStreaming(ctx context.Context, params goanthropic.MessageNewParams) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		stream := m.client.Messages.NewStreaming(ctx, params)

		var contentBuf strings.Builder
		var reasoningBuf strings.Builder
		toolCallAcc := make(map[int64]*model.ToolCall) // index → accumulated tool call
		var finishReason goanthropic.StopReason
		var usage *model.TokenUsage

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case goanthropic.MessageStartEvent:
				if eventUsage := tokenUsageFromAnthropic(e.Message.Usage, e.Message.JSON.Usage.Valid()); eventUsage != nil {
					usage = eventUsage
				}

			case goanthropic.ContentBlockStartEvent:
				// Capture tool_use block start.
				if toolUse := e.ContentBlock.AsToolUse(); toolUse.ID != "" {
					idx := e.Index
					if _, ok := toolCallAcc[idx]; !ok {
						toolCallAcc[idx] = &model.ToolCall{ID: toolUse.ID, Name: toolUse.Name}
					}
				}

			case goanthropic.ContentBlockDeltaEvent:
				// Yield text delta.
				if text := e.Delta.AsTextDelta(); text.Text != "" {
					contentBuf.WriteString(text.Text)
					if !yield(&model.LLMResponse{
						Content: model.Content{
							Role:    model.RoleAssistant,
							Content: text.Text,
						},
						Partial: true,
					}, nil) {
						return
					}
				}
				// Yield thinking delta.
				if thinking := e.Delta.AsThinkingDelta(); thinking.Thinking != "" {
					reasoningBuf.WriteString(thinking.Thinking)
					if !yield(&model.LLMResponse{
						Content: model.Content{
							Role:             model.RoleAssistant,
							ReasoningContent: thinking.Thinking,
						},
						Partial: true,
					}, nil) {
						return
					}
				}
				// Accumulate tool call input JSON delta.
				if inputJSON := e.Delta.AsInputJSONDelta(); inputJSON.PartialJSON != "" {
					idx := e.Index
					if tc, ok := toolCallAcc[idx]; ok {
						tc.Arguments = append(tc.Arguments, inputJSON.PartialJSON...)
					}
				}

			case goanthropic.MessageDeltaEvent:
				if e.Delta.StopReason != "" {
					finishReason = e.Delta.StopReason
				}
				usage = mergeAnthropicDeltaUsage(usage, e.Usage, e.JSON.Usage.Valid())
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, fmt.Errorf("anthropic: stream: %w", err))
			return
		}

		// Build final complete response.
		msg := model.Content{
			Role:             model.RoleAssistant,
			Content:          contentBuf.String(),
			ReasoningContent: reasoningBuf.String(),
		}
		if len(toolCallAcc) > 0 {
			msg.ToolCalls = make([]model.ToolCall, len(toolCallAcc))
			for idx, tc := range toolCallAcc {
				if int(idx) < len(msg.ToolCalls) {
					if len(tc.Arguments) == 0 {
						tc.Arguments = json.RawMessage(`{}`)
					}
					msg.ToolCalls[idx] = *tc
				}
			}
		}

		f := convertStopReason(finishReason)
		if len(msg.ToolCalls) > 0 {
			f = model.FinishReasonToolCalls
		}

		yield(&model.LLMResponse{
			Content:      msg,
			FinishReason: f,
			Usage:        usage,
			TurnComplete: true,
		}, nil)
	}
}

// convertMessages maps model.Content slice to Anthropic MessageParam slice,
// extracting system messages into the top-level system prompt.
// Consecutive RoleTool messages are batched into a single user message.
func convertMessages(msgs []model.Content) ([]goanthropic.MessageParam, []goanthropic.TextBlockParam, error) {
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
			blocks, err := convertAssistantBlocks(m)
			if err != nil {
				return nil, nil, fmt.Errorf("assistant message: %w", err)
			}
			messages = append(messages, goanthropic.NewAssistantMessage(blocks...))
			i++

		case model.RoleTool:
			// Batch consecutive tool-result messages into one user message.
			var blocks []goanthropic.ContentBlockParamUnion
			for i < len(msgs) && msgs[i].Role == model.RoleTool {
				tm := msgs[i]
				response := tm.ToolResponseValue()
				toolResult := goanthropic.ToolResultBlockParam{
					ToolUseID: response.ToolCallID,
					Content: []goanthropic.ToolResultBlockParamContentUnion{
						{OfText: &goanthropic.TextBlockParam{Text: response.Text()}},
					},
				}
				if _, failed := response.Outcome.(*tool.HandledError); failed {
					toolResult.IsError = goanthropic.Bool(true)
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

// convertUserBlocks converts a RoleUser model.Content to Anthropic ContentBlockParamUnion slice.
func convertUserBlocks(m model.Content) ([]goanthropic.ContentBlockParamUnion, error) {
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

// convertAssistantBlocks converts a RoleAssistant model.Content to Anthropic ContentBlockParamUnion slice.
func convertAssistantBlocks(m model.Content) ([]goanthropic.ContentBlockParamUnion, error) {
	var blocks []goanthropic.ContentBlockParamUnion
	if m.Content != "" {
		blocks = append(blocks, goanthropic.ContentBlockParamUnion{
			OfText: &goanthropic.TextBlockParam{Text: m.Content},
		})
	}
	for _, tc := range m.ToolCalls {
		var input any
		if err := json.Unmarshal(tc.Arguments, &input); err != nil {
			return nil, fmt.Errorf("tool call %q: parse arguments: %w", tc.Name, err)
		}
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
	return blocks, nil
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

// applyConfig transfers common GenerateConfig settings and Anthropic adapter
// options to MessageNewParams.
func applyConfig(p *goanthropic.MessageNewParams, cfg *model.GenerateConfig, generation generationOptions) {
	if cfg != nil && cfg.Temperature != 0 {
		p.Temperature = param.NewOpt(cfg.Temperature)
	}

	// Map Anthropic thinking controls. A positive ThinkingBudget is the most
	// specific signal and enables thinking even when EnableThinking is nil.
	if generation.thinkingBudget > 0 {
		p.Thinking = goanthropic.ThinkingConfigParamOfEnabled(generation.thinkingBudget)
	} else if generation.enableThinking != nil && *generation.enableThinking {
		budget := int64(defaultThinkingBudget)
		p.Thinking = goanthropic.ThinkingConfigParamOfEnabled(budget)
	} else if generation.enableThinking != nil && !*generation.enableThinking {
		disabled := goanthropic.NewThinkingConfigDisabledParam()
		p.Thinking = goanthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
	}
}

// convertResponse maps an Anthropic Message to a provider-agnostic LLMResponse.
func convertResponse(resp *goanthropic.Message) *model.LLMResponse {
	msg := model.Content{Role: model.RoleAssistant}

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
				Arguments: json.RawMessage(argsJSON),
			})
		}
	}

	msg.Content = strings.Join(textParts, "")
	msg.ReasoningContent = strings.Join(reasoningParts, "")

	finishReason := convertStopReason(resp.StopReason)
	if len(msg.ToolCalls) > 0 {
		finishReason = model.FinishReasonToolCalls
	}

	return &model.LLMResponse{
		Content:      msg,
		FinishReason: finishReason,
		Usage:        tokenUsageFromAnthropic(resp.Usage, resp.JSON.Usage.Valid()),
	}
}

func tokenUsageFromAnthropic(usage goanthropic.Usage, valid bool) *model.TokenUsage {
	if !valid {
		return nil
	}
	return tokenUsageFromAnthropicParts(
		usage.InputTokens,
		usage.CacheCreationInputTokens,
		usage.CacheReadInputTokens,
		usage.OutputTokens,
	)
}

func tokenUsageFromAnthropicDelta(usage goanthropic.MessageDeltaUsage, valid bool) *model.TokenUsage {
	if !valid {
		return nil
	}
	return tokenUsageFromAnthropicParts(
		usage.InputTokens,
		usage.CacheCreationInputTokens,
		usage.CacheReadInputTokens,
		usage.OutputTokens,
	)
}

func mergeAnthropicDeltaUsage(current *model.TokenUsage, delta goanthropic.MessageDeltaUsage, valid bool) *model.TokenUsage {
	if !valid {
		return current
	}
	deltaUsage := tokenUsageFromAnthropicDelta(delta, true)
	if current == nil {
		return deltaUsage
	}
	promptTokens := current.PromptTokens
	if deltaUsage.PromptTokens != 0 {
		promptTokens = deltaUsage.PromptTokens
	}
	return &model.TokenUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: deltaUsage.CompletionTokens,
		TotalTokens:      promptTokens + deltaUsage.CompletionTokens,
		Details:          mergeAnthropicUsageDetails(current.Details, deltaUsage.Details),
	}
}

func tokenUsageFromAnthropicParts(inputTokens, cacheCreationTokens, cacheReadTokens, outputTokens int64) *model.TokenUsage {
	promptTokens := inputTokens + cacheCreationTokens + cacheReadTokens
	details := model.TokenUsageDetails{
		CachedPromptTokens:        cacheReadTokens,
		CacheCreationPromptTokens: cacheCreationTokens,
		CacheReadPromptTokens:     cacheReadTokens,
	}
	return &model.TokenUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      promptTokens + outputTokens,
		Details:          tokenUsageDetailsPtr(details),
	}
}

func mergeAnthropicUsageDetails(current, delta *model.TokenUsageDetails) *model.TokenUsageDetails {
	if delta == nil {
		return cloneTokenUsageDetails(current)
	}
	if current == nil {
		return cloneTokenUsageDetails(delta)
	}
	merged := *current
	merged.CachedPromptTokens = delta.CachedPromptTokens
	merged.CacheCreationPromptTokens = delta.CacheCreationPromptTokens
	merged.CacheReadPromptTokens = delta.CacheReadPromptTokens
	return tokenUsageDetailsPtr(merged)
}

func cloneTokenUsageDetails(details *model.TokenUsageDetails) *model.TokenUsageDetails {
	if details == nil || details.IsZero() {
		return nil
	}
	cloned := *details
	return &cloned
}

func tokenUsageDetailsPtr(details model.TokenUsageDetails) *model.TokenUsageDetails {
	if details.IsZero() {
		return nil
	}
	return &details
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
