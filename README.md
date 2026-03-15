# ADK — Agent Development Kit

[简体中文](README_zh-CN.md)

A lightweight, idiomatic Go library for building production-ready AI agents.
ADK decouples agent logic from LLM providers, session storage, and tool
integrations so you can compose exactly the pieces you need.

> **Module path:** `soasurs.dev/soasurs/adk`  
> **Go version:** 1.26+

---

## Features

- **Provider-agnostic LLM interface** — swap models without touching agent code
- **Stateless Agent + Stateful Runner** — clean separation of concerns
- **Automatic tool-call loop** — `LlmAgent` drives the model until a stop response
- **Pluggable session backends** — in-memory (zero config) or SQLite (persistent)
- **Message history compaction** — soft-archive old messages without deleting them
- **MCP tool integration** — connect any [Model Context Protocol](https://modelcontextprotocol.io) server
- **Snowflake IDs** — distributed, time-ordered message identifiers
- **Streaming via Go iterators** — native `iter.Seq2` for incremental output

---

## Installation

```bash
go get soasurs.dev/soasurs/adk
```

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                       Runner                         │
│  - loads session history                             │
│  - appends & persists every message                  │
│  - drives the Agent per user turn                    │
└───────────────┬──────────────────────────────────────┘
                │ []model.Message
                ▼
┌──────────────────────────────────────────────────────┐
│                    LlmAgent  (stateless)            │
│  - prepends system prompt                            │
│  - calls model.LLM.Generate in a loop                │
│  - executes tool calls, yields each message          │
└───────────────┬──────────────────────────────────────┘
                │
        ┌───────┴────────┐
        ▼                ▼
  model.LLM          tool.Tool
  (OpenAI, …)    (builtin / MCP / custom)
```

The **Runner** is stateful — it owns the session and persists every message.  
The **Agent** is stateless — it only sees the messages passed to it and yields
results; it has no memory of previous turns.

---

## Package Layout

| Package | Purpose |
|---|---|
| `agent` | `Agent` interface |
| `agent/llmagent` | LLM-backed agent with tool-call loop |
| `model` | Provider-agnostic LLM interface and message types |
| `model/openai` | OpenAI adapter |
| `session` | `Session` and `SessionService` interfaces |
| `session/memory` | In-memory session backend |
| `session/database` | SQLite session backend |
| `session/message` | Persisted message type |
| `tool` | `Tool` interface and `Definition` |
| `tool/builtin` | Built-in tools (echo, …) |
| `tool/mcp` | MCP server bridge |
| `runner` | Wires Agent + SessionService together |
| `internal/snowflake` | Snowflake node factory |

---

## Quick Start

### 1. Create an LLM

```go
import "soasurs.dev/soasurs/adk/model/openai"

llm := openai.New(openai.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
    Model:  "gpt-4o-mini",
})
```

### 2. Build an Agent

```go
import (
    "soasurs.dev/soasurs/adk/agent/llmagent"
    "soasurs.dev/soasurs/adk/model"
)

agent := llmagent.New(llmagent.LlmAgentConfig{
    Name:         "my-agent",
    Description:  "A helpful assistant",
    Model:        llm,
    SystemPrompt: "You are a helpful assistant.",
})
```

### 3. Choose a Session Backend

**In-memory** (great for testing or single-process use):

```go
import "soasurs.dev/soasurs/adk/session/memory"

svc := memory.NewSessionService()
```

**SQLite** (persistent across restarts):

```go
import "soasurs.dev/soasurs/adk/session/database"

svc, err := database.NewSessionService("sessions.db")
```

### 4. Create a Runner and Run

```go
import (
    "soasurs.dev/soasurs/adk/runner"
)

r, err := runner.New(agent, svc)
if err != nil { /* … */ }

ctx := context.Background()
sessionID := int64(1)

// Create the session once
_, _ = svc.CreateSession(ctx, sessionID)

// Send a user message and iterate over the results
for msg, err := range r.Run(ctx, sessionID, "Hello!") {
    if err != nil { /* … */ }
    fmt.Println(msg.Role, msg.Content)
}
```

---

## Core Concepts

### `model.LLM`

The central interface every LLM adapter must implement:

```go
type LLM interface {
    Name() string
    Generate(ctx context.Context, req *LLMRequest, cfg *GenerateConfig) (*LLMResponse, error)
}
```

`GenerateConfig` lets you tune temperature, top-p, reasoning effort, and
service tier in a provider-agnostic way.

### `model.Message`

A single message in a conversation. Key fields:

| Field | Description |
|---|---|
| `Role` | `system` / `user` / `assistant` / `tool` |
| `Content` | Plain-text content |
| `Parts` | Multi-modal content (text + images) |
| `ReasoningContent` | Chain-of-thought from reasoning models (informational only) |
| `ToolCalls` | Tool invocations requested by the assistant |
| `ToolCallID` | Links a tool-result message back to its call |
| `Usage` | Token consumption for this generation step |

### `agent.Agent`

```go
type Agent interface {
    Name() string
    Description() string
    Run(ctx context.Context, messages []model.Message) iter.Seq2[model.Message, error]
}
```

`Run` returns a Go iterator that yields each produced message — assistant
replies, tool results, and intermediate steps — allowing the caller to
stream output incrementally.

### `tool.Tool`

```go
type Tool interface {
    Definition() Definition   // name, description, JSON Schema
    Run(ctx context.Context, toolCallID string, arguments string) (string, error)
}
```

`LlmAgent` dispatches tool calls automatically during its generation loop.

### Session & Message Compaction

A `Session` holds a conversation's message history. The two storage backends
both support **soft compaction**: old messages are archived (marked with a
`CompactedAt` timestamp) rather than deleted.

```go
// Archive messages using a custom summariser
err = sess.CompactMessages(ctx, func(ctx context.Context, msgs []*message.Message) (*message.Message, error) {
    summary := summarise(msgs) // your logic
    return &message.Message{Role: "system", Content: summary}, nil
})

// Active messages (post-compaction)
active, _ := sess.ListMessages(ctx)

// Archived messages
archived, _ := sess.ListCompactedMessages(ctx)
```

---

## MCP Tools

Connect any MCP server and expose all its tools to your agent:

```go
import (
    "soasurs.dev/soasurs/adk/tool/mcp"
    sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

transport := sdkmcp.NewStdioTransport("my-mcp-server", []string{"--flag"}, nil)
ts := mcp.NewToolSet(transport)
if err := ts.Connect(ctx); err != nil { /* … */ }
defer ts.Close()

tools, err := ts.Tools(ctx)

agent := llmagent.New(llmagent.LlmAgentConfig{
    // …
    Tools: tools,
})
```

---

## Multi-modal Input

Pass images alongside text using `model.ContentPart`:

```go
msg := model.Message{
    Role: model.RoleUser,
    Parts: []model.ContentPart{
        {Type: model.ContentPartTypeText, Text: "What is in this image?"},
        {
            Type:        model.ContentPartTypeImageURL,
            ImageURL:    "https://example.com/photo.jpg",
            ImageDetail: model.ImageDetailHigh,
        },
    },
}
```

---

## Dependencies

| Library | Purpose |
|---|---|
| `github.com/openai/openai-go/v3` | OpenAI API client |
| `github.com/modelcontextprotocol/go-sdk` | MCP client |
| `github.com/google/jsonschema-go` | JSON Schema for tool definitions |
| `github.com/jmoiron/sqlx` | SQL query helpers |
| `github.com/mattn/go-sqlite3` | SQLite driver |
| `github.com/bwmarrin/snowflake` | Distributed ID generation |
| `github.com/stretchr/testify` | Test assertions |

---

## License

[Apache 2.0](LICENSE)
