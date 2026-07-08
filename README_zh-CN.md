# ADK - Agent Development Kit

一个轻量、符合 Go 惯用风格的 AI Agent 构建库。ADK 将 Agent 逻辑、LLM
提供商、工具和会话存储解耦：Agent 保持无状态，Runner 负责维护可持久化的
事件账本。

> 早期开发声明：本项目仍在快速迭代中，API 可能随时变化。

模块路径：`github.com/soasurs/adk`

Go 版本：`1.26+`

## 特性

- 提供商无关的 LLM 接口，支持 OpenAI、DeepSeek、Gemini、Anthropic 适配器
- Event-first 会话历史：完整事件会持久化，partial 事件只用于流式展示
- 无状态 Agent，由有状态 `runner.Runner` 协调
- `llmagent` 内置自动 tool-call 循环
- 支持顺序和并行 Agent 组合
- 支持将 Agent 包装成 Tool
- 内存与 SQL 数据库会话后端，已覆盖 SQLite 和 PostgreSQL 测试
- 结构化 tool call 和 tool result，包括 MCP 工具集成
- 使用 `iter.Seq2` 做原生 Go 流式输出
- 面向 span 的 tracing，支持 `slog` 和 OpenTelemetry 适配器

## 安装

```bash
go get github.com/soasurs/adk
```

## 包结构

| 包 | 职责 |
|---|---|
| `agent` | `Agent` 接口 |
| `agent/llmagent` | 带 tool-call 循环的 LLM Agent |
| `agent/sequentialagent` | 顺序流水线 |
| `agent/parallelagent` | 并行扇出与合并 |
| `agent/agentool` | 将 Agent 包装成 Tool |
| `model` | 提供商无关的 LLM、Content、Event 类型 |
| `model/openai` | OpenAI Chat Completions、Responses 和兼容适配器 |
| `model/deepseek` | DeepSeek 适配器 |
| `model/gemini` | Gemini 和 Vertex AI 适配器 |
| `model/anthropic` | Anthropic 适配器 |
| `session` | `Session` 与 `SessionService` 接口 |
| `session/event` | 持久化事件类型 |
| `session/memory` | 内存会话后端 |
| `session/database` | 支持 SQLite 和 PostgreSQL 的 SQL 数据库会话后端 |
| `session/compaction` | 手动压缩用的参考配置 |
| `tool` | Tool 接口、结构化 call/result，以及 typed function 辅助函数 |
| `tool/builtin` | 内置工具 |
| `tool/mcp` | MCP 工具桥接 |
| `trace` | 面向 span 的 tracing 接口和 `slog` tracer |
| `trace/otel` | OpenTelemetry tracing 适配器 |
| `runner` | 串联 Agent 与会话存储 |

## 快速开始

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/soasurs/adk/agent/llmagent"
    "github.com/soasurs/adk/model"
    "github.com/soasurs/adk/model/openai"
    "github.com/soasurs/adk/runner"
    "github.com/soasurs/adk/session"
    "github.com/soasurs/adk/session/memory"
)

func main() {
    ctx := context.Background()

    llm := openai.New(os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_BASE_URL"), "gpt-4o-mini")
    agent := llmagent.New(llmagent.Config{
        Name:        "assistant",
        Description: "A helpful assistant",
        Model:       llm,
        Instruction: "You are a helpful assistant.",
        Stream:      true,
    })

    sessions := memory.NewMemorySessionService()
    const sessionID = "session-1"
    _, _ = sessions.CreateSession(ctx, session.CreateSessionRequest{
        SessionID: sessionID,
        AppID:     "quickstart",
        UserID:    "user-1",
    })

    r, err := runner.New(agent, sessions)
    if err != nil {
        panic(err)
    }

    input := model.Content{Content: "你好！"}
    for event, err := range r.Run(ctx, sessionID, input) {
        if err != nil {
            panic(err)
        }
        if event.Partial {
            fmt.Print(event.Content.Content)
            continue
        }
        fmt.Printf("\n%s: %s\n", event.Content.Role, event.Content.Content)
    }
}
```

## 核心类型

### `model.Content`

`Content` 是 LLM 适配器看到的载荷，包含 role、文本、多模态 parts、推理文本、
tool calls 以及结构化 tool-call 响应关联。

```go
content := model.Content{
    Role:    model.RoleUser,
    Content: "这张图片里有什么？",
    Parts: []model.ContentPart{
        {Type: model.ContentPartTypeText, Text: "描述这张图片。"},
        {Type: model.ContentPartTypeImageURL, ImageURL: "https://example.com/photo.jpg"},
    },
}
```

### `model.Event`

`Event` 是运行时输出和会话历史的基本单位。完整事件组成持久化账本；partial 事件
只会转发给调用方做实时展示，不会被 `Runner` 持久化。`TurnID` 会把一次
`Runner.Run` 调用中的 user event 和所有 agent events 分到同一组；它是关联 ID，
不是排序键，也不是自动恢复检查点。

```go
type Event struct {
    ID           int64
    SessionID    string
    TurnID       string
    Author       string
    Content      model.Content
    FinishReason model.FinishReason
    Usage        *model.TokenUsage
    Partial      bool
    CreatedAt    int64
    UpdatedAt    int64
}
```

### `model.TokenUsage`

`TokenUsage` 保存 provider 返回的聚合 token 计数。`Details` 是可选字段，
用于记录 provider 提供的常见细分项，例如 cached prompt tokens、cache
creation/read tokens、reasoning tokens、tool-use prompt tokens、audio tokens
和 prediction tokens。

### `agent.Agent`

```go
type Agent interface {
    Name() string
    Description() string
    Run(ctx context.Context, events []model.Event) iter.Seq2[*model.Event, error]
}
```

Agent 是无状态的。Runner 会从 session 加载 active events，追加新的 user event，
再把完整 event history 传给 Agent。

### `model.LLM`

```go
type LLM interface {
    Name() string
    GenerateContent(ctx context.Context, req *LLMRequest, cfg *GenerateConfig, stream bool) iter.Seq2[*LLMResponse, error]
}
```

调用 provider 之前，`LLMRequest.Contents` 会从 event history 投影出来。

### `tool.Tool`

Tool 接收 raw JSON arguments，并返回 provider-neutral result。`Content` 是给只支持
文本 tool response 的 provider 使用的纯文本 fallback；`StructuredContent` 保存 JSON
结果，供支持结构化 tool output 的 provider 和存储层使用。

```go
type Tool interface {
    Definition() Definition
    Run(ctx context.Context, call Call) (Result, error)
}
```

大多数应用工具可以用 `tool.NewFunc` 包装 typed Go function。如果没有显式提供
schema，输入和输出 JSON Schema 会从 Go 类型自动推导。

```go
type weatherInput struct {
    City string `json:"city"`
}

type weatherOutput struct {
    City     string `json:"city"`
    Forecast string `json:"forecast"`
}

weatherTool, err := tool.NewFunc(tool.Definition{
    Name:        "weather",
    Description: "Get a short weather forecast for a city.",
}, func(ctx context.Context, input weatherInput) (weatherOutput, error) {
    return weatherOutput{
        City:     input.City,
        Forecast: "clear",
    }, nil
})
```

将 tools 传给 LLM Agent：

```go
agent := llmagent.New(llmagent.Config{
    Name:  "assistant",
    Model: llm,
    Tools: []tool.Tool{weatherTool},
})
```

面向模型可见的工具失败使用 `tool.Result{IsError: true}`。SDK、transport 或框架
层面的失败返回 Go `error`，由 agent runtime 标记为执行失败。

### 生成配置

`model.GenerateConfig` 只包含 provider-neutral 的通用控制：

```go
cfg := &model.GenerateConfig{
    Temperature: 0.7,
    MaxTokens:   2048,
}
```

Provider-specific 控制和 endpoint 覆盖放在对应 adapter 包里：

```go
llm := openai.NewWithOptions(
    os.Getenv("OPENAI_API_KEY"),
    os.Getenv("OPENAI_BASE_URL"),
    "gpt-4o-mini",
    openai.WithReasoningEffort(openai.ReasoningEffortHigh),
    openai.WithServiceTier(openai.ServiceTierFlex),
)

deepSeekLLM := deepseek.NewWithOptions(
    os.Getenv("DEEPSEEK_API_KEY"),
    deepseek.ModelV4Pro,
    deepseek.WithReasoningEffort(deepseek.ReasoningEffortMax),
)

geminiLLM, err := gemini.NewWithOptions(
    ctx,
    os.Getenv("GEMINI_API_KEY"),
    "gemini-2.5-pro",
    gemini.WithBaseURL(os.Getenv("GEMINI_BASE_URL")),
    gemini.WithThinkingLevel(gemini.ThinkingLevelHigh),
)

anthropicLLM := anthropic.NewWithOptions(
    os.Getenv("ANTHROPIC_API_KEY"),
    "claude-sonnet-4-5",
    anthropic.WithBaseURL(os.Getenv("ANTHROPIC_BASE_URL")),
    anthropic.WithThinkingBudget(3000),
)
```

`openai.New` 使用 Chat Completions API，也是 DeepSeek 等 OpenAI-compatible
provider 的复用路径。要调用 OpenAI Responses API，使用 `openai.NewResponses`：

```go
llm := openai.NewResponsesWithOptions(
    os.Getenv("OPENAI_API_KEY"),
    "",
    "gpt-5-mini",
    openai.WithResponsesReasoningEffort(openai.ReasoningEffortLow),
)
```

Responses adapter 默认每次请求都发送完整 ADK session history，并设置
`store=false`，因此状态仍由 `Runner` 和 `SessionService` 管理。如果确实要启用
OpenAI 侧 response 存储，需要显式使用 `openai.WithResponsesStore(true)`。

每个 provider 使用自己的 endpoint 配置：`OPENAI_BASE_URL`、
`DEEPSEEK_BASE_URL`、`GEMINI_BASE_URL`、`VERTEX_AI_BASE_URL` 或
`ANTHROPIC_BASE_URL`。

### 环境变量

ADK 不会全局读取环境变量。示例和集成测试在构造 adapter 时使用这些变量名：

| 范围 | 必需 | 可选 |
|---|---|---|
| OpenAI | `OPENAI_API_KEY` | `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_MODEL` |
| DeepSeek | `DEEPSEEK_API_KEY` | `DEEPSEEK_BASE_URL`, `DEEPSEEK_MODEL` |
| Gemini | `GEMINI_API_KEY` | `GEMINI_BASE_URL`, `GEMINI_MODEL`, `GEMINI_THINKING_MODEL` |
| Vertex AI | `VERTEX_AI_PROJECT`, `VERTEX_AI_LOCATION` | `VERTEX_AI_BASE_URL`, `VERTEX_AI_MODEL` |
| Anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_THINKING_MODEL` |
| PostgreSQL 示例 | `ADK_POSTGRES_DSN` | — |
| PostgreSQL 集成测试 | `ADK_TEST_POSTGRES_DSN` | — |
| Exa MCP 示例 | — | `EXA_API_KEY` |

Vertex AI 认证使用 Application Default Credentials；如果要指定 service
account key 文件，可以设置 `GOOGLE_APPLICATION_CREDENTIALS`。

## 会话存储

测试或临时运行可以使用内存后端：

```go
svc := memory.NewMemorySessionService()
```

创建 session 时需要传入应用侧提供的 `SessionID`，以及应用和用户归属信息：

```go
sessionID := "session-123"
sess, err := svc.CreateSession(ctx, session.CreateSessionRequest{
    SessionID: sessionID,
    AppID:     "my-app",
    UserID:    "user-123",
})
```

需要持久化时可以使用 database 后端。它接收应用自己持有的 `*sqlx.DB`；
SQLite 和 PostgreSQL 都有测试覆盖。应用需要自行 import 对应 driver。

SQLite：

```go
db, err := sqlx.Connect("sqlite3", "sessions.db")
if err != nil { /* handle */ }

if err := database.InitSchema(ctx, db); err != nil { /* handle */ }
svc, err := database.NewDatabaseSessionService(db)
```

测试里如果使用 `:memory:` SQLite，需要设置 `db.SetMaxOpenConns(1)`，或者使用
shared-cache DSN，确保所有操作看到同一个内存数据库。

PostgreSQL：

```go
db, err := sqlx.Connect("pgx", os.Getenv("ADK_POSTGRES_DSN"))
if err != nil { /* handle */ }

if err := database.InitSchema(ctx, db); err != nil { /* handle */ }
svc, err := database.NewDatabaseSessionService(db)
```

Session 存储的是 events：

```go
events, _ := sess.ListEvents(ctx)
page, _ := sess.GetEvents(ctx, 50, 0)
_ = sess.DeleteEvent(ctx, events[0].EventID)
```

## 手动压缩

ADK 不会自动摘要或压缩上下文。应用层自行决定何时摘要旧 events，然后通过
`CompactEvents` 持久化压缩状态。

```go
events, _ := sess.ListEvents(ctx)

splitID := events[4].EventID // 第一个要保留的 event
summary := &event.Event{
    EventID:   nextID(),
    Author:    "compactor",
    Role:      string(model.RoleSystem),
    Content:   "前文摘要...",
    CreatedAt: time.Now().UnixMilli(),
    UpdatedAt: time.Now().UnixMilli(),
}

_ = sess.CompactEvents(ctx, splitID, summary)
```

partial events 不会被持久化，因此也不参与压缩。

## Agent 组合

顺序 Agent 会把每一步产生的完整 events 传给下一步：

```go
pipeline, err := sequentialagent.New(sequentialagent.Config{
    Name:   "research-pipeline",
    Agents: []agent.Agent{researcher, drafter, reviewer},
})
```

并行 Agent 会收集每个分支的完整 events，再合并成一个 event：

```go
fanout, err := parallelagent.New(parallelagent.Config{
    Name:   "multi-model",
    Agents: []agent.Agent{gptAgent, claudeAgent, geminiAgent},
    MergeFunc: func(results []parallelagent.AgentOutput) model.Event {
        return model.Event{
            Author: "multi-model",
            Content: model.Content{
                Role:    model.RoleAssistant,
                Content: "merged answer",
            },
        }
    },
})
```

## MCP 工具

```go
transport := sdkmcp.NewStdioTransport("my-mcp-server", []string{"--flag"}, nil)
toolSet := mcp.NewToolSet(transport)
if err := toolSet.Connect(ctx); err != nil { /* handle */ }
defer toolSet.Close()

tools, err := toolSet.Tools(ctx)
```

把 `tools` 传入 `llmagent.Config.Tools` 即可。

## Tracing

ADK 提供面向 span 的运行时 tracing。核心 SDK 定义了很小的
`trace.Tracer` 接口，`Runner` 会把 active span context 继续传给 session
存储、agents、LLM 调用和 tool 调用。

本地调试可以使用 `slog`：

```go
logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(trace.NewSlogTracer(logger)),
)
```

如果应用已经配置了全局 OpenTelemetry `TracerProvider`，可以使用 OTel 适配器：

```go
import adkotel "github.com/soasurs/adk/trace/otel"

r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(adkotel.NewTracer()),
)
```

OTel 适配器会创建这些 internal spans：

- `adk.runner.run`
- `adk.runner.lock`
- `adk.session.load`
- `adk.session.persist_event`
- `adk.agent.run`
- `adk.llm.iteration`
- `adk.llm.call`
- `adk.tool.call`

每个模型请求的 tool invocation 都会有自己的 `adk.tool.call` span。如果一次模型
响应里请求了多个 tools，这些 spans 会作为同一个 `adk.llm.iteration` 的并列
children 出现。Streaming partial delta 默认不会逐条生成 span；最终 LLM span 会
记录 `adk.partial_responses`。

默认情况下，OTel 适配器不会把 `session_id`、`app_id`、`user_id` 写入 span
attributes。只有当你的 telemetry 后端允许接收这些标识时才显式开启：

```go
tracer := adkotel.NewTracer(adkotel.WithSensitiveAttributes(true))
```

## 许可证

[Apache 2.0](LICENSE)
