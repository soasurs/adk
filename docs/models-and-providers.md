# Models and Providers

[简体中文](models-and-providers_zh-CN.md) · [Documentation index](../README.md#documentation)

ADK exposes a provider-neutral `model.LLM` interface while keeping endpoint,
reasoning, and service-tier controls in each adapter package.

## LLM interface

```go
type LLM interface {
    Name() string
    GenerateContent(ctx context.Context, req *LLMRequest, cfg *GenerateConfig, stream bool) iter.Seq2[*LLMResponse, error]
}
```

`model.GenerateConfig` contains only shared controls:

```go
cfg := &model.GenerateConfig{Temperature: 0.7, MaxTokens: 2048}
```

## Provider adapters

Provider-specific controls belong to their adapters:

```go
openAILLM := openai.NewWithOptions(apiKey, baseURL, "gpt-4o-mini",
    openai.WithReasoningEffort(openai.ReasoningEffortHigh),
    openai.WithServiceTier(openai.ServiceTierFlex),
)

deepSeekLLM := deepseek.NewWithOptions(apiKey, deepseek.ModelV4Pro,
    deepseek.WithReasoningEffort(deepseek.ReasoningEffortMax),
)

geminiLLM, err := gemini.NewWithOptions(ctx, apiKey, "gemini-2.5-pro",
    gemini.WithBaseURL(baseURL),
    gemini.WithThinkingLevel(gemini.ThinkingLevelHigh),
)

anthropicLLM := anthropic.NewWithOptions(apiKey, "claude-sonnet-4-5",
    anthropic.WithBaseURL(baseURL),
    anthropic.WithThinkingBudget(3000),
)
```

`openai.New` uses Chat Completions and is also the OpenAI-compatible path.
`openai.NewResponsesWithOptions` uses the OpenAI Responses API:

```go
llm := openai.NewResponsesWithOptions(apiKey, "", "gpt-5-mini",
    openai.WithResponsesReasoningEffort(openai.ReasoningEffortLow),
)
```

The Responses adapter sends ADK-provided history and uses `store=false` by
default, keeping `Runner` and `SessionService` as state owners. Enable provider
storage only when intentional with `openai.WithResponsesStore(true)`.

## Environment variables

ADK does not read environment variables globally. Examples and integration
tests use the following names when constructing adapters:

| Area | Required | Optional |
|---|---|---|
| OpenAI | `OPENAI_API_KEY` | `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_MODEL` |
| DeepSeek | `DEEPSEEK_API_KEY` | `DEEPSEEK_BASE_URL`, `DEEPSEEK_MODEL` |
| Gemini | `GEMINI_API_KEY` | `GEMINI_BASE_URL`, `GEMINI_MODEL`, `GEMINI_THINKING_MODEL` |
| Vertex AI | `VERTEX_AI_PROJECT`, `VERTEX_AI_LOCATION` | `VERTEX_AI_BASE_URL`, `VERTEX_AI_MODEL` |
| Anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_THINKING_MODEL` |
| PostgreSQL example | `ADK_POSTGRES_DSN` | — |
| PostgreSQL tests | `ADK_TEST_POSTGRES_DSN` | — |
| Exa MCP example | — | `EXA_API_KEY` |

Vertex AI uses Application Default Credentials. Set
`GOOGLE_APPLICATION_CREDENTIALS` to select a service-account key file.
