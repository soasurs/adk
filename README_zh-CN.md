# ADK — 智能体开发套件

一个轻量、符合 Go 惯用风格的生产级 AI 智能体构建库。  
ADK 将智能体逻辑与 LLM 提供商、会话存储和工具集成彻底解耦，让你可以按需自由组合各个部分。

> **模块路径：** `github.com/soasurs/adk`  
> **Go 版本：** 1.26+

---

## 特性

- **提供商无关的 LLM 接口** — 切换 OpenAI、Gemini 或 Anthropic 无需改动智能体代码
- **无状态 Agent + 有状态 Runner** — 职责清晰分离
- **自动 Tool-Call 循环** — `LlmAgent` 持续驱动模型直到获得终止响应
- **智能体组合** — 顺序链式执行或并行执行多个智能体
- **Agent as Tool** — 通过函数调用将任务委托给子智能体
- **可插拔会话后端** — 内存存储（零配置）或 SQLite（持久化）
- **消息历史压缩** — 软归档旧消息，不物理删除
- **MCP 工具集成** — 接入任意 [Model Context Protocol](https://modelcontextprotocol.io) 服务器
- **雪花 ID** — 分布式、时间有序的消息唯一标识
- **Go 迭代器流式输出** — 原生 `iter.Seq2`，支持逐条消息输出

---

## 安装

```bash
go get github.com/soasurs/adk
```

---

## 架构设计

```
┌──────────────────────────────────────────────────────┐
│                       Runner                         │
│  - 加载会话历史                                        │
│  - 追加并持久化每条消息                               │
│  - 每轮用户输入驱动 Agent 执行                        │
└───────────────┬──────────────────────────────────────┘
                │ []model.Message
                ▼
┌──────────────────────────────────────────────────────┐
│                  LlmAgent（无状态）                   │
│  - 拼接 system prompt                                │
│  - 在循环中调用 model.LLM.Generate                   │
│  - 执行 tool call，逐条 yield 消息                    │
└───────────────┬──────────────────────────────────────┘
                │
        ┌───────┴────────┐
        ▼                ▼
  model.LLM          tool.Tool
  (OpenAI, …)   (内置 / MCP / 自定义)
```

**Runner 是有状态的** — 它持有会话并负责持久化每条消息。  
**Agent 是无状态的** — 它只能看到传入的消息列表，并逐条 yield 结果；它对之前的对话轮次没有记忆。

---

## 包结构

| 包 | 职责 |
|---|---|
| `agent` | `Agent` 接口定义 |
| `agent/llmagent` | 带 tool-call 循环的 LLM 智能体实现 |
| `agent/sequential` | 顺序智能体组合（流水线） |
| `agent/parallel` | 并行智能体组合（扇出） |
| `agent/agentool` | 将智能体包装为工具以供委托 |
| `model` | 提供商无关的 LLM 接口与消息类型 |
| `model/openai` | OpenAI 适配器 |
| `model/gemini` | Google Gemini 适配器（Gemini API 和 Vertex AI） |
| `model/anthropic` | Anthropic Claude 适配器 |
| `session` | `Session` 和 `SessionService` 接口定义 |
| `session/memory` | 内存会话后端 |
| `session/database` | SQLite 会话后端 |
| `session/message` | 持久化消息类型 |
| `tool` | `Tool` 接口与 `Definition` 定义 |
| `tool/builtin` | 内置工具（echo 等） |
| `tool/mcp` | MCP 服务器桥接 |
| `runner` | 将 Agent 与 SessionService 串联 |
| `internal/snowflake` | 雪花节点工厂 |

---

## 快速上手

### 1. 创建 LLM

**OpenAI：**

```go
import "github.com/soasurs/adk/model/openai"

llm := openai.New(openai.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
    Model:  "gpt-4o-mini",
})
```

**Google Gemini：**

```go
import "github.com/soasurs/adk/model/gemini"

llm, err := gemini.New(ctx, os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")
// 或使用 Vertex AI：
// llm, err := gemini.NewVertexAI(ctx, "my-project", "us-central1", "gemini-2.0-flash")
```

**Anthropic Claude：**

```go
import "github.com/soasurs/adk/model/anthropic"

llm := anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), "claude-sonnet-4-5")
```

### 2. 构建 Agent

```go
import (
    "github.com/soasurs/adk/agent/llmagent"
    "github.com/soasurs/adk/model"
)

agent := llmagent.New(llmagent.Config{
    Name:         "my-agent",
    Description:  "一个有用的助手",
    Model:        llm,
    Instruction:  "你是一个有用的助手。",
})
```

### 3. 选择会话后端

**内存存储**（适合测试或单进程场景）：

```go
import "github.com/soasurs/adk/session/memory"

svc := memory.NewSessionService()
```

**SQLite 存储**（跨重启持久化）：

```go
import "github.com/soasurs/adk/session/database"

svc, err := database.NewSessionService("sessions.db")
```

### 4. 创建 Runner 并运行

```go
import "github.com/soasurs/adk/runner"

r, err := runner.New(agent, svc)
if err != nil { /* … */ }

ctx := context.Background()
sessionID := int64(1)

// 先创建会话
_, _ = svc.CreateSession(ctx, sessionID)

// 发送用户消息并迭代结果
for event, err := range r.Run(ctx, sessionID, "你好！") {
    if err != nil { /* … */ }
    if event.Partial {
        // 流式片段 —— 实时展示
        fmt.Print(event.Message.Content)
    } else {
        // 完整消息 —— 持久化或处理
        fmt.Println(event.Message.Role, event.Message.Content)
    }
}
```

---

## 核心概念

### `model.LLM`

所有 LLM 适配器必须实现的核心接口：

```go
type LLM interface {
    Name() string
    Generate(ctx context.Context, req *LLMRequest, cfg *GenerateConfig) (*LLMResponse, error)
}
```

`GenerateConfig` 以提供商无关的方式控制温度、top-p、推理力度和服务等级。

### `model.Message`

对话中的单条消息，关键字段说明：

| 字段 | 说明 |
|---|---|
| `Role` | `system` / `user` / `assistant` / `tool` |
| `Content` | 纯文本内容 |
| `Parts` | 多模态内容（文本 + 图片） |
| `ReasoningContent` | 推理模型的思维链输出（仅供参考，不回传给 LLM） |
| `ToolCalls` | 助手请求调用的工具列表 |
| `ToolCallID` | 将工具结果消息与对应 `ToolCall.ID` 关联 |
| `Usage` | 本次生成的 Token 消耗统计 |

### `agent.Agent`

```go
type Agent interface {
    Name() string
    Description() string
    Run(ctx context.Context, messages []model.Message) iter.Seq2[*model.Event, error]
}
```

`Run` 返回一个 Go 迭代器，逐条 yield 产生的事件——助手回复、工具结果及中间步骤——让调用方可以增量地流式处理输出。

事件带有 `Partial` 标记：当为 `true` 时，事件是用于实时展示的流式片段；当为 `false` 时，它是可持久化的完整消息。

### `tool.Tool`

```go
type Tool interface {
    Definition() Definition   // 名称、描述、JSON Schema
    Run(ctx context.Context, toolCallID string, arguments string) (string, error)
}
```

`LlmAgent` 在生成循环中自动分发 tool call。

### 会话与消息压缩

`Session` 保存一段对话的消息历史。两种存储后端均支持**软压缩**：旧消息被归档（标记 `CompactedAt` 时间戳）而非物理删除。

```go
// 使用自定义摘要函数归档消息
err = sess.CompactMessages(ctx, func(ctx context.Context, msgs []*message.Message) (*message.Message, error) {
    summary := summarise(msgs) // 你的摘要逻辑
    return &message.Message{Role: "system", Content: summary}, nil
})

// 活跃消息（压缩后剩余的）
active, _ := sess.ListMessages(ctx)

// 已归档消息
archived, _ := sess.ListCompactedMessages(ctx)
```

---

## MCP 工具

接入任意 MCP 服务器，将其所有工具暴露给 Agent：

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

## 智能体组合

### 顺序智能体

将多个智能体链式组合成流水线，每个智能体都能看到之前所有智能体的输出：

```go
import "github.com/soasurs/adk/agent/sequential"

pipeline := sequential.New(sequential.Config{
    Name:        "research-pipeline",
    Description: "研究、起草和审阅",
    Agents: []agent.Agent{
        researchAgent,  // 步骤 1：收集信息
        draftAgent,     // 步骤 2：撰写草稿
        reviewAgent,    // 步骤 3：审阅和润色
    },
})
```

### 并行智能体

并发执行多个智能体并合并它们的输出：

```go
import "github.com/soasurs/adk/agent/parallel"

ensemble := parallel.New(parallel.Config{
    Name:        "multi-model-ensemble",
    Description: "从多个模型获取答案",
    Agents: []agent.Agent{
        gpt4Agent,
        claudeAgent,
        geminiAgent,
    },
    // 可选：自定义合并函数
    MergeFunc: func(results []parallel.AgentOutput) model.Message {
        // 合并所有智能体的输出
    },
})
```

### Agent as Tool

通过 LLM 的函数调用机制将任务委托给子智能体：

```go
import "github.com/soasurs/adk/agent/agentool"

// 将智能体包装为工具
calculatorTool := agentool.New(calculatorAgent)

// 将其提供给另一个智能体
orchestrator := llmagent.New(llmagent.Config{
    Name:        "orchestrator",
    Description: "委托计算任务",
    Model:       llm,
    Tools:       []tool.Tool{calculatorTool},
    Instruction: "使用计算器工具处理数学问题。",
})
```

---

## 多模态输入

通过 `model.ContentPart` 同时传入图片和文本：

```go
msg := model.Message{
    Role: model.RoleUser,
    Parts: []model.ContentPart{
        {Type: model.ContentPartTypeText, Text: "这张图片里有什么？"},
        {
            Type:        model.ContentPartTypeImageURL,
            ImageURL:    "https://example.com/photo.jpg",
            ImageDetail: model.ImageDetailHigh,
        },
    },
}
```

---

## 依赖库

| 库 | 用途 |
|---|---|
| `github.com/openai/openai-go/v3` | OpenAI API 客户端 |
| `google.golang.org/genai` | Google Gemini API 客户端 |
| `github.com/anthropics/anthropic-sdk-go` | Anthropic Claude API 客户端 |
| `github.com/modelcontextprotocol/go-sdk` | MCP 客户端 |
| `github.com/google/jsonschema-go` | 工具定义的 JSON Schema |
| `github.com/jmoiron/sqlx` | SQL 查询增强 |
| `github.com/mattn/go-sqlite3` | SQLite 驱动 |
| `github.com/bwmarrin/snowflake` | 分布式 ID 生成 |
| `github.com/stretchr/testify` | 测试断言 |

---

## 许可证

[Apache 2.0](LICENSE)
