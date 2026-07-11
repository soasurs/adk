# 工具与 Agent 组合

[English](tools-and-agents.md) · [文档索引](../README_zh-CN.md#文档)

## Typed Tool

Tool 接收 raw JSON arguments，并返回 provider-neutral result：

```go
type Tool interface {
    Definition() Definition
    Run(ctx context.Context, call Call) (*Result, error)
}
```

应用工具推荐使用 `tool.NewFunc`，它会从 Go 类型推导输入和输出 JSON Schema：

```go
type weatherInput struct {
    City string `json:"city"`
}

type weatherOutput struct {
    City, Forecast string
}

weatherTool, err := tool.NewFunc(tool.Definition{
    Name: "weather", Description: "Get a weather forecast.",
}, func(ctx context.Context, input weatherInput) (weatherOutput, error) {
    if input.City == "" {
        return weatherOutput{}, tool.NewHandledError("city is required")
    }
    return weatherOutput{City: input.City, Forecast: "clear"}, nil
})
```

通过 `llmagent.Config.Tools` 传入工具。`Result.Content` 是文本 fallback；
`Result.StructuredContent` 为支持结构化输出的 provider 和存储层保留 JSON。

## 失败语义

三种结果含义不同：

- `*tool.Result, nil` 表示成功；nil result 和 nil error 的组合是非法实现。
- `nil, *tool.HandledError` 表示已经完成且可安全发送给模型的预期失败，通过
  `tool.NewHandledError(...)` 创建。
- 其他非 nil Go `error` 表示没有有效结果。`LlmAgent` 会丢弃同时返回的 result、取消
  并行 tool calls 并终止 Run，不会向模型暴露错误文本。

工具可能有副作用，因此 ADK 不会自动重试。安全的重试应放在工具内部或显式 wrapper 中。

## Agent 组合

顺序 Agent 会把每一步的完整 Event 传给下一步：

```go
pipeline, err := sequentialagent.New(sequentialagent.Config{
    Name: "research-pipeline", Agents: []agent.Agent{researcher, drafter, reviewer},
})
```

并行 Agent 会并发运行各分支，再合并完整输出：

```go
fanout, err := parallelagent.New(parallelagent.Config{
    Name: "multi-model", Agents: []agent.Agent{gptAgent, claudeAgent, geminiAgent},
    MergeFunc: func(results []parallelagent.AgentOutput) model.Event {
        return model.Event{Author: "multi-model", Content: model.Content{
            Role: model.RoleAssistant, Content: "merged answer",
        }}
    },
})
```

使用 `agent/agentool` 可以把 Agent 暴露成 Tool，供另一个 LLM Agent 委派调用。

## MCP 工具

```go
transport := sdkmcp.NewStdioTransport("my-mcp-server", []string{"--flag"}, nil)
toolSet := mcp.NewToolSet(transport)
if err := toolSet.Connect(ctx); err != nil { /* handle */ }
defer toolSet.Close()

tools, err := toolSet.Tools(ctx)
```

返回的 tools 可以像本地工具一样传给 `llmagent.Config.Tools`。
