package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/genai"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/retry"
	"github.com/soasurs/adk/tool"
)

// GenerateContent implements model.LLM using the Gemini GenerateContent API.
type GenerateContent struct {
	client     *genai.Client
	baseURL    string
	modelName  string
	retryCfg   retry.Config
	generation generationOptions
}

// ThinkingLevel controls Gemini thinking depth.
type ThinkingLevel string

const (
	// ThinkingLevelMinimal requests minimal Gemini thinking depth.
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	// ThinkingLevelLow requests low Gemini thinking depth.
	ThinkingLevelLow ThinkingLevel = "low"
	// ThinkingLevelMedium requests medium Gemini thinking depth.
	ThinkingLevelMedium ThinkingLevel = "medium"
	// ThinkingLevelHigh requests high Gemini thinking depth.
	ThinkingLevelHigh ThinkingLevel = "high"
)

type generationOptions struct {
	thinkingLevel  ThinkingLevel
	enableThinking *bool
}

// Option configures a Gemini GenerateContent adapter.
type Option func(*GenerateContent)

// WithRetryConfig sets the retry behavior for transient API errors.
func WithRetryConfig(cfg retry.Config) Option {
	return func(g *GenerateContent) {
		g.retryCfg = cfg
	}
}

// WithBaseURL overrides the Gemini or Vertex AI API endpoint for this adapter.
func WithBaseURL(baseURL string) Option {
	return func(g *GenerateContent) {
		g.baseURL = baseURL
	}
}

// WithThinkingLevel sets Gemini thinking depth for every call.
func WithThinkingLevel(level ThinkingLevel) Option {
	return func(g *GenerateContent) {
		g.generation.thinkingLevel = level
	}
}

// WithThinkingEnabled explicitly enables or disables Gemini thinking.
func WithThinkingEnabled(enabled bool) Option {
	return func(g *GenerateContent) {
		g.generation.enableThinking = new(bool)
		*g.generation.enableThinking = enabled
	}
}

// New creates a new GenerateContent instance for the Gemini Developer API.
// apiKey is required. modelName is the identifier of the Gemini model to use
// (e.g. "gemini-2.0-flash", "gemini-2.5-pro").
// retryCfg is optional; when provided it enables automatic retry with
// exponential backoff on transient errors (rate limits, 5xx, network issues).
func New(ctx context.Context, apiKey, modelName string, retryCfg ...retry.Config) (*GenerateContent, error) {
	gc, err := NewWithOptions(ctx, apiKey, modelName)
	if err != nil {
		return nil, err
	}
	if len(retryCfg) > 0 {
		gc.retryCfg = retryCfg[0]
	}
	return gc, nil
}

// NewWithOptions creates a new GenerateContent instance for the Gemini
// Developer API with explicit adapter options.
func NewWithOptions(ctx context.Context, apiKey, modelName string, opts ...Option) (*GenerateContent, error) {
	gc := newGenerateContent(modelName, opts...)
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      apiKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: gc.baseURL},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create client: %w", err)
	}
	gc.client = client
	return gc, nil
}

// NewVertexAI creates a new GenerateContent instance backed by Google Cloud Vertex AI.
// project is the GCP project ID, location is the region (e.g. "us-central1"),
// and modelName is the Gemini model identifier (e.g. "gemini-2.0-flash").
// Authentication uses Application Default Credentials (ADC) by default; set up
// credentials via `gcloud auth application-default login` or a service account
// key file pointed to by the GOOGLE_APPLICATION_CREDENTIALS environment variable.
// retryCfg is optional; when provided it enables automatic retry with
// exponential backoff on transient errors (rate limits, 5xx, network issues).
func NewVertexAI(ctx context.Context, project, location, modelName string, retryCfg ...retry.Config) (*GenerateContent, error) {
	gc, err := NewVertexAIWithOptions(ctx, project, location, modelName)
	if err != nil {
		return nil, err
	}
	if len(retryCfg) > 0 {
		gc.retryCfg = retryCfg[0]
	}
	return gc, nil
}

// NewVertexAIWithOptions creates a new GenerateContent instance backed by
// Google Cloud Vertex AI with explicit adapter options.
func NewVertexAIWithOptions(ctx context.Context, project, location, modelName string, opts ...Option) (*GenerateContent, error) {
	gc := newGenerateContent(modelName, opts...)
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:     genai.BackendVertexAI,
		Project:     project,
		Location:    location,
		HTTPOptions: genai.HTTPOptions{BaseURL: gc.baseURL},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create vertex ai client: %w", err)
	}
	gc.client = client
	return gc, nil
}

func newGenerateContent(modelName string, opts ...Option) *GenerateContent {
	gc := &GenerateContent{
		modelName: modelName,
		retryCfg:  retry.DefaultConfig(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(gc)
		}
	}
	return gc
}

// Name returns the model identifier.
func (g *GenerateContent) Name() string {
	return g.modelName
}

// GenerateContent sends the request to the Gemini GenerateContent API.
// When stream is false, exactly one complete *model.LLMResponse is yielded.
// When stream is true, partial text/reasoning chunks are yielded (Partial=true)
// followed by the assembled complete response (Partial=false, TurnComplete=true).
// Transient errors are automatically retried according to the retry.Config
// provided at construction time.
func (g *GenerateContent) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		contents, sysInstruction, err := convertMessages(req.Contents)
		if err != nil {
			yield(nil, fmt.Errorf("gemini: convert messages: %w", err))
			return
		}

		tools, err := convertTools(req.Tools)
		if err != nil {
			yield(nil, fmt.Errorf("gemini: convert tools: %w", err))
			return
		}

		gCfg := &genai.GenerateContentConfig{
			SystemInstruction: sysInstruction,
			Tools:             tools,
		}
		applyConfig(gCfg, cfg, g.generation)

		for resp, err := range retry.Seq2(ctx, g.retryCfg,
			func() iter.Seq2[*model.LLMResponse, error] {
				return g.callAPI(ctx, req.Model, contents, gCfg, stream)
			},
			func(r *model.LLMResponse) bool { return r != nil && r.Partial },
		) {
			if !yield(resp, err) {
				return
			}
		}
	}
}

// callAPI performs a single (non-retried) call to the Gemini API and returns
// its result as an iter.Seq2. It is called by GenerateContent, potentially
// multiple times when retries are enabled.
func (g *GenerateContent) callAPI(ctx context.Context, modelName string, contents []*genai.Content, gCfg *genai.GenerateContentConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if !stream {
			resp, err := g.client.Models.GenerateContent(ctx, modelName, contents, gCfg)
			if err != nil {
				yield(nil, fmt.Errorf("gemini: generate content: %w", err))
				return
			}
			if len(resp.Candidates) == 0 {
				yield(nil, fmt.Errorf("gemini: no candidates returned"))
				return
			}
			llmResp := convertResponse(resp)
			llmResp.TurnComplete = true
			yield(llmResp, nil)
			return
		}

		// Streaming mode.
		var contentBuf strings.Builder
		var reasoningBuf strings.Builder
		var toolCalls []model.ToolCall
		var finishReason model.FinishReason
		var usage *model.TokenUsage

		for resp, err := range g.client.Models.GenerateContentStream(ctx, modelName, contents, gCfg) {
			if err != nil {
				yield(nil, fmt.Errorf("gemini: stream: %w", err))
				return
			}
			if len(resp.Candidates) == 0 {
				continue
			}
			candidate := resp.Candidates[0]
			if candidate.Content == nil {
				continue
			}

			var deltaTxt strings.Builder
			var deltaReasoning strings.Builder
			for idx, part := range candidate.Content.Parts {
				switch {
				case part.Thought:
					if part.Text != "" {
						reasoningBuf.WriteString(part.Text)
						deltaReasoning.WriteString(part.Text)
					}
				case part.FunctionCall != nil:
					id := part.FunctionCall.ID
					if id == "" {
						id = fmt.Sprintf("call_%d", idx)
					}
					argsJSON := marshalToolArgs(part.FunctionCall.Args)
					toolCalls = append(toolCalls, model.ToolCall{
						ID:               id,
						Name:             part.FunctionCall.Name,
						Arguments:        argsJSON,
						ThoughtSignature: part.ThoughtSignature,
					})
				case part.Text != "":
					contentBuf.WriteString(part.Text)
					deltaTxt.WriteString(part.Text)
				}
			}

			// Yield partial event for text/reasoning deltas.
			if deltaTxt.String() != "" || deltaReasoning.String() != "" {
				if !yield(&model.LLMResponse{
					Content: model.Content{
						Role:             model.RoleAssistant,
						Content:          deltaTxt.String(),
						ReasoningContent: deltaReasoning.String(),
					},
					Partial: true,
				}, nil) {
					return
				}
			}

			if candidate.FinishReason != "" {
				finishReason = convertFinishReason(candidate.FinishReason)
			}
			if eventUsage := tokenUsageFromGemini(resp.UsageMetadata); eventUsage != nil {
				usage = eventUsage
			}
		}

		// Determine final finish reason.
		if len(toolCalls) > 0 {
			finishReason = model.FinishReasonToolCalls
		} else if finishReason == "" {
			finishReason = model.FinishReasonStop
		}

		// Yield final complete response.
		yield(&model.LLMResponse{
			Content: model.Content{
				Role:             model.RoleAssistant,
				Content:          contentBuf.String(),
				ReasoningContent: reasoningBuf.String(),
				ToolCalls:        toolCalls,
			},
			FinishReason: finishReason,
			Usage:        usage,
			TurnComplete: true,
		}, nil)
	}
}

// convertMessages extracts a system instruction from the message list and
// converts the remaining messages to a Gemini []*genai.Content slice.
// Consecutive RoleTool messages are batched into a single "user" content so
// that all FunctionResponse parts for a single model turn are grouped correctly.
func convertMessages(msgs []model.Content) ([]*genai.Content, *genai.Content, error) {
	// Pre-build a toolCallID → name lookup so each FunctionResponse can include
	// the required function name (absent from model.Content{Role: RoleTool}).
	toolCallNames := make(map[string]string)
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			toolCallNames[tc.ID] = tc.Name
		}
	}

	var sysInstruction *genai.Content
	contents := make([]*genai.Content, 0, len(msgs))

	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch m.Role {
		case model.RoleSystem:
			// Gemini supports a single system instruction; use the first occurrence.
			if sysInstruction == nil {
				sysInstruction = &genai.Content{
					Parts: []*genai.Part{{Text: m.Content}},
				}
			}
			i++

		case model.RoleUser:
			parts, err := convertUserParts(m)
			if err != nil {
				return nil, nil, fmt.Errorf("user message: %w", err)
			}
			contents = append(contents, &genai.Content{Role: "user", Parts: parts})
			i++

		case model.RoleAssistant:
			parts, err := convertAssistantParts(m)
			if err != nil {
				return nil, nil, fmt.Errorf("assistant message: %w", err)
			}
			contents = append(contents, &genai.Content{Role: "model", Parts: parts})
			i++

		case model.RoleTool:
			// Batch consecutive tool-result messages into one "user" content.
			var parts []*genai.Part
			for i < len(msgs) && msgs[i].Role == model.RoleTool {
				tm := msgs[i]
				response := tm.ToolResponseValue()
				name := response.Name
				if name == "" {
					name = toolCallNames[response.ToolCallID]
				}
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:       response.ToolCallID,
						Name:     name,
						Response: convertToolResponse(response),
					},
				})
				i++
			}
			contents = append(contents, &genai.Content{Role: "user", Parts: parts})

		default:
			return nil, nil, fmt.Errorf("unknown message role: %q", m.Role)
		}
	}

	return contents, sysInstruction, nil
}

func convertToolResponse(response model.ToolResponse) map[string]any {
	switch outcome := response.Outcome.(type) {
	case *tool.HandledError:
		result := map[string]any{"error": outcome.Text()}
		if details, ok := decodeStructuredContent(outcome.StructuredContent); ok {
			result["details"] = details
		}
		return result
	case *tool.Result:
		if len(outcome.StructuredContent) > 0 {
			if value, ok := decodeStructuredContent(outcome.StructuredContent); ok {
				if result, ok := value.(map[string]any); ok {
					return result
				}
				return map[string]any{"result": value}
			}
		}
		return map[string]any{"output": outcome.Text()}
	default:
		return map[string]any{"output": response.Text()}
	}
}

func decodeStructuredContent(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

// convertUserParts converts a RoleUser model.Content to a Gemini Part slice.
func convertUserParts(m model.Content) ([]*genai.Part, error) {
	if len(m.Parts) == 0 {
		return []*genai.Part{{Text: m.Content}}, nil
	}
	parts := make([]*genai.Part, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case model.ContentPartTypeText:
			parts = append(parts, &genai.Part{Text: p.Text})
		case model.ContentPartTypeImageURL:
			// Pass the URL as FileData. Callers should prefer base64 for
			// maximum compatibility; not all HTTP URLs are accepted by Gemini.
			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{FileURI: p.ImageURL},
			})
		case model.ContentPartTypeImageBase64:
			raw, err := base64.StdEncoding.DecodeString(p.ImageBase64)
			if err != nil {
				return nil, fmt.Errorf("decode base64 image: %w", err)
			}
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{Data: raw, MIMEType: p.MIMEType},
			})
		default:
			return nil, fmt.Errorf("unsupported content part type: %q", p.Type)
		}
	}
	return parts, nil
}

// convertAssistantParts converts a RoleAssistant model.Content to a Gemini Part slice.
func convertAssistantParts(m model.Content) ([]*genai.Part, error) {
	var parts []*genai.Part
	if m.Content != "" {
		parts = append(parts, &genai.Part{Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return nil, fmt.Errorf("tool call %q: parse arguments: %w", tc.Name, err)
		}
		parts = append(parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: args,
			},
			ThoughtSignature: tc.ThoughtSignature,
		})
	}
	// Gemini requires at least one part in a content; fall back to empty text.
	if len(parts) == 0 {
		parts = []*genai.Part{{Text: ""}}
	}
	return parts, nil
}

// convertTools maps a tool.Tool slice to a single Gemini Tool holding all
// FunctionDeclarations. The tool input schema is forwarded as a raw JSON Schema
// via ParametersJsonSchema to avoid manual genai.Schema field mapping.
func convertTools(tools []tool.Tool) ([]*genai.Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		def := t.Definition()
		schemaJSON, err := json.Marshal(def.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: marshal schema: %w", def.Name, err)
		}
		var schemaMap any
		if err := json.Unmarshal(schemaJSON, &schemaMap); err != nil {
			return nil, fmt.Errorf("tool %q: unmarshal schema: %w", def.Name, err)
		}
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 def.Name,
			Description:          def.Description,
			ParametersJsonSchema: schemaMap,
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}, nil
}

// applyConfig transfers common GenerateConfig settings and Gemini adapter
// options into a Gemini GenerateContentConfig.
func applyConfig(gCfg *genai.GenerateContentConfig, cfg *model.GenerateConfig, generation generationOptions) {
	if cfg != nil {
		if cfg.MaxTokens > 0 {
			gCfg.MaxOutputTokens = int32(cfg.MaxTokens)
		}
		if cfg.Temperature != 0 {
			t := float32(cfg.Temperature)
			gCfg.Temperature = &t
		}
	}

	// Map Gemini adapter thinking options. ThinkingLevel is more specific than
	// the boolean toggle.
	var thinkCfg *genai.ThinkingConfig
	switch {
	case generation.thinkingLevel != "":
		thinkCfg = &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingLevel:   mapThinkingLevel(generation.thinkingLevel),
		}
	case generation.enableThinking != nil && *generation.enableThinking:
		thinkCfg = &genai.ThinkingConfig{IncludeThoughts: true}
	case generation.enableThinking != nil && !*generation.enableThinking:
		zero := int32(0)
		thinkCfg = &genai.ThinkingConfig{ThinkingBudget: &zero}
	}
	if thinkCfg != nil {
		gCfg.ThinkingConfig = thinkCfg
	}
}

// mapThinkingLevel maps the Gemini ThinkingLevel to the SDK enum.
func mapThinkingLevel(level ThinkingLevel) genai.ThinkingLevel {
	switch level {
	case ThinkingLevelMinimal:
		return genai.ThinkingLevelMinimal
	case ThinkingLevelLow:
		return genai.ThinkingLevelLow
	case ThinkingLevelMedium:
		return genai.ThinkingLevelMedium
	case ThinkingLevelHigh:
		return genai.ThinkingLevelHigh
	default:
		return ""
	}
}

// convertResponse maps the first Gemini candidate to a provider-agnostic LLMResponse.
// Thought parts (internal reasoning) are collected into ReasoningContent.
// FunctionCall parts are promoted to ToolCalls; their presence overrides the
// raw FinishReason with FinishReasonToolCalls.
func convertResponse(resp *genai.GenerateContentResponse) *model.LLMResponse {
	candidate := resp.Candidates[0]
	msg := model.Content{Role: model.RoleAssistant}

	var textParts []string
	var reasoningParts []string

	if candidate.Content != nil {
		for idx, part := range candidate.Content.Parts {
			switch {
			case part.Thought:
				if part.Text != "" {
					reasoningParts = append(reasoningParts, part.Text)
				}
			case part.FunctionCall != nil:
				id := part.FunctionCall.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", idx)
				}
				argsJSON := marshalToolArgs(part.FunctionCall.Args)
				msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
					ID:               id,
					Name:             part.FunctionCall.Name,
					Arguments:        argsJSON,
					ThoughtSignature: part.ThoughtSignature,
				})
			case part.Text != "":
				textParts = append(textParts, part.Text)
			}
		}
	}

	msg.Content = strings.Join(textParts, "")
	msg.ReasoningContent = strings.Join(reasoningParts, "")

	finishReason := model.FinishReasonStop
	if len(msg.ToolCalls) > 0 {
		finishReason = model.FinishReasonToolCalls
	} else if candidate.FinishReason != "" {
		finishReason = convertFinishReason(candidate.FinishReason)
	}

	return &model.LLMResponse{
		Content:      msg,
		FinishReason: finishReason,
		Usage:        tokenUsageFromGemini(resp.UsageMetadata),
	}
}

func tokenUsageFromGemini(usage *genai.GenerateContentResponseUsageMetadata) *model.TokenUsage {
	if usage == nil {
		return nil
	}
	details := model.TokenUsageDetails{
		CachedPromptTokens:    int64(usage.CachedContentTokenCount),
		ReasoningTokens:       int64(usage.ThoughtsTokenCount),
		ToolUsePromptTokens:   int64(usage.ToolUsePromptTokenCount),
		AudioPromptTokens:     modalityTokenCount(usage.PromptTokensDetails, genai.MediaModalityAudio),
		AudioCompletionTokens: modalityTokenCount(usage.CandidatesTokensDetails, genai.MediaModalityAudio),
	}
	return &model.TokenUsage{
		PromptTokens:     int64(usage.PromptTokenCount),
		CompletionTokens: int64(usage.CandidatesTokenCount),
		TotalTokens:      int64(usage.TotalTokenCount),
		Details:          tokenUsageDetailsPtr(details),
	}
}

func modalityTokenCount(details []*genai.ModalityTokenCount, modality genai.MediaModality) int64 {
	var count int64
	for _, detail := range details {
		if detail != nil && detail.Modality == modality {
			count += int64(detail.TokenCount)
		}
	}
	return count
}

func tokenUsageDetailsPtr(details model.TokenUsageDetails) *model.TokenUsageDetails {
	if details.IsZero() {
		return nil
	}
	return &details
}

func marshalToolArgs(args map[string]any) json.RawMessage {
	raw, err := json.Marshal(args)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

// convertFinishReason maps a Gemini FinishReason to the provider-agnostic FinishReason.
func convertFinishReason(reason genai.FinishReason) model.FinishReason {
	switch reason {
	case genai.FinishReasonStop:
		return model.FinishReasonStop
	case genai.FinishReasonMaxTokens:
		return model.FinishReasonLength
	case genai.FinishReasonSafety, genai.FinishReasonProhibitedContent,
		genai.FinishReasonBlocklist, genai.FinishReasonSPII:
		return model.FinishReasonContentFilter
	default:
		return model.FinishReasonStop
	}
}
