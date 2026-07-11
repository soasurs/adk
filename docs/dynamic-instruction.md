# Dynamic System Instructions

`llmagent.InstructionProvider` builds an ephemeral system instruction before
each LLM invocation within an agent run. It complements the static
`Config.Instruction` field when the instruction must incorporate context that is
only known at runtime.

```go
import "github.com/soasurs/adk/agent/llmagent"

type InstructionProvider func(ctx context.Context, input InstructionInput) (string, error)
```

## What the provider receives

`InstructionInput` carries three pieces of context:

| Field          | Description                                                        |
|----------------|--------------------------------------------------------------------|
| `AgentName`    | Logical name of the agent being run.                               |
| `Iteration`    | 1-based LLM invocation number within the current `Run` call.       |
| `Conversation` | Isolated deep copy of the conversation **without** system messages. The provider may freely mutate this copy. |

`Iteration` resets to 1 for each `Run` call. It counts LLM turns within a
single tool-call loop, not across a multi-turn session.

`Conversation` includes user, assistant, and tool messages, but never contains
system messages. The SDK does not pass previous provider outputs back to the
provider. An implementation may still manage its own external state when needed.

## How the output is used

The dynamic output follows the static instruction:

```
[static Instruction] + [dynamic output]
```

The components are joined with `\n\n`; empty strings are omitted. Other system
context maintained by the application is independent of the provider contract.

The assembled system message affects only the current request. It is **never**
yielded as an event, never persisted to session history, and never cached by the
SDK. If the provider returns an error, the agent run terminates immediately—the
LLM is never called.

## When to use InstructionProvider

Use `InstructionProvider` when you need to **inject context into the system
message** based on runtime information. For anything beyond that, prefer the
alternatives below.

| Mechanism                 | Use when                                                 |
|---------------------------|----------------------------------------------------------|
| `Config.Instruction`      | The instruction is fixed at agent creation time.         |
| `InstructionProvider`     | The instruction needs runtime context, but only the system message should change. |
| `BeforeLLMCalls` hooks    | You need to modify the full request (tools, model, generation config) or skip the LLM call entirely. |

## Prompt-cache behavior

The dynamic instruction follows the static instruction near the start of every
request. When its content changes, prompt-cache reuse is reduced from the first
different token onward. Any stable prefix before that point may still be reused.

Keep dynamic output stable when possible. Avoid timestamps, random values, and
other data that changes without affecting model behavior. Return an empty string
when no dynamic guidance is needed.

### Run-stable context

One useful pattern is to read metadata from `context.Context` that remains stable
for one `Run` call but may differ between concurrent runs:

```go
type runtimeInstruction struct {
    Project     string
    Environment string
}

type runtimeInstructionKey struct{}

InstructionProvider: func(ctx context.Context, _ llmagent.InstructionInput) (string, error) {
    runtime, _ := ctx.Value(runtimeInstructionKey{}).(runtimeInstruction)
    if runtime == (runtimeInstruction{}) {
        return "", nil
    }
    return fmt.Sprintf("Project: %s. Environment: %s.",
        runtime.Project, runtime.Environment), nil
},
```

The caller is responsible for placing the metadata in the context before
calling `Run` and keeping it stable for as long as cache reuse matters. The
provider input does not expose session IDs, so any session-scoped metadata must
be supplied by the caller.

## Context management

`InstructionProvider` is not a context store. Stable facts should normally be
represented by user, assistant, or tool events so they remain part of the
canonical conversation.

Applications with long histories may optionally use
`session.ArchiveEventsBefore` to archive older events. Summary generation and
storage remain application-owned; a summary may be injected through an
`InstructionProvider` when appropriate, but it is not required.

## Anti-patterns

**Per-iteration unique content.** Avoid timestamps with second granularity,
random nonces, or a different instruction on every iteration. Every change
reduces cache reuse from its first different token.

```go
// Cache-killer: changes on every iteration.
InstructionProvider: func(ctx context.Context, input InstructionInput) (string, error) {
    return fmt.Sprintf("This is iteration %d of %d.", input.Iteration, maxIter), nil
},
```

If you genuinely need per-iteration guidance (e.g., "this is your last
attempt"), accept the cache cost consciously.

**Generating long instructions that rarely change.** `InstructionProvider` runs
before *every* LLM call. If the output is almost always the same, compute it
once and set `Config.Instruction` instead.

**Using it as a conversation logger or state store.** The provider output is
ephemeral. Do not rely on it to carry state between iterations—use hook
callbacks or external storage for that.

## Example

```go
package example

import (
    "context"
    "fmt"
    "strings"

    "github.com/soasurs/adk/agent"
    "github.com/soasurs/adk/agent/llmagent"
    "github.com/soasurs/adk/model"
)

type runtimeInstruction struct {
    Project     string
    Environment string
}

type runtimeInstructionKey struct{}

func withRuntimeInstruction(ctx context.Context, instruction runtimeInstruction) context.Context {
    return context.WithValue(ctx, runtimeInstructionKey{}, instruction)
}

func newSupportAgent(llm model.LLM) agent.Agent {
    return llmagent.New(llmagent.Config{
        Name:        "assistant",
        Model:       llm,
        Instruction: "You are a helpful technical support agent.",
        InstructionProvider: func(ctx context.Context, _ llmagent.InstructionInput) (string, error) {
            runtime, _ := ctx.Value(runtimeInstructionKey{}).(runtimeInstruction)
            var parts []string

            if runtime.Project != "" {
                parts = append(parts, fmt.Sprintf("Project: %s.", runtime.Project))
            }
            if runtime.Environment != "" {
                parts = append(parts, fmt.Sprintf("Environment: %s.", runtime.Environment))
            }

            return strings.Join(parts, " "), nil
        },
    })
}
```

Pass the run-specific metadata through the context used for `Run`:

```go
ctx = withRuntimeInstruction(ctx, runtimeInstruction{
    Project:     "payments",
    Environment: "staging",
})
```
