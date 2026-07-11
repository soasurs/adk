# Core Concepts

[简体中文](core-concepts_zh-CN.md) · [Documentation index](../README.md#documentation)

ADK separates conversation state from agent behavior. `Runner` owns the turn
lifecycle and persistence, while every `Agent` receives the history it needs for
one invocation.

## Content and events

`model.Content` is the provider-facing payload. It can contain a role, text,
multimodal parts, reasoning text, tool calls, and tool-result linkage.

```go
content := model.Content{
    Role:    model.RoleUser,
    Content: "What is in this image?",
    Parts: []model.ContentPart{
        {Type: model.ContentPartTypeImageURL, ImageURL: "https://example.com/photo.jpg"},
    },
}
```

`model.Event` wraps content with runtime metadata such as its author, session,
turn, finish reason, usage, and timestamps. Events are both runtime output and
the unit stored in session history.

Complete events form the durable event ledger. Partial events are transient
streaming fragments: `Runner` forwards them to the caller but never persists
them. `model.TokenUsage` records provider-reported totals and, when available,
details such as cached, reasoning, audio, and prediction tokens.

## Stateless agents

```go
type Agent interface {
    Name() string
    Description() string
    Run(ctx context.Context, events []model.Event) iter.Seq2[*model.Event, error]
}
```

Agents do not own conversation state. For each `Runner.Run`, the runner loads
the active session events, appends the user event, and passes the resulting
history to the agent.

An LLM-backed agent projects that event history into `model.LLMRequest.Contents`.
Request-only behavior such as a dynamic system instruction is added after this
projection and is not written back to the event ledger. See
[Dynamic System Instructions](dynamic-instruction.md).

## Turns and rollback

Every `Runner.Run` call creates a `TurnID` shared by the user event and all agent
events from that call. It is a correlation identifier, not an ordering key or
an automatic resume checkpoint.

`Runner` persists complete events while the agent runs. If the agent fails or
the caller stops consuming the sequence early, the runner removes events saved
for that incomplete turn. This prevents a partial tool protocol from being
replayed as valid history.

Before starting another turn, the runner also verifies that every durable
assistant tool call has a matching durable tool result. A missing result returns
`runner.ErrToolExecutionUnknown`, leaves the session unchanged, and does not
invoke the agent. ADK cannot safely retry automatically because the external
side effect may already have happened.

## Streaming

All streaming APIs use `iter.Seq2[V, error]`:

```go
for event, err := range r.Run(ctx, sessionID, input) {
    if err != nil {
        return err
    }
    if event.Partial {
        fmt.Print(event.Content.Content)
        continue
    }
    // A complete event has already entered the durable turn ledger.
}
```

Consume the sequence until it finishes unless you intentionally want to cancel
the run. Stopping early triggers incomplete-turn cleanup.
