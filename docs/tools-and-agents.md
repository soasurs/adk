# Tools and Agent Composition

[简体中文](tools-and-agents_zh-CN.md) · [Documentation index](../README.md#documentation)

## Typed tools

Tools receive raw JSON arguments and return provider-neutral results:

```go
type Tool interface {
    Definition() Definition
    Run(ctx context.Context, call Call) (Result, error)
}
```

For application tools, `tool.NewFunc` infers input and output JSON Schemas from
Go types:

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
        return weatherOutput{}, tool.NewFuncError("city is required")
    }
    return weatherOutput{City: input.City, Forecast: "clear"}, nil
})
```

Pass tools through `llmagent.Config.Tools`. `Result.Content` is the text fallback;
`Result.StructuredContent` preserves JSON for capable providers and storage.

## Failure semantics

The two failure channels have different meanings:

- `tool.Result{IsError: true}, nil` is an expected, handled failure safe for the
  model. With `tool.NewFunc`, return `tool.NewFuncError(...)` for this case.
- A non-nil Go `error` means no valid result exists. `LlmAgent` discards any
  accompanying result, cancels sibling calls, and terminates the run without
  exposing the error text to the model.

ADK does not automatically retry tool calls because they may have side effects.
Place safe retries inside the tool or an explicit wrapper.

## Agent composition

Sequential agents pass complete events from one step to the next:

```go
pipeline, err := sequentialagent.New(sequentialagent.Config{
    Name: "research-pipeline", Agents: []agent.Agent{researcher, drafter, reviewer},
})
```

Parallel agents run branches concurrently and merge their complete outputs:

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

Use `agent/agentool` to expose an agent as a tool for delegation from another
LLM-backed agent.

## MCP tools

```go
transport := sdkmcp.NewStdioTransport("my-mcp-server", []string{"--flag"}, nil)
toolSet := mcp.NewToolSet(transport)
if err := toolSet.Connect(ctx); err != nil { /* handle */ }
defer toolSet.Close()

tools, err := toolSet.Tools(ctx)
```

Pass the returned tools to `llmagent.Config.Tools` like any local tool.
