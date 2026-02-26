# AGENTS.md - Development Guidelines for ADK

## Project Overview

ADK (Agent Development Kit) is a stateless Go agent framework with PostgreSQL persistence, HTTP API support, and multi-agent orchestration.

## Build / Test Commands

```bash
# Build entire project
go build ./...

# Build binaries
make build

# Run all tests with verbose output
go test -v ./...

# Run tests in specific package
go test -v ./pkg/agent

# Run single test by name pattern
go test -v -run ^TestEngine_ExecuteSequentialWorkflow$ ./pkg/orchestration

# Run tests with coverage
go test -v -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# Use make targets
make test            # Run all tests
make test-coverage   # Tests with coverage report
make lint            # Run golangci-lint
make fmt             # Format code
make deps            # Tidy dependencies
make clean           # Clean build artifacts
make dev             # Development with hot reload (air)
make run             # Run API server
make migrate         # Run database migrations
```

## Code Style Guidelines

### Imports
1. Standard library packages
2. Third-party packages  
3. Internal/project packages
```go
import (
    "context"
    "fmt"
    "time"

    "github.com/google/uuid"
    "soasurs.dev/soasurs/adk/internal/storage"
    "soasurs.dev/soasurs/adk/pkg/agent"
)
```
- Use `goimports` for automatic import management
- Avoid blank imports (`_`) unless absolutely necessary
- Never use dot imports (`.`)

### Naming Conventions
- **Packages**: lowercase, single word (e.g., `storage`, `agent`, `tool`)
- **Types/Structs**: PascalCase (e.g., `Session`, `RunResult`, `WorkflowStep`)
- **Interfaces**: PascalCase with `-er` suffix when possible (e.g., `Store`, `Registry`, `Provider`)
- **Variables**: camelCase (e.g., `sessionID`, `toolRegistry`, `maxIterations`)
- **Errors**: end with `Err` for sentinel errors (e.g., `ErrNotFound`, `ErrMaxIterationsReached`)

### Error Handling
```go
// Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to execute step %s: %w", stepID, err)
}

// Use sentinel errors for expected conditions
var ErrAgentNotFound = errors.New("agent not found")

// Always handle errors explicitly
result, err := agent.Run(ctx, sessionID, input)
if err != nil {
    return nil, err
}
```

### Concurrency
- Use `sync.RWMutex` for read-heavy access patterns
- Always defer unlock operations

## Multi-Agent Architecture

### Agent Registry
- Central registry for managing multiple specialized agents
- Pre-configured agents: `default`, `code`, `writer`
- Each agent can have different configurations (context size, system prompts)

### Workflow Engine
- **Sequential workflows**: Agent A → Agent B → Agent C
- **Parallel workflows**: Simultaneous agent execution
- **Conditional execution**: Dynamic routing based on results
- **Template variables**: Pass data between steps using `{{step.output}}`

### Session Routing
- Sessions are associated with specific agents via `agent_id`
- Message handlers automatically route to correct agent
- Default fallback to `default` agent if unspecified

## API Endpoints

### Core Endpoints
- `POST   /api/v1/sessions` - Create session (specify `agent_id`)
- `POST   /api/v1/sessions/:id/messages` - Send message (agent auto-routed)
- `POST   /api/v1/sessions/:id/stream` - Stream message (SSE)
- `GET    /api/v1/sessions/:id/messages` - Get conversation history
- `GET    /api/v1/runs/:id` - Get run status

### Multi-Agent Endpoints  
- `GET    /api/v1/agents` - List all registered agents
- `GET    /api/v1/agents/:id` - Get agent details

## Testing Guidelines

- Use `testify/assert` and `testify/require` for assertions
- Create mock implementations for dependencies
- Test both success and error cases
- Follow naming: `Test[Struct]_[Method]_[Scenario]`
- Keep tests focused and independent
