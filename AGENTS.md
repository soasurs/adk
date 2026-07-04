# AGENTS.md

Guidance for autonomous coding agents working in `github.com/soasurs/adk`.

## Scope and Intent

- This repository is a Go library for building AI agents.
- Prefer small, focused changes that preserve existing package boundaries.
- Keep public APIs stable unless the task explicitly requests API changes.
- Follow existing conventions in adjacent files over personal preferences.

## Repository Layout

- Module path: `github.com/soasurs/adk`
- Go version: `1.26+`; uses `iter.Seq2` (1.23+), `t.Context()` (1.21+), `slices` (1.21+)

| Package | Role |
|---|---|
| `agent` | `Agent` interface |
| `agent/llmagent` | LLM-backed agent with tool-call loop and lifecycle hooks |
| `agent/parallelagent` | Fan-out to multiple agents concurrently, merge results |
| `agent/sequentialagent` | Run agents in order, passing output forward |
| `agent/agentool` | Wrap an `Agent` as a `tool.Tool` for nesting |
| `model` | Provider-neutral types: `LLM`, `Content`, `Event`, `LLMRequest/Response` |
| `model/openai` | OpenAI / OpenAI-compatible adapter |
| `model/deepseek` | DeepSeek adapter |
| `model/gemini` | Gemini / Vertex AI adapter |
| `model/anthropic` | Anthropic adapter |
| `model/retry` | `retry.Seq2` — exponential-backoff wrapper for `iter.Seq2` |
| `runner` | Ties `Agent` + `SessionService` together for multi-turn conversations |
| `session` | `Session` and `SessionService` interfaces |
| `session/memory` | In-memory session (tests / ephemeral use) |
| `session/database` | SQL database-backed session with schema migration; SQLite and PostgreSQL are tested |
| `session/compaction` | `compaction.Config` reference type for manual context management |
| `session/event` | `event.Event` — persisted form of `model.Event` |
| `tool` | `Tool` interface, `Definition`, structured `Call`/`Result`, and typed function helpers |
| `tool/builtin` | Ready-made tools (e.g. code execution) |
| `tool/mcp` | MCP client tool adapter |
| `internal/snowflake` | Snowflake ID generator |

Example app: `examples/chat/main.go`

## Build, Lint, and Test

Run all commands from the repository root.

```bash
go build ./...          # must pass before every commit
go vet ./...            # must pass before every commit
go test ./...           # full unit-test suite
go test -race ./...     # run when concurrency was touched
```

**Single package:**
```bash
go test ./runner
go test ./agent/llmagent
go test ./session/database
```

**Single test (use anchored regex):**
```bash
go test ./runner -run '^TestRunner_Run_Basic$'
go test ./model/openai -run '^TestConvertMessage_User$'
```

**Pattern across a package:**
```bash
go test ./runner -run 'Runner_Run_'
go test -v ./...
```

### Integration-test environment variables

Tests auto-skip when required vars are absent; optional vars fall back to defaults.

| Provider / backend | Required | Optional |
|---|---|---|
| OpenAI | `OPENAI_API_KEY` | `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_MODEL` |
| DeepSeek | `DEEPSEEK_API_KEY` | `DEEPSEEK_BASE_URL`, `DEEPSEEK_MODEL` |
| Gemini | `GEMINI_API_KEY` | `GEMINI_BASE_URL`, `GEMINI_MODEL`, `GEMINI_THINKING_MODEL` |
| Vertex AI | `VERTEX_AI_PROJECT`, `VERTEX_AI_LOCATION` | `VERTEX_AI_BASE_URL`, `VERTEX_AI_MODEL` |
| Anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_THINKING_MODEL` |
| PostgreSQL session/database | `ADK_TEST_POSTGRES_DSN` | — |
| MCP (Exa) | — | `EXA_API_KEY` |

## Architecture — Critical Invariants

- **`iter.Seq2[V, error]`** is the universal streaming primitive. Consume with `for v, err := range seq { if err != nil { ... } }`. On error, yield `(nil, err)` and return.
- **Partial vs complete events** — `Event.Partial=true` fragments are forwarded to the caller for real-time display but **never persisted**. Only `Event.Partial=false` events are saved. Do not break this invariant.
- **Stateless agents** — agents hold no conversation state; all history is supplied by `Runner` via `SessionService` on every call.
- **Parallel tool execution** — `LlmAgent` dispatches tool calls from a single response concurrently via `sync.WaitGroup`; results write to pre-allocated index slots (no mutex contention).
- **Provider neutrality** — `model.LLM`, `tool.Tool`, and `session.Session` are the three abstraction points. Provider-specific code lives exclusively in sub-packages.
- **Structured tool boundary** — tool-call arguments are raw JSON (`json.RawMessage`) in `model.ToolCall` and `tool.Call`; tool outputs use `tool.Result` / `model.ToolResult` with text fallback, structured JSON, and `IsError`. Do not collapse arguments or results back into plain strings except at provider-specific API boundaries.
- **Manual compaction** — the SDK performs no automatic compaction. Call `session.CompactEvents(ctx, splitEventID, summaryEvent)` to archive old events and insert a summary. `splitEventID=0` archives all active events.

## Coding Style

### Formatting and imports

- Always run `gofmt`. No exceptions.
- Three import groups separated by blank lines: standard library, third-party, local module.
- No dot imports. Use aliases only to resolve collisions (`sdkmcp`, `goopenai`, `goanthropic`).

### Types and API design

- Thin interfaces defined at point of use in package-level files (`agent/agent.go`, `model/model.go`, `tool/tool.go`).
- `Config` structs for construction: `llmagent.Config`, `parallelagent.Config`, `retry.Config`.
- Constructors return interfaces for agent and session types (`New(...) agent.Agent`). `Runner.New` returns `*Runner` (concrete) because `Runner` adds no interface — this is intentional.
- Functional options (`Option func(*T)`) for `session/database` configuration.
- `*bool` only for tri-state fields where nil means "provider decides" (`EnableThinking *bool`).
- Decorator pattern for tool wrappers: `tool.WithTimeout` wraps any `Tool` as a private struct.
- Prefer `tool.NewFunc[In, Out]` for application tools so input/output schemas are inferred from Go types. Custom tools should implement `Run(ctx, tool.Call) (tool.Result, error)`.
- Use `tool.Result{IsError: true}` for model-visible tool failures. Reserve Go `error` returns for SDK, transport, parsing, or framework failures that the runtime should turn into execution failures.

### Naming

- Exported: `PascalCase` — `LlmAgent`, `ToolCall`, `FinishReasonStop`, `RoleSystem`.
- Unexported: `camelCase` — `memorySession`, `toolCallAcc`, `defaultMaxTokens`.
- Package names: short, lowercase — `runner`, `llmagent`, `agentool`, `compaction`.
- Error messages: lowercase, no trailing punctuation, `<package>: <action>: ` prefix — `"openai: convert tools: %w"`.
- Constants: `PascalCase` exported (`RoleSystem`), `camelCase` unexported (`defaultMaxTokens`).

### Error handling

- Return errors; do not panic in library code except for construction-time invariants.
- Wrap external errors with `%w` to preserve the chain for `errors.Is`/`errors.As`.
- Structured error types pair with a sentinel for `errors.Is` matching via `Unwrap()`:
  ```go
  var ErrSessionNotFound = errors.New("runner: session not found")
  type SessionNotFoundError struct{ SessionID int64 }
  func (e *SessionNotFoundError) Unwrap() error { return ErrSessionNotFound }
  ```
- Not-found conditions that are part of normal flow return `nil, nil` (e.g. `GetSession`).
- In `iter.Seq2` generators: `yield(nil, err); return` — always pass the zero value.

### Concurrency and context

- `context.Context` is the first parameter for all blocking or IO operations.
- Fan-out with `sync.WaitGroup`; capture loop variables explicitly:
  ```go
  for i, tc := range items {
      wg.Add(1)
      go func(i int, tc T) { defer wg.Done(); results[i] = process(tc) }(i, tc)
  }
  ```
- Derive a child context with `context.WithCancel`; call `cancel()` on first error to unblock siblings.
- Retry backoff must be context-aware: `select { case <-time.After(wait): case <-ctx.Done(): return }`.

### Comments and documentation

- All exported symbols must have doc comments.
- Package-level doc comment required on every package.
- Explain "why" and protocol contracts, not just "what".
- Section banners in long files: `// ───────────────────────────────────────────`
- Numbered steps in complex methods: `// ── 1. Build request ──`

## Test Style

- Use `github.com/stretchr/testify`: `require` for fatal assertions, `assert` for non-fatal.
- Test names: `Test<TypeOrFunction>_<Scenario>` — `TestRunner_Run_Basic`, `TestConvertMessage_Assistant_ToolCalls`.
- White-box tests (same package as code under test) are the default. Black-box tests use `package <name>_test`.
- Table-driven tests for multiple cases; each case in `t.Run`.
- Test helpers call `t.Helper()` as the first line.
- Use `t.Context()` (not `context.Background()`) in tests.
- Define mocks locally in the test file; mock only the minimal interface needed.
- Concurrency tests use channels and `atomic` counters, not timing-dependent sleeps.
- Unit and integration tests co-located in the same `_test.go` file, separated by banners:
  ```
  // Unit tests (no network required)
  // ───────────────────────────────────────────

  // Integration tests (require OPENAI_API_KEY)
  // ───────────────────────────────────────────
  ```
- Env-gating: skip on missing required vars; fall back to a sensible default for optional vars.

## Change Checklist

**Before editing:** read neighboring files in the same package to mirror exact patterns; check whether the change touches a public interface, the streaming partial/complete invariant, or concurrency.

**After editing:**
- `go build ./...` — must pass
- `go vet ./...` — must pass
- `go test ./...` — must pass
- `go test -race ./...` — run if concurrency was touched
- All new exported identifiers must have doc comments

## Cursor / Copilot Rules

No additional instruction files exist in this repository (no `.cursorrules`, `.cursor/rules/`, or `.github/copilot-instructions.md`). If added later, treat them as authoritative supplements to this file.
