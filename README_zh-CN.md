# ADK - Agent Development Kit

[English](README.md)

一个轻量、符合 Go 惯用风格的 AI Agent 构建库。ADK 将 Agent 逻辑、LLM
提供商、工具和会话存储解耦：Agent 保持无状态，Runner 负责维护可持久化的事件账本。

> 早期开发声明：本项目仍在快速迭代中，API 可能随时变化。

模块路径：`github.com/soasurs/adk` · Go 版本：`1.26+`

## 特性

- 提供商无关的 OpenAI、DeepSeek、Gemini、Vertex AI 和 Anthropic 适配器
- 无状态 Agent 与基于 Event 的持久化会话历史
- 自动 tool-call 循环和结构化 tool result
- 顺序、并行以及 agent-as-tool 组合
- 基于 `iter.Seq2` 的原生 Go 流式输出
- Agent Skills 解析、发现、catalog 渲染和按需加载
- 内存、SQLite 和 PostgreSQL 会话后端
- MCP 工具，以及面向 span 的 `slog`/OpenTelemetry tracing

## 安装

```bash
go get github.com/soasurs/adk
```

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

    for event, err := range r.Run(ctx, "session-1", model.Content{Content: "你好！"}) {
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

可运行的聊天应用见 [`examples/chat`](examples/chat)。

## 文档

| 指南 | 内容 |
|---|---|
| [核心概念](docs/core-concepts_zh-CN.md) | Content、Event、Agent、流式输出、Turn 与失败恢复 |
| [模型与提供商](docs/models-and-providers_zh-CN.md) | LLM 适配器、生成选项、Responses API 和环境变量 |
| [工具与 Agent 组合](docs/tools-and-agents_zh-CN.md) | Typed tool、失败语义、MCP、顺序/并行 Agent 和 agent-as-tool |
| [会话与 Event 归档](docs/sessions_zh-CN.md) | 内存与 SQL 后端、归属、分页、持久化和归档 |
| [Tracing](docs/tracing_zh-CN.md) | `slog`、OpenTelemetry、span 层级和敏感属性 |
| [动态 System Instruction](docs/dynamic-instruction_zh-CN.md) | 请求级 instruction、生命周期、缓存和示例 |
| [Agent Skills](docs/skills_zh-CN.md) | SKILL.md 加载、catalog 注入、激活工具和资源安全 |

完整 API 文档见
[`pkg.go.dev`](https://pkg.go.dev/github.com/soasurs/adk)。

## 包结构

| 范围 | 包 |
|---|---|
| Agent | `agent`, `agent/llmagent`, `agent/sequentialagent`, `agent/parallelagent`, `agent/agentool` |
| 模型 | `model`, `model/openai`, `model/deepseek`, `model/gemini`, `model/anthropic` |
| 会话 | `session`, `session/event`, `session/memory`, `session/database` |
| 工具 | `tool`, `tool/builtin`, `tool/mcp` |
| Skills | `skill` |
| 运行时与可观测性 | `runner`, `trace`, `trace/otel` |

## 许可证

[Apache 2.0](LICENSE)
