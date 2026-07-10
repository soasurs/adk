# ADK - Agent Development Kit

[简体中文](README_zh-CN.md)

A lightweight, idiomatic Go library for building AI agents. ADK keeps agent
logic separate from LLM providers, tools, and session storage, so agents stay
stateless while the runner manages the durable event ledger.

> Early development notice: this project is under active development and APIs
> may change without notice.

Module path: `github.com/soasurs/adk`

Go version: `1.26+`

## Features

- Provider-neutral LLM interface for OpenAI, DeepSeek, Gemini, and Anthropic adapters
- Event-first session history: complete events are persisted, partial events are transient
- Stateless agents coordinated by a stateful `runner.Runner`
- Automatic tool-call loop in `llmagent`
- Sequential and parallel agent composition
- Agent-as-tool delegation
- In-memory and SQL database session backends, tested with SQLite and PostgreSQL
- Structured tool calls and results, including MCP tool integration
- Native Go streaming with `iter.Seq2`
- Span-oriented tracing with `slog` and OpenTelemetry adapters

## Installation

```bash
go get github.com/soasurs/adk
```

## Package Layout

| Package | Purpose |
|---|---|
| `agent` | `Agent` interface |
| `agent/llmagent` | LLM-backed agent with tool-call loop |
| `agent/sequentialagent` | Sequential agent pipeline |
| `agent/parallelagent` | Parallel fan-out and merge |
| `agent/agentool` | Wrap an agent as a tool |
| `model` | Provider-neutral LLM, content, and event types |
| `model/openai` | OpenAI Chat Completions, Responses, and compatible adapter |
| `model/deepseek` | DeepSeek adapter |
| `model/gemini` | Gemini and Vertex AI adapter |
| `model/anthropic` | Anthropic adapter |
| `session` | `Session` and `SessionService` interfaces |
| `session/event` | Persisted event representation |
| `session/memory` | In-memory session backend |
| `session/database` | SQL database session backend for SQLite and PostgreSQL |
| `session/compaction` | Reference config for manual compaction |
| `tool` | Tool interface, structured calls/results, and typed function helpers |
| `tool/builtin` | Built-in tools |
| `tool/mcp` | MCP tool bridge |
| `trace` | Span-oriented tracing interfaces and `slog` tracer |
| `trace/otel` | OpenTelemetry tracing adapter |
| `runner` | Wires an agent to session storage |

## Quick Start

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

    input := model.Content{Content: "Hello!"}
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

## Core Types

### `model.Content`

`Content` is the provider-facing payload carried by events and LLM responses.
It contains the role, text, multimodal parts, reasoning text, tool calls, and
structured tool-call response linkage.

```go
content := model.Content{
    Role:    model.RoleUser,
    Content: "What is in this image?",
    Parts: []model.ContentPart{
        {Type: model.ContentPartTypeText, Text: "Describe this image."},
        {Type: model.ContentPartTypeImageURL, ImageURL: "https://example.com/photo.jpg"},
    },
}
```

### `model.Event`

`Event` is the runtime and session-history unit. Complete events form the
durable ledger; partial events are only forwarded to the caller for streaming
display and are not persisted by `Runner`. `TurnID` groups the user event and
all agent events produced by one `Runner.Run` call; it is a correlation
identifier, not an ordering key or an automatic resume checkpoint.

`Runner` persists complete events as they are produced. If the agent returns an
error or the caller stops consuming the sequence early, events created for that
incomplete turn are removed so a partial tool-call protocol is not replayed on
the next run.

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

`TokenUsage` carries provider-reported aggregate token counts. `Details` is
optional and contains common breakdowns such as cached prompt tokens, cache
creation/read tokens, reasoning tokens, tool-use prompt tokens, audio tokens,
and prediction tokens when a provider reports them.

### `agent.Agent`

```go
type Agent interface {
    Name() string
    Description() string
    Run(ctx context.Context, events []model.Event) iter.Seq2[*model.Event, error]
}
```

Agents are stateless. The runner loads active events from the session, appends
the new user event, and passes the full event history into `Run`.

Before appending that user event, the runner checks whether every assistant
tool call in active history has a matching durable tool result. If any result is
missing, `Run` returns `runner.ErrToolExecutionUnknown` (with details in
`runner.ToolExecutionUnknownError`), leaves the session unchanged, and does not
invoke the agent. The runner does not automatically retry or synthesize a
result because the external side effect may already have happened.

### `model.LLM`

```go
type LLM interface {
    Name() string
    GenerateContent(ctx context.Context, req *LLMRequest, cfg *GenerateConfig, stream bool) iter.Seq2[*LLMResponse, error]
}
```

`LLMRequest.Contents` is projected from event history before calling the
provider adapter.

### `tool.Tool`

Tools receive raw JSON arguments and return provider-neutral results. `Content`
is the plain-text fallback for providers that only accept text tool responses;
`StructuredContent` carries the JSON result for providers and storage layers
that preserve structured tool output.

```go
type Tool interface {
    Definition() Definition
    Run(ctx context.Context, call Call) (Result, error)
}
```

For most application tools, wrap a typed Go function with `tool.NewFunc`.
Input and output JSON Schemas are inferred when not provided explicitly.

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
    if input.City == "" {
        return weatherOutput{}, tool.NewFuncError("city is required")
    }
    return weatherOutput{
        City:     input.City,
        Forecast: "clear",
    }, nil
})
```

Pass tools to an LLM-backed agent:

```go
agent := llmagent.New(llmagent.Config{
    Name:  "assistant",
    Model: llm,
    Tools: []tool.Tool{weatherTool},
})
```

Use `tool.Result{IsError: true}` for handled failures whose content is safe to
send to the model. A custom `Tool` returning a non-nil Go `error` reports that
the invocation did not produce a valid result: `LlmAgent` ignores the returned
`Result`, cancels sibling tool calls, and terminates the current run without
sending the error text to the model.

For `tool.NewFunc`, ordinary handler errors follow that terminal path. Return
`tool.NewFuncError(...)` only for an expected, model-visible failure. Tool and
SDK retries belong inside the tool implementation or an explicit wrapper;
`LlmAgent` does not automatically retry tool calls because they may have side
effects.

### Generation config

`model.GenerateConfig` contains only provider-neutral controls:

```go
cfg := &model.GenerateConfig{
    Temperature: 0.7,
    MaxTokens:   2048,
}
```

Provider-specific controls and endpoint overrides live in the adapter packages:

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

`openai.New` uses the Chat Completions API and remains the OpenAI-compatible
path for providers such as DeepSeek. Use `openai.NewResponses` to call OpenAI's
Responses API:

```go
llm := openai.NewResponsesWithOptions(
    os.Getenv("OPENAI_API_KEY"),
    "",
    "gpt-5-mini",
    openai.WithResponsesReasoningEffort(openai.ReasoningEffortLow),
)
```

The Responses adapter sends the full ADK session history on each request with
`store=false` by default, so `Runner` and `SessionService` remain the state
owners. Enable OpenAI-side response storage explicitly with
`openai.WithResponsesStore(true)` when that is the intended behavior.

Each provider uses its own endpoint setting: `OPENAI_BASE_URL`,
`DEEPSEEK_BASE_URL`, `GEMINI_BASE_URL`, `VERTEX_AI_BASE_URL`, or
`ANTHROPIC_BASE_URL`.

### Environment variables

ADK does not read environment variables globally. The examples and integration
tests use these names when constructing adapters:

| Area | Required | Optional |
|---|---|---|
| OpenAI | `OPENAI_API_KEY` | `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_MODEL` |
| DeepSeek | `DEEPSEEK_API_KEY` | `DEEPSEEK_BASE_URL`, `DEEPSEEK_MODEL` |
| Gemini | `GEMINI_API_KEY` | `GEMINI_BASE_URL`, `GEMINI_MODEL`, `GEMINI_THINKING_MODEL` |
| Vertex AI | `VERTEX_AI_PROJECT`, `VERTEX_AI_LOCATION` | `VERTEX_AI_BASE_URL`, `VERTEX_AI_MODEL` |
| Anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_THINKING_MODEL` |
| PostgreSQL example | `ADK_POSTGRES_DSN` | — |
| PostgreSQL integration tests | `ADK_TEST_POSTGRES_DSN` | — |
| Exa MCP example | — | `EXA_API_KEY` |

Vertex AI authentication uses Application Default Credentials; set
`GOOGLE_APPLICATION_CREDENTIALS` when you want to point ADC at a service account
key file.

## Session Storage

Use memory for tests and ephemeral runs:

```go
svc := memory.NewMemorySessionService()
```

Sessions are created with an application-provided `SessionID` plus application
and user ownership metadata:

```go
sessionID := "session-123"
sess, err := svc.CreateSession(ctx, session.CreateSessionRequest{
    SessionID: sessionID,
    AppID:     "my-app",
    UserID:    "user-123",
})
```

Use the database backend for durable sessions. It accepts an application-owned
`*sqlx.DB`; SQLite and PostgreSQL are covered by the test suite. Applications
must import the driver they use.

SQLite:

```go
db, err := sqlx.Connect("sqlite3", "sessions.db")
if err != nil { /* handle */ }

if err := database.InitSchema(ctx, db); err != nil { /* handle */ }
svc, err := database.NewDatabaseSessionService(db)
```

For `:memory:` SQLite databases in tests, use `db.SetMaxOpenConns(1)` or a
shared-cache DSN so all operations see the same in-memory database.

PostgreSQL:

```go
db, err := sqlx.Connect("pgx", os.Getenv("ADK_POSTGRES_DSN"))
if err != nil { /* handle */ }

if err := database.InitSchema(ctx, db); err != nil { /* handle */ }
svc, err := database.NewDatabaseSessionService(db)
```

The session interface stores events:

```go
events, _ := sess.ListEvents(ctx)
page, _ := sess.GetEvents(ctx, 50, 0)
_ = sess.DeleteEvent(ctx, events[0].EventID)
```

## Manual Compaction

ADK does not automatically summarize or compact history. Applications decide
when to summarize old events and then persist that state with `CompactEvents`.

```go
events, _ := sess.ListEvents(ctx)

splitID := events[4].EventID // first event to keep
summary := &event.Event{
    EventID:   nextID(),
    Author:    "compactor",
    Role:      string(model.RoleSystem),
    Content:   "Summary of earlier conversation...",
    CreatedAt: time.Now().UnixMilli(),
    UpdatedAt: time.Now().UnixMilli(),
}

_ = sess.CompactEvents(ctx, splitID, summary)
```

Partial events are never persisted and therefore never need compaction.

## Agent Composition

Sequential agents pass complete events from each step to the next:

```go
pipeline, err := sequentialagent.New(sequentialagent.Config{
    Name:   "research-pipeline",
    Agents: []agent.Agent{researcher, drafter, reviewer},
})
```

Parallel agents collect complete events from each branch and merge them:

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

## MCP Tools

```go
transport := sdkmcp.NewStdioTransport("my-mcp-server", []string{"--flag"}, nil)
toolSet := mcp.NewToolSet(transport)
if err := toolSet.Connect(ctx); err != nil { /* handle */ }
defer toolSet.Close()

tools, err := toolSet.Tools(ctx)
```

Pass `tools` to `llmagent.Config.Tools`.

## Tracing

ADK exposes span-oriented tracing for runtime operations. The core SDK defines
a small `trace.Tracer` interface, and `Runner` propagates the active span context
through session storage, agents, LLM calls, and tool calls.

Use `slog` for local debugging:

```go
logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(trace.NewSlogTracer(logger)),
)
```

Use OpenTelemetry when your application has configured a global
`TracerProvider`:

```go
import adkotel "github.com/soasurs/adk/trace/otel"

r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(adkotel.NewTracer()),
)
```

The OTel adapter creates internal spans such as:

- `adk.runner.run`
- `adk.runner.lock`
- `adk.session.load`
- `adk.session.persist_event`
- `adk.agent.run`
- `adk.llm.iteration`
- `adk.llm.call`
- `adk.tool.call`

Each model-requested tool invocation gets its own `adk.tool.call` span. When a
model response requests multiple tools, those spans appear as parallel children
under the same `adk.llm.iteration` span. Streaming partial deltas are not emitted
as individual spans; the final LLM span records `adk.partial_responses`.

By default, the OTel adapter does not attach `session_id`, `app_id`, or
`user_id` attributes. Enable them explicitly when your telemetry backend is
allowed to receive those identifiers:

```go
tracer := adkotel.NewTracer(adkotel.WithSensitiveAttributes(true))
```

## License

[Apache 2.0](LICENSE)
