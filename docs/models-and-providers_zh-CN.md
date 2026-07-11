# 模型与提供商

[English](models-and-providers.md) · [文档索引](../README_zh-CN.md#文档)

ADK 对外提供 provider-neutral 的 `model.LLM` 接口，把 endpoint、reasoning 和
service tier 等控制留在各 adapter 包内。

## LLM 接口

```go
type LLM interface {
    Name() string
    GenerateContent(ctx context.Context, req *LLMRequest, cfg *GenerateConfig, stream bool) iter.Seq2[*LLMResponse, error]
}
```

`model.GenerateConfig` 只包含跨 provider 的通用控制：

```go
cfg := &model.GenerateConfig{Temperature: 0.7, MaxTokens: 2048}
```

## Provider 适配器

Provider-specific 控制放在对应 adapter 中：

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

`openai.New` 使用 Chat Completions，也是 OpenAI-compatible provider 的复用路径。
`openai.NewResponsesWithOptions` 使用 OpenAI Responses API：

```go
llm := openai.NewResponsesWithOptions(apiKey, "", "gpt-5-mini",
    openai.WithResponsesReasoningEffort(openai.ReasoningEffortLow),
)
```

Responses adapter 默认发送 ADK 提供的完整历史，并设置 `store=false`，状态仍由
`Runner` 和 `SessionService` 持有。只有确实需要 provider 存储时，才显式使用
`openai.WithResponsesStore(true)`。

## 环境变量

ADK 不会全局读取环境变量。示例和集成测试构造 adapter 时使用以下名称：

| 范围 | 必需 | 可选 |
|---|---|---|
| OpenAI | `OPENAI_API_KEY` | `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_MODEL` |
| DeepSeek | `DEEPSEEK_API_KEY` | `DEEPSEEK_BASE_URL`, `DEEPSEEK_MODEL` |
| Gemini | `GEMINI_API_KEY` | `GEMINI_BASE_URL`, `GEMINI_MODEL`, `GEMINI_THINKING_MODEL` |
| Vertex AI | `VERTEX_AI_PROJECT`, `VERTEX_AI_LOCATION` | `VERTEX_AI_BASE_URL`, `VERTEX_AI_MODEL` |
| Anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_THINKING_MODEL` |
| PostgreSQL 示例 | `ADK_POSTGRES_DSN` | — |
| PostgreSQL 测试 | `ADK_TEST_POSTGRES_DSN` | — |
| Exa MCP 示例 | — | `EXA_API_KEY` |

Vertex AI 使用 Application Default Credentials。可通过
`GOOGLE_APPLICATION_CREDENTIALS` 指定 service-account key 文件。
