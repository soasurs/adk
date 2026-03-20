# AGENTS.md

Guidance for autonomous coding agents working in `github.com/soasurs/adk`.

## Scope and Intent

- This repository is a Go library for building AI agents.
- Prefer small, focused changes that preserve existing package boundaries.
- Keep public APIs stable unless the task explicitly requests API changes.
- Follow existing conventions in adjacent files over personal preferences.

## Repository Facts

- Module path: `github.com/soasurs/adk`
- Go version: `1.26+` (see `go.mod`); uses `iter.Seq2` (1.23+), `t.Context()` (1.21+), `slices` (1.21+)
- Main packages: `agent`, `model`, `runner`, `session`, `tool`, `internal/snowflake`
- Agent sub-packages: `agent/llmagent`, `agent/parallelagent`, `agent/sequentialagent`, `agent/agentool`
- Model sub-packages: `model/openai`, `model/gemini`, `model/anthropic`, `model/retry`
- Session sub-packages: `session/memory`, `session/database`, `session/compaction`
- Tool sub-packages: `tool/builtin`, `tool/mcp`
- Example app: `examples/chat/main.go`
- Tests: unit tests (no network) and integration tests (env-gated, skip when keys absent), co-located in the same `_test.go` file

## Build, Lint, and Test Commands

Run all commands from the repository root.

### Core commands

```bash
go build ./...
go vet ./...
go test ./...
```

### Single-package tests

```bash
go test ./runner
go test ./agent/llmagent
go test ./session/database
```

### Run a single test (most important)

Use `-run` with an anchored regex:

```bash
go test ./runner -run '^TestRunner_Run_Basic$'
go test ./model/openai -run '^TestConvertMessage_User$'
```

### Run tests by pattern across a package

```bash
go test ./runner -run 'Runner_Run_'
```

### Verbose test output

```bash
go test -v ./...
```

### Optional race check (slower; run when touching concurrency)

```bash
go test -race ./...
```

### Integration-test environment variables

Some tests auto-skip unless the required env var is set.

| Provider | Required | Optional (fall back to default) |
|---|---|---|
| OpenAI | `OPENAI_API_KEY` | `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_MODEL` |
| Gemini / Vertex AI | `GEMINI_API_KEY` | `GEMINI_MODEL`, `GEMINI_THINKING_MODEL`, `VERTEX_AI_PROJECT`, `VERTEX_AI_LOCATION`, `VERTEX_AI_MODEL` |
| Anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_MODEL`, `ANTHROPIC_THINKING_MODEL` |
| MCP (Exa) | — | `EXA_API_KEY` |

## Architecture Overview

Key design decisions every agent must understand:

- **`iter.Seq2[V, error]`** is the universal streaming primitive. All LLM responses, agent events, and retry wrappers return `iter.Seq2`. Consume with Go range-over-func; on error the sequence yields `(nil, err)` and returns.
- **Stateless agents** — agents hold no conversation state. All history is supplied by `Runner` via `SessionService` on every call.
- **Partial vs complete events** — partial events (streaming chunks) are forwarded immediately to the caller for real-time display but are never persisted. Only complete events are saved to the session. Do not break this invariant.
- **Parallel tool execution** — `LlmAgent` dispatches all tool calls from a single LLM response concurrently with `sync.WaitGroup`; results are written to pre-allocated index slots to avoid mutex contention.
- **Provider neutrality** — `model.LLM`, `tool.Tool`, and `session.Session` are the three abstraction points. Provider-specific code lives in sub-packages (`model/openai`, etc.).

## Context Management and Manual Compaction

The SDK does not perform automatic compaction. Instead, it provides `Session.CompactMessages()` for users to manage context growth manually.

### About Compaction

Long conversations accumulate messages, which increases the input token count for each LLM call. When the session grows too large, you must decide:

1. **When to compact** — based on token count, message count, time, or other heuristics
2. **What to archive** — which older messages to remove from active history
3. **How to summarize** — whether and how to create a summary of archived messages

### How to Implement Compaction

Compaction involves four steps:

1. **Monitor growth**: Track session size (e.g., via `PromptTokens` on LLM responses or message count)
2. **Decide what to keep**: Choose which messages remain in active history (e.g., the last N rounds of conversation)
3. **Summarize archived**: Generate a summary of the messages being archived (optional, but recommended for context preservation)
4. **Call CompactMessages**: Persist the archival using `session.CompactMessages(ctx, splitMessageID, summaryMsg)`

### Example: Token-Based Compaction Strategy

```go
import (
    "context"
    "fmt"
    "strings"
    "time"

    snowflaker "github.com/soasurs/adk/internal/snowflake"
    "github.com/soasurs/adk/model"
    "github.com/soasurs/adk/session"
    "github.com/soasurs/adk/session/message"
)

// TokenBasedCompactor monitors session growth and archives old messages
// when they exceed a token threshold.
type TokenBasedCompactor struct {
    maxTokens int64
    llm       model.LLM // for generating summaries
    modelName string
}

// MaybeCompact checks if compaction is needed and compacts if threshold is exceeded.
func (c *TokenBasedCompactor) MaybeCompact(
    ctx context.Context,
    sess session.Session,
    msgs []*message.Message,
) error {
    // 1. Check if we exceed the threshold
    var latestPromptTokens int64
    for i := len(msgs) - 1; i >= 0; i-- {
        if msgs[i].PromptTokens > 0 {
            latestPromptTokens = msgs[i].PromptTokens
            break
        }
    }

    if latestPromptTokens == 0 || latestPromptTokens <= c.maxTokens {
        return nil // No compaction needed
    }

    // 2. Decide what to keep: keep only the last 2 user messages
    // (adjust this strategy based on your needs)
    var splitIdx int = 0
    userCount := 0
    for i := len(msgs) - 1; i >= 0; i-- {
        if msgs[i].Role == model.RoleUser {
            userCount++
            if userCount >= 2 {
                splitIdx = i
                break
            }
        }
    }

    if splitIdx == 0 {
        return nil // Nothing to archive
    }

    toArchive := msgs[:splitIdx]
    toKeep := msgs[splitIdx:]

    // 3. Generate summary
    summaryContent, err := c.summarize(ctx, toArchive)
    if err != nil {
        return fmt.Errorf("summarize: %w", err)
    }

    // 4. Create summary message and call CompactMessages
    now := time.Now().UnixMilli()
    sf, err := snowflaker.New()
    if err != nil {
        return fmt.Errorf("snowflake: %w", err)
    }

    summaryMsg := &message.Message{
        MessageID:     sf.Generate().Int64(),
        Role:          model.RoleSystem,
        Content:       summaryContent,
        CreatedAt:     now,
        UpdatedAt:     now,
    }

    splitMessageID := int64(0)
    if len(toKeep) > 0 {
        splitMessageID = toKeep[0].MessageID
    }

    return sess.CompactMessages(ctx, splitMessageID, summaryMsg)
}

// summarize calls the LLM to create a brief summary of archived messages.
func (c *TokenBasedCompactor) summarize(
    ctx context.Context,
    msgs []*message.Message,
) (string, error) {
    var sb strings.Builder
    for _, m := range msgs {
        sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
    }

    req := &model.LLMRequest{
        Model: c.modelName,
        Messages: []model.Message{
            {
                Role:    model.RoleUser,
                Content: "Summarize the following conversation concisely:\n\n" + sb.String(),
            },
        },
    }

    for resp := range c.llm.GenerateContent(ctx, req, nil, false) {
        if !resp.Partial {
            return resp.Message.Content, nil
        }
    }

    return "", fmt.Errorf("no summary response")
}
```

### CompactMessages API

```go
// Session.CompactMessages marks messages as archived and inserts a summary.
// If splitMessageID > 0, all messages with MessageID < splitMessageID are marked CompactedAt.
// If splitMessageID == 0, all messages are marked CompactedAt.
// summaryMsg (usually with Role="system") is inserted into the session.
// ListMessages will exclude archived messages and include the summary.
CompactMessages(ctx context.Context, splitMessageID int64, summaryMsg *Message) error
```

### Other Compaction Strategies

Choose a strategy based on your workload:

- **Rounds-based**: Keep the last N conversation rounds, archive older ones
- **Time-based**: Archive messages older than T hours
- **Hybrid**: Combine multiple signals (tokens + rounds + time)

No matter which strategy you choose, the final step is always `session.CompactMessages(ctx, splitMessageID, summaryMsg)`.

## Coding Style Guidelines

### Formatting and file layout

- Always format with `gofmt` (or editor-integrated gofmt). No exceptions.
- Keep imports in three groups separated by blank lines (standard library, third-party, local module); this is what `gofmt` produces.
- Preserve existing file organization; keep related types and functions together.
- Add or retain doc comments for all exported types and functions.

### Imports

- Avoid unnecessary aliases.
- Use aliases only to resolve collisions or improve clarity (e.g., `sdkmcp`, `goopenai`, `goanthropic`).
- No dot imports anywhere in the codebase.

### Types and API design

- Define thin interfaces at the point of use in their own package-level file (`agent/agent.go`, `model/model.go`, `tool/tool.go`).
- Use `Config` structs for agent/service construction: `llmagent.Config`, `compaction.Config`, `retry.Config`.
- Constructor functions return interfaces for agent and session types (`New(...) agent.Agent`), but may return concrete types for LLM adapters where the concrete type adds methods.
- Functional options (`Option func(*T)`) are the pattern for database session configuration.
- Use `*bool` for tri-state fields only when nil (provider decides) is semantically distinct from true/false (e.g., `EnableThinking *bool`).
- Decorator pattern for tool wrappers: `tool.WithTimeout` wraps any `Tool` as a private struct implementing the same interface.
- Keep provider-neutral abstractions in `model`; keep provider-specific code in `model/openai`, `model/gemini`, `model/anthropic`.

### Naming conventions

- Exported identifiers: `PascalCase` with clear nouns — `LlmAgent`, `GenerateConfig`, `ToolCall`, `FinishReasonStop`.
- Unexported identifiers: `camelCase` — `memorySession`, `agentTool`, `toolCallAcc`.
- Package names: short, lowercase, semantic — `runner`, `llmagent`, `agentool`, `compaction`.
- Error messages: lowercase, no trailing punctuation, action-oriented with a `<package>: <action>: ` prefix — `"openai: convert tools: %w"`, `"mcp connect: %w"`.
- Variable names: descriptive domain language — `sessionID`, `toolCallID`, `finishReason`, `contentBuf`.
- Constants: `PascalCase` for exported (`RoleSystem`), `camelCase` for unexported (`defaultMaxTokens`).

### Error handling

- Return errors; do not panic in library code except for true construction-time invariants.
- Wrap external errors with `%w` to preserve the chain for `errors.Is`/`errors.As`.
- Use `errors.Is` for sentinel checks (`sql.ErrNoRows`, `context.Canceled`).
- A not-found condition that is part of normal flow returns `nil, nil` (e.g., `GetSession`).
- In `iter.Seq2` generators, always use `yield(nil, err); return` — pass the zero value alongside the error.

### Concurrency and context

- Accept `context.Context` as the first parameter for all operations that may block or call IO.
- For parallel fan-out, use `sync.WaitGroup` with explicit loop-variable capture in goroutine arguments:
  ```go
  for i, tc := range items {
      wg.Add(1)
      go func(i int, tc T) {
          defer wg.Done()
          results[i] = process(tc)
      }(i, tc)
  }
  wg.Wait()
  ```
- Pre-allocate result slices by index to avoid mutex contention.
- In fan-out flows, derive a child context with `context.WithCancel` and call `cancel()` when any sub-task errors to signal siblings to stop.
- Respect context in retry backoff: use `select { case <-time.After(wait): case <-ctx.Done(): return }`.
- If concurrency was touched, run `go test -race ./...` before declaring done.

### Comments and documentation

- All exported symbols must have doc comments.
- Package-level doc comments are required on every package.
- Keep comments concise and behavior-focused. Explain "why" and protocol contracts, not just "what".
- Use section banners in long files: `// ───────────────────────────────────────────`.
- Use numbered step comments in complex methods: `// ── 1. Build request ──`.

## Test Style Guidelines

- Use `github.com/stretchr/testify`: `require` for preconditions/fatal assertions, `assert` for subsequent non-fatal checks.
- Name tests `Test<TypeOrFunction>_<Scenario>` (e.g., `TestRunner_Run_Basic`, `TestConvertMessage_Assistant_ToolCalls`).
- White-box tests (most common) use the same package as the code under test (`package llmagent`). Black-box tests use `package <name>_test` (e.g., `agentool_test.go`).
- Use table-driven tests for multiple input/output permutations; run each case with `t.Run`.
- All test helpers must call `t.Helper()` as the first line.
- Use `t.Context()` (Go 1.21+) in tests instead of `context.Background()`.
- Define mocks locally in the test file (not shared across packages); mock only the minimal interface needed.
- For concurrency tests, use channels (`ready`, `gate`) and `atomic` counters rather than timing-dependent sleeps.
- Integration tests live in the same `_test.go` file as unit tests, separated by comment banners:
  ```go
  // Unit tests (no network required)
  // ───────────────────────────────────────────

  // Integration tests (require OPENAI_API_KEY)
  // ───────────────────────────────────────────
  ```
- Env-gating pattern — required vars skip; optional vars fall back to defaults:
  ```go
  func newLLMFromEnv(t *testing.T) model.LLM {
      t.Helper()
      apiKey := os.Getenv("OPENAI_API_KEY")
      if apiKey == "" {
          t.Skip("OPENAI_API_KEY not set")
      }
      modelName := os.Getenv("OPENAI_MODEL")
      if modelName == "" {
          modelName = "gpt-4o-mini" // sensible default
      }
      return openai.New(apiKey, os.Getenv("OPENAI_BASE_URL"), modelName)
  }
  ```

## Change Checklist for Agents

Before editing:

- Read neighboring files in the same package to mirror their exact patterns.
- Check whether the behavior is part of a public interface or cross-package contract.
- Confirm whether the change touches streaming (partial/complete event invariant) or concurrency (race check needed).

After editing:

- `go build ./...` — must pass.
- `go vet ./...` — must pass.
- `go test ./...` — must pass.
- `go test -race ./...` — run if concurrency was touched.
- New exported identifiers must have doc comments.

## Cursor/Copilot Rules Audit

No additional instruction files were found in this repository:

- No `.cursorrules`
- No `.cursor/rules/`
- No `.github/copilot-instructions.md`

If any of these files are added later, treat them as authoritative supplements to this AGENTS guide.
