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

## Durable turns

Every `Runner.Run` call creates a `TurnID` shared by the user event and all agent
events from that call. It is a correlation identifier, not an ordering key or
an automatic resume checkpoint.

Sessions implementing `session.TurnStore` record each run as `running` and
finalize it as `completed`, `failed`, or `interrupted`. Complete events remain
durable when a run fails or the caller stops consuming it. Before a later LLM
call, Runner projects failed and interrupted turns into safe context: closed
event sequences are retained, unsafe suffixes beginning with an unmatched tool
call are omitted, and an ephemeral status notice is added. The notice is never
written back to the event ledger.

Projection is a public, reusable boundary. `runner.NewDefaultProjector` applies
the same durable-Turn rules used by Runner, and `runner.WithProjector` installs
a custom implementation. Applications can use the default projector when
building compaction summaries. Runner always applies
`runner.ValidateToolProtocol` after projection, including custom projectors.

Terminal Turns may contain a structured `TurnFailure` with a stable code,
display-safe message, and failure stage. Runner never persists an arbitrary
`error.Error()` value. The default classifier trusts typed errors implementing
`session.TurnFailureProvider`; applications can install a safe classifier with
`runner.WithFailureClassifier`. Display-safe failure messages are not injected
into model context by the default projector.

Turn and event writes are individually atomic but do not share a transaction
covering the full run. After acquiring a session run lock, Runner recovers any
old `running` turns as `interrupted/abandoned`. Session implementations without
`TurnStore` retain the legacy incomplete-turn rollback behavior.

Before invoking an agent, Runner verifies that the projected context has no
unmatched assistant tool calls. A missing result outside a recoverable durable
Turn projection returns `runner.ErrToolExecutionUnknown`. ADK cannot safely
retry an unknown external side effect automatically.

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
the run. With durable Turns, stopping early preserves complete events and marks
the Turn `interrupted/consumer_stopped`. Partial fragments remain transport-only
and may disappear after disconnection. Legacy Session implementations retain
incomplete-turn rollback.
