package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/genai"

	"soasurs.dev/soasurs/adk/model"
	"soasurs.dev/soasurs/adk/tool"
)

// GenerateContent implements model.LLM using the Gemini GenerateContent API.
type GenerateContent struct {
	client    *genai.Client
	modelName string
}

// New creates a new GenerateContent instance for the Gemini Developer API.
// apiKey is required. modelName is the identifier of the Gemini model to use
// (e.g. "gemini-2.0-flash", "gemini-2.5-pro").
func New(ctx context.Context, apiKey, modelName string) (*GenerateContent, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create client: %w", err)
	}
	return &GenerateContent{
		client:    client,
		modelName: modelName,
	}, nil
}

// NewVertexAI creates a new GenerateContent instance backed by Google Cloud Vertex AI.
// project is the GCP project ID, location is the region (e.g. "us-central1"),
// and modelName is the Gemini model identifier (e.g. "gemini-2.0-flash").
// Authentication uses Application Default Credentials (ADC) by default; set up
// credentials via `gcloud auth application-default login` or a service account
// key file pointed to by the GOOGLE_APPLICATION_CREDENTIALS environment variable.
func NewVertexAI(ctx context.Context, project, location, modelName string) (*GenerateContent, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: location,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create vertex ai client: %w", err)
	}
	return &GenerateContent{
		client:    client,
		modelName: modelName,
	}, nil
}

// Name returns the model identifier.
func (g *GenerateContent) Name() string {
	return g.modelName
}

// GenerateContent sends the request to the Gemini GenerateContent API.
// When stream is false, exactly one complete *model.LLMResponse is yielded.
// When stream is true, partial text/reasoning chunks are yielded (Partial=true)
// followed by the assembled complete response (Partial=false, TurnComplete=true).
func (g *GenerateContent) GenerateContent(ctx context.Context, req *model.LLMRequest, cfg *model.GenerateConfig, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		contents, sysInstruction, err := convertMessages(req.Messages)
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
		if cfg != nil {
			applyConfig(gCfg, cfg)
		}

		if !stream {
			resp, err := g.client.Models.GenerateContent(ctx, req.Model, contents, gCfg)
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

		for resp, err := range g.client.Models.GenerateContentStream(ctx, req.Model, contents, gCfg) {
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

			var deltaTxt string
			var deltaReasoning string
			for idx, part := range candidate.Content.Parts {
				switch {
				case part.Thought:
					if part.Text != "" {
						reasoningBuf.WriteString(part.Text)
						deltaReasoning += part.Text
					}
				case part.FunctionCall != nil:
					id := part.FunctionCall.ID
					if id == "" {
						id = fmt.Sprintf("call_%d", idx)
					}
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, model.ToolCall{
						ID:               id,
						Name:             part.FunctionCall.Name,
						Arguments:        string(argsJSON),
						ThoughtSignature: part.ThoughtSignature,
					})
				case part.Text != "":
					contentBuf.WriteString(part.Text)
					deltaTxt += part.Text
				}
			}

			// Yield partial event for text/reasoning deltas.
			if deltaTxt != "" || deltaReasoning != "" {
				if !yield(&model.LLMResponse{
					Message: model.Message{
						Role:             model.RoleAssistant,
						Content:          deltaTxt,
						ReasoningContent: deltaReasoning,
					},
					Partial: true,
				}, nil) {
					return
				}
			}

			if candidate.FinishReason != "" {
				finishReason = convertFinishReason(candidate.FinishReason)
			}
			if resp.UsageMetadata != nil {
				usage = &model.TokenUsage{
					PromptTokens:     int64(resp.UsageMetadata.PromptTokenCount),
					CompletionTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
					TotalTokens:      int64(resp.UsageMetadata.TotalTokenCount),
				}
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
			Message: model.Message{
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
func convertMessages(msgs []model.Message) ([]*genai.Content, *genai.Content, error) {
	// Pre-build a toolCallID → name lookup so each FunctionResponse can include
	// the required function name (absent from model.Message{Role: RoleTool}).
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
			parts := convertAssistantParts(m)
			contents = append(contents, &genai.Content{Role: "model", Parts: parts})
			i++

		case model.RoleTool:
			// Batch consecutive tool-result messages into one "user" content.
			var parts []*genai.Part
			for i < len(msgs) && msgs[i].Role == model.RoleTool {
				tm := msgs[i]
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:       tm.ToolCallID,
						Name:     toolCallNames[tm.ToolCallID],
						Response: map[string]any{"output": tm.Content},
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

// convertUserParts converts a RoleUser model.Message to a Gemini Part slice.
func convertUserParts(m model.Message) ([]*genai.Part, error) {
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

// convertAssistantParts converts a RoleAssistant model.Message to a Gemini Part slice.
func convertAssistantParts(m model.Message) []*genai.Part {
	var parts []*genai.Part
	if m.Content != "" {
		parts = append(parts, &genai.Part{Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.Arguments), &args)
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
	return parts
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

// applyConfig transfers GenerateConfig settings into a Gemini GenerateContentConfig.
func applyConfig(gCfg *genai.GenerateContentConfig, cfg *model.GenerateConfig) {
	if cfg.MaxTokens > 0 {
		gCfg.MaxOutputTokens = int32(cfg.MaxTokens)
	}
	if cfg.Temperature != 0 {
		t := float32(cfg.Temperature)
		gCfg.Temperature = &t
	}

	// Map ReasoningEffort / EnableThinking → Gemini ThinkingConfig.
	// Priority: ReasoningEffort (explicit effort level) > EnableThinking (bool toggle).
	var thinkCfg *genai.ThinkingConfig
	switch {
	case cfg.ReasoningEffort == model.ReasoningEffortNone:
		zero := int32(0)
		thinkCfg = &genai.ThinkingConfig{ThinkingBudget: &zero}
	case cfg.ReasoningEffort != "":
		thinkCfg = &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingLevel:   mapReasoningEffort(cfg.ReasoningEffort),
		}
	case cfg.EnableThinking != nil && *cfg.EnableThinking:
		thinkCfg = &genai.ThinkingConfig{IncludeThoughts: true}
	case cfg.EnableThinking != nil && !*cfg.EnableThinking:
		zero := int32(0)
		thinkCfg = &genai.ThinkingConfig{ThinkingBudget: &zero}
	}
	if thinkCfg != nil {
		gCfg.ThinkingConfig = thinkCfg
	}
}

// mapReasoningEffort maps the provider-agnostic ReasoningEffort to a Gemini ThinkingLevel.
func mapReasoningEffort(effort model.ReasoningEffort) genai.ThinkingLevel {
	switch effort {
	case model.ReasoningEffortMinimal:
		return genai.ThinkingLevelMinimal
	case model.ReasoningEffortLow:
		return genai.ThinkingLevelLow
	case model.ReasoningEffortMedium:
		return genai.ThinkingLevelMedium
	case model.ReasoningEffortHigh, model.ReasoningEffortXhigh:
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
	msg := model.Message{Role: model.RoleAssistant}

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
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
					ID:               id,
					Name:             part.FunctionCall.Name,
					Arguments:        string(argsJSON),
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

	var usage *model.TokenUsage
	if resp.UsageMetadata != nil {
		usage = &model.TokenUsage{
			PromptTokens:     int64(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int64(resp.UsageMetadata.TotalTokenCount),
		}
	}

	return &model.LLMResponse{
		Message:      msg,
		FinishReason: finishReason,
		Usage:        usage,
	}
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
