# Tracing

[English](tracing.md) · [文档索引](../README_zh-CN.md#文档)

ADK 中的 tracing 只用于观测，不能改变运行语义。`Runner` 通过精简的
`trace.Tracer` 抽象，把 active span context 传递给 session 存储、Agent、LLM 和
Tool 调用。

## 本地日志

本地调试可以使用内置的 `slog` tracer：

```go
logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(trace.NewSlogTracer(logger)),
)
```

## OpenTelemetry

Exporter 和 SDK 配置由应用负责。配置好 `TracerProvider` 后，通过 OTel adapter 接入
ADK：

```go
import adkotel "github.com/soasurs/adk/trace/otel"

r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(adkotel.NewTracer()),
)
```

Adapter 会创建这些 internal spans：

- `adk.runner.run` 和 `adk.runner.lock`
- `adk.session.load` 和 `adk.session.persist_event`
- `adk.agent.run`
- `adk.llm.iteration` 和 `adk.llm.call`
- `adk.tool.call`

每个模型请求的 tool invocation 都有自己的 `adk.tool.call` span。同一次模型响应中的
tool calls 会并发执行，并作为同一个 `adk.llm.iteration` 下的 sibling spans。

流式 partial delta 不会逐条创建 span；最终 LLM span 会通过
`adk.partial_responses` 记录数量。

## 敏感属性

OTel adapter 默认不记录 `session_id`、`app_id` 和 `user_id`。只有 telemetry 后端
允许接收这些标识时才开启：

```go
tracer := adkotel.NewTracer(adkotel.WithSensitiveAttributes(true))
```
