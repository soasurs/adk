# AGENTS.md

Guidance for autonomous coding agents working in `github.com/soasurs/adk`.

## Scope and Intent

- This repository is a Go library for building AI agents.
- Prefer small, focused changes that preserve existing package boundaries.
- Keep public APIs stable unless the task explicitly requests API changes.
- Follow existing conventions in adjacent files over personal preferences.

## Repository Facts

- Module path: `github.com/soasurs/adk`
- Go version: `1.26+` (see `go.mod`)
- Main packages: `agent`, `model`, `runner`, `session`, `tool`, `internal/snowflake`
- Example app: `examples/chat/main.go`
- Tests: primarily unit tests, plus integration tests that are env-gated and skip when keys are absent.

## Build, Lint, and Test Commands

Run commands from repository root: `/Volumes/Code/go/soasurs/adk`.

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
```

Another example:

```bash
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

### Optional race check (slower, useful for concurrency changes)

```bash
go test -race ./...
```

### Integration-test environment variables

Some tests auto-skip unless required env vars are set.

- OpenAI-related tests: `OPENAI_API_KEY` (optional: `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_MODEL`)
- Gemini/Vertex tests: `GEMINI_API_KEY`, `GEMINI_MODEL`, `GEMINI_THINKING_MODEL`, `VERTEX_AI_PROJECT`, `VERTEX_AI_LOCATION`, `VERTEX_AI_MODEL`
- Anthropic tests: `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL`, `ANTHROPIC_THINKING_MODEL`
- MCP tool integration tests may use: `EXA_API_KEY`

## Coding Style Guidelines

### Formatting and file layout

- Always format Go code with `gofmt` (or editor-integrated gofmt).
- Keep imports grouped by `gofmt`: standard library, third-party, then local module imports.
- Preserve existing file organization; keep related types/functions together.
- Add or retain doc comments for exported types/functions.

### Imports

- Avoid unnecessary aliases.
- Use aliases only to resolve collisions or improve clarity (for example `sdkmcp`, `goopenai`).
- Prefer explicit imports over dot imports.

### Types and API design

- Prefer concrete structs with constructor functions like `New(...)`.
- Return interfaces where existing packages already do so (for example `agent.Agent`, `session.SessionService`, `tool.Tool`).
- Keep zero-value behavior sensible; use optional pointer fields only when tri-state behavior is needed (for example `*bool`).
- Keep provider-neutral abstractions in `model` and provider-specific code in `model/openai`, `model/gemini`, `model/anthropic`.

### Naming conventions

- Exported identifiers: `PascalCase` with clear nouns (`GenerateConfig`, `ToolCall`).
- Unexported identifiers: `camelCase`.
- Package names are short, lowercase, and semantic (`runner`, `parallel`, `agentool`).
- Error messages are lowercase and without trailing punctuation.
- Use descriptive variable names for domain concepts (`sessionID`, `toolCalls`, `finishReason`).

### Error handling

- Return errors instead of panicking, except for true construction-time invariants already used in this codebase (for example invalid static setup in some `New` functions).
- Wrap external/dependency errors with context using `%w`.
- Keep wrappers action-oriented and scoped (`"openai: convert tools: %w"`, `"mcp connect: %w"`).
- When a not-found condition is part of normal flow, follow existing conventions (for example `GetSession` may return `nil, nil`).

### Concurrency and context

- Accept `context.Context` as the first parameter for operations that may block or call IO.
- In fan-out flows, use cancellation to stop sibling work early on error.
- For goroutines in loops, always capture loop variables explicitly in the closure args.
- Preserve current streaming contract: partial events are forwarded; only complete events are persisted.

### Data and serialization conventions

- Keep persisted message schema aligned with `session/message.Message`.
- Use JSON marshal/unmarshal for schema/tool payload transformations when necessary.
- Maintain explicit role/tool-call mapping between persisted and in-memory model types.

### Comments and documentation

- Keep comments concise and behavior-focused.
- Prefer explaining "why" or protocol contracts over repeating the code.
- Maintain package/type/function comments for exported symbols.

## Test Style Guidelines

- Use Go `testing` with `testify/assert` and `testify/require` (current project standard).
- Prefer `require` for preconditions and `assert` for subsequent checks.
- Use table-driven tests where multiple input/output permutations exist.
- Name tests with `Test<TypeOrFunction>_<Scenario>`.
- For helpers in tests, call `t.Helper()`.
- Keep network/API tests env-gated and skippable.

## Change Checklist for Agents

Before editing:

- Read neighboring files in the same package to mirror patterns.
- Check whether behavior is part of a public interface or cross-package contract.

After editing:

- Run `go test ./...` at minimum.
- Run `go vet ./...` for static checks.
- If concurrency was touched, strongly consider `go test -race ./...`.
- Ensure new exported identifiers have doc comments.

## Cursor/Copilot Rules Audit

As of this file creation, no additional repository instruction files were found:

- No `.cursorrules`
- No `.cursor/rules/`
- No `.github/copilot-instructions.md`

If any of these files are added later, treat them as authoritative supplements to this AGENTS guide.
