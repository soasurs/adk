# ADK — Agent Development Kit

[简体中文](README_zh-CN.md)

A lightweight, idiomatic Go library for building production-ready AI agents.
ADK decouples agent logic from LLM providers, session storage, and tool
integrations so you can compose exactly the pieces you need.

> **Module path:** `github.com/soasurs/adk`  
> **Go version:** 1.26+

---

## Features

- **Provider-agnostic LLM interface** — swap OpenAI, Gemini, or Anthropic without touching agent code
- **Stateless Agent + Stateful Runner** — clean separation of concerns
- **Automatic tool-call loop** — `LlmAgent` drives the model until a stop response
- **Agent composition** — chain agents sequentially or run them in parallel
- **Agent as Tool** — delegate tasks to sub-agents via function calling
- **Pluggable session backends** — in-memory (zero config) or SQLite (persistent)
- **Message history compaction** — soft-archive old messages without deleting them
- **MCP tool integration** — connect any [Model Context Protocol](https://modelcontextprotocol.io) server
- **Snowflake IDs** — distributed, time-ordered message identifiers
- **Streaming via Go iterators** — native `iter.Seq2` for incremental output

---

## Installation

```bash
go get github.com/soasurs/adk
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
| `agent/sequential` | Sequential agent composition (pipeline) |
| `agent/parallel` | Parallel agent composition (fan-out) |
| `agent/agentool` | Wrap agents as tools for delegation |
| `model` | Provider-agnostic LLM interface and message types |
| `model/openai` | OpenAI adapter |
| `model/gemini` | Google Gemini adapter (Gemini API & Vertex AI) |
| `model/anthropic` | Anthropic Claude adapter |
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

**OpenAI:**

```go
import "github.com/soasurs/adk/model/openai"

llm := openai.New(openai.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
    Model:  "gpt-4o-mini",
})
```

**Google Gemini:**

```go
import "github.com/soasurs/adk/model/gemini"

llm, err := gemini.New(ctx, os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")
// Or use Vertex AI:
// llm, err := gemini.NewVertexAI(ctx, "my-project", "us-central1", "gemini-2.0-flash")
```

**Anthropic Claude:**

```go
import "github.com/soasurs/adk/model/anthropic"

llm := anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), "claude-sonnet-4-5")
```

### 2. Build an Agent

```go
import (
    "github.com/soasurs/adk/agent/llmagent"
    "github.com/soasurs/adk/model"
)

agent := llmagent.New(llmagent.Config{
    Name:         "my-agent",
    Description:  "A helpful assistant",
    Model:        llm,
    Instruction:  "You are a helpful assistant.",
})
```

### 3. Choose a Session Backend

**In-memory** (great for testing or single-process use):

```go
import "github.com/soasurs/adk/session/memory"

svc := memory.NewSessionService()
```

**SQLite** (persistent across restarts):

```go
import "github.com/soasurs/adk/session/database"

svc, err := database.NewSessionService("sessions.db")
```

### 4. Create a Runner and Run

```go
import (
    "github.com/soasurs/adk/runner"
)

r, err := runner.New(agent, svc)
if err != nil { /* … */ }

ctx := context.Background()
sessionID := int64(1)

// Create the session once
_, _ = svc.CreateSession(ctx, sessionID)

// Send a user message and iterate over the results
for event, err := range r.Run(ctx, sessionID, "Hello!") {
    if err != nil { /* … */ }
    if event.Partial {
        // Streaming fragment — display in real-time
        fmt.Print(event.Message.Content)
    } else {
        // Complete message — persist or process
        fmt.Println(event.Message.Role, event.Message.Content)
    }
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
    Run(ctx context.Context, messages []model.Message) iter.Seq2[*model.Event, error]
}
```

`Run` returns a Go iterator that yields each produced event — assistant
replies, tool results, and intermediate steps — allowing the caller to
stream output incrementally.

Events carry a `Partial` flag: when `true`, the event is a streaming fragment
for real-time display; when `false`, it is a complete, persistable message.

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
    "github.com/soasurs/adk/tool/mcp"
    sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

transport := sdkmcp.NewStdioTransport("my-mcp-server", []string{"--flag"}, nil)
ts := mcp.NewToolSet(transport)
if err := ts.Connect(ctx); err != nil { /* … */ }
defer ts.Close()

tools, err := ts.Tools(ctx)

agent := llmagent.New(llmagent.Config{
    // …
    Tools: tools,
})
```

---

## Agent Composition

### Sequential Agent

Chain multiple agents into a pipeline where each agent sees the output of all
previous agents:

```go
import "github.com/soasurs/adk/agent/sequential"

pipeline := sequential.New(sequential.Config{
    Name:        "research-pipeline",
    Description: "Research, draft, and review",
    Agents: []agent.Agent{
        researchAgent,  // Step 1: gather information
        draftAgent,     // Step 2: write draft
        reviewAgent,    // Step 3: review and polish
    },
})
```

### Parallel Agent

Run multiple agents concurrently and merge their outputs:

```go
import "github.com/soasurs/adk/agent/parallel"

ensemble := parallel.New(parallel.Config{
    Name:        "multi-model-ensemble",
    Description: "Get answers from multiple models",
    Agents: []agent.Agent{
        gpt4Agent,
        claudeAgent,
        geminiAgent,
    },
    // Optional: custom merge function
    MergeFunc: func(results []parallel.AgentOutput) model.Message {
        // Combine outputs from all agents
    },
})
```

### Agent as Tool

Delegate tasks to sub-agents via the LLM's function-calling mechanism:

```go
import "github.com/soasurs/adk/agent/agentool"

// Wrap an agent as a tool
calculatorTool := agentool.New(calculatorAgent)

// Give it to another agent
orchestrator := llmagent.New(llmagent.Config{
    Name:        "orchestrator",
    Description: "Delegates calculations",
    Model:       llm,
    Tools:       []tool.Tool{calculatorTool},
    Instruction: "Use the calculator tool for math problems.",
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
| `google.golang.org/genai` | Google Gemini API client |
| `github.com/anthropics/anthropic-sdk-go` | Anthropic Claude API client |
| `github.com/modelcontextprotocol/go-sdk` | MCP client |
| `github.com/google/jsonschema-go` | JSON Schema for tool definitions |
| `github.com/jmoiron/sqlx` | SQL query helpers |
| `github.com/mattn/go-sqlite3` | SQLite driver |
| `github.com/bwmarrin/snowflake` | Distributed ID generation |
| `github.com/stretchr/testify` | Test assertions |

---

## License

[Apache 2.0](LICENSE)
