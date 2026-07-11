# Tracing

[简体中文](tracing_zh-CN.md) · [Documentation index](../README.md#documentation)

Tracing in ADK is observational: it must not change runtime behavior. `Runner`
propagates active span context through session storage, agents, LLM calls, and
tool calls using the small `trace.Tracer` abstraction.

## Local logging

Use the built-in `slog` tracer for local debugging:

```go
logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(trace.NewSlogTracer(logger)),
)
```

## OpenTelemetry

Applications own exporter and SDK configuration. Once a `TracerProvider` is
configured, connect ADK with the OTel adapter:

```go
import adkotel "github.com/soasurs/adk/trace/otel"

r, err := runner.New(
    agent,
    sessions,
    runner.WithTracer(adkotel.NewTracer()),
)
```

The adapter creates internal spans including:

- `adk.runner.run` and `adk.runner.lock`
- `adk.session.load` and `adk.session.persist_event`
- `adk.agent.run`
- `adk.llm.iteration` and `adk.llm.call`
- `adk.tool.call`

Each model-requested tool invocation gets its own `adk.tool.call` span. Tool
calls from one model response run concurrently and appear as sibling spans
under the same `adk.llm.iteration` span.

Streaming partial deltas do not create individual spans. The final LLM span
records their count in `adk.partial_responses`.

## Sensitive attributes

The OTel adapter omits `session_id`, `app_id`, and `user_id` by default. Enable
them only when the telemetry backend is permitted to receive those identifiers:

```go
tracer := adkotel.NewTracer(adkotel.WithSensitiveAttributes(true))
```
