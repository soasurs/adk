# ADK - Agent Development Kit

[简体中文](README_zh-CN.md)

A lightweight, idiomatic Go library for building AI agents. ADK keeps agent
logic separate from LLM providers, tools, and session storage, so agents stay
stateless while the runner manages the durable event ledger.

> Early development notice: this project is under active development and APIs
> may change without notice.

Module path: `github.com/soasurs/adk` · Go version: `1.26+`

## Features

- Provider-neutral adapters for OpenAI, DeepSeek, Gemini, Vertex AI, and Anthropic
- Stateless agents with event-based, persistent conversation history
- Automatic tool-call loops and structured tool results
- Sequential, parallel, and agent-as-tool composition
- Native Go streaming with `iter.Seq2`
- Agent Skills parsing, discovery, catalog rendering, and on-demand loading
- In-memory, SQLite, and PostgreSQL session backends
- MCP tools and span-oriented `slog`/OpenTelemetry tracing

## Installation

```bash
go get github.com/soasurs/adk
```

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
    assistant := llmagent.New(llmagent.Config{
        Name: "assistant", Model: llm, Instruction: "You are a helpful assistant.", Stream: true,
    })

    sessions := memory.NewMemorySessionService()
    _, _ = sessions.CreateSession(ctx, session.CreateSessionRequest{
        SessionID: "session-1", AppID: "quickstart", UserID: "user-1",
    })
    r, err := runner.New(assistant, sessions)
    if err != nil {
        panic(err)
    }

    for event, err := range r.Run(ctx, "session-1", model.Content{Content: "Hello!"}) {
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

See [`examples/chat`](examples/chat) for a runnable chat application.

## Documentation

| Guide | What it covers |
|---|---|
| [Core Concepts](docs/core-concepts.md) | Content, events, agents, streaming, turns, and failure recovery |
| [Models and Providers](docs/models-and-providers.md) | LLM adapters, generation options, Responses API, and environment variables |
| [Tools and Agent Composition](docs/tools-and-agents.md) | Typed tools, failure semantics, MCP, sequential/parallel agents, and agent-as-tool |
| [Sessions and Event Archival](docs/sessions.md) | Memory and SQL backends, ownership, pagination, persistence, and archival |
| [Tracing](docs/tracing.md) | `slog`, OpenTelemetry, span hierarchy, and sensitive attributes |
| [Dynamic System Instructions](docs/dynamic-instruction.md) | Request-scoped instructions, lifecycle, caching, and examples |
| [Agent Skills](docs/skills.md) | SKILL.md loading, catalog injection, activation tools, and resource safety |

API documentation is available on
[`pkg.go.dev`](https://pkg.go.dev/github.com/soasurs/adk).

## Package Map

| Area | Packages |
|---|---|
| Agents | `agent`, `agent/llmagent`, `agent/sequentialagent`, `agent/parallelagent`, `agent/agentool` |
| Models | `model`, `model/openai`, `model/deepseek`, `model/gemini`, `model/anthropic` |
| Sessions | `session`, `session/event`, `session/memory`, `session/database` |
| Tools | `tool`, `tool/builtin`, `tool/mcp` |
| Skills | `skill` |
| Runtime and observability | `runner`, `trace`, `trace/otel` |

## License

[Apache 2.0](LICENSE)
