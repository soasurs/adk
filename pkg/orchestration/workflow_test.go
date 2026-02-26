package orchestration

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"soasurs.dev/soasurs/adk/internal/storage"
	"soasurs.dev/soasurs/adk/pkg/agent"
)

type mockAgent struct {
	id        string
	config    *agent.Config
	responses map[string]string
}

func (m *mockAgent) Run(ctx context.Context, sessionID uuid.UUID, input string) (*agent.RunResult, error) {
	response := m.responses[input]
	if response == "" {
		response = "Default response for: " + input
	}
	return &agent.RunResult{
		RunID:  uuid.New(),
		Output: response,
	}, nil
}

func (m *mockAgent) RunWithHistory(ctx context.Context, sessionID uuid.UUID, input string, maxHistory int) (*agent.RunResult, error) {
	return m.Run(ctx, sessionID, input)
}

func (m *mockAgent) GetConfig() *agent.Config {
	return m.config
}

type mockStore struct{}

func (m *mockStore) CreateSession(ctx context.Context, session *storage.Session) error { return nil }
func (m *mockStore) GetSession(ctx context.Context, id uuid.UUID) (*storage.Session, error) {
	return &storage.Session{ID: id, AgentID: "default"}, nil
}
func (m *mockStore) UpdateSession(ctx context.Context, session *storage.Session) error { return nil }
func (m *mockStore) DeleteSession(ctx context.Context, id uuid.UUID) error             { return nil }
func (m *mockStore) SaveMessage(ctx context.Context, msg *storage.Message) error       { return nil }
func (m *mockStore) GetConversation(ctx context.Context, sessionID uuid.UUID, limit int) ([]storage.Message, error) {
	return nil, nil
}
func (m *mockStore) GetMessage(ctx context.Context, id uuid.UUID) (*storage.Message, error) {
	return nil, nil
}
func (m *mockStore) CreateRun(ctx context.Context, run *storage.Run) error          { return nil }
func (m *mockStore) GetRun(ctx context.Context, id uuid.UUID) (*storage.Run, error) { return nil, nil }
func (m *mockStore) UpdateRun(ctx context.Context, run *storage.Run) error          { return nil }
func (m *mockStore) GetRunsBySession(ctx context.Context, sessionID uuid.UUID, limit int) ([]storage.Run, error) {
	return nil, nil
}

type mockRegistry struct {
	agents map[string]agent.Agent
}

func (m *mockRegistry) Register(id string, a agent.Agent) error {
	m.agents[id] = a
	return nil
}

func (m *mockRegistry) Unregister(id string) error {
	delete(m.agents, id)
	return nil
}

func (m *mockRegistry) Get(id string) (agent.Agent, error) {
	a, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", id)
	}
	return a, nil
}

func (m *mockRegistry) GetInfo(id string) (agent.AgentInfo, error) {
	a, ok := m.agents[id]
	if !ok {
		return agent.AgentInfo{}, fmt.Errorf("agent %s not found", id)
	}
	var config *agent.Config
	if cfg, ok := a.(interface{ GetConfig() *agent.Config }); ok {
		config = cfg.GetConfig()
	}
	return agent.AgentInfo{
		ID:     id,
		Name:   id,
		Config: config,
	}, nil
}

func (m *mockRegistry) Has(id string) bool {
	_, ok := m.agents[id]
	return ok
}

func (m *mockRegistry) List() []agent.AgentInfo {
	var infos []agent.AgentInfo
	for id := range m.agents {
		info, _ := m.GetInfo(id)
		infos = append(infos, info)
	}
	return infos
}

func (m *mockRegistry) Execute(ctx context.Context, agentID string, sessionID uuid.UUID, input string) (*agent.RunResult, error) {
	a, err := m.Get(agentID)
	if err != nil {
		return nil, err
	}
	return a.Run(ctx, sessionID, input)
}

func TestEngine_RegisterWorkflow(t *testing.T) {
	registry := &mockRegistry{agents: make(map[string]agent.Agent)}
	store := &mockStore{}
	engine := NewEngine(registry, store).(*engine)

	workflow := &Workflow{
		Name:        "Test Workflow",
		Description: "A test workflow",
		Steps: []WorkflowStep{
			{
				ID:      "step1",
				AgentID: "agent1",
				Input:   "Hello",
			},
		},
	}

	err := engine.RegisterWorkflow(workflow)
	require.NoError(t, err)
	assert.NotEmpty(t, workflow.ID)

	retrieved, err := engine.GetWorkflow(workflow.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.Name, retrieved.Name)
	assert.Equal(t, len(workflow.Steps), len(retrieved.Steps))
}

func TestEngine_ExecuteSequentialWorkflow(t *testing.T) {
	registry := &mockRegistry{agents: make(map[string]agent.Agent)}
	store := &mockStore{}

	agent1 := &mockAgent{
		id: "agent1",
		responses: map[string]string{
			"Hello": "Response from agent1",
		},
	}
	agent2 := &mockAgent{
		id: "agent2",
		responses: map[string]string{
			"Response from agent1": "Final response",
		},
	}

	registry.Register("agent1", agent1)
	registry.Register("agent2", agent2)

	engine := NewEngine(registry, store).(*engine)

	workflow := &Workflow{
		Name: "Sequential Workflow",
		Steps: []WorkflowStep{
			{
				ID:      "step1",
				AgentID: "agent1",
				Input:   "Hello",
			},
			{
				ID:      "step2",
				AgentID: "agent2",
				Input:   "{{step1}}",
			},
		},
	}

	err := engine.RegisterWorkflow(workflow)
	require.NoError(t, err)

	sessionID := uuid.New()
	execution, err := engine.Execute(context.Background(), workflow.ID, sessionID, "Initial input")
	require.NoError(t, err)
	assert.Equal(t, "completed", execution.Status)
	assert.Equal(t, 2, len(execution.Steps))
	assert.Equal(t, "Final response", execution.Output)
	assert.Equal(t, "Response from agent1", execution.Steps[0].Output)
	assert.Equal(t, "Final response", execution.Steps[1].Output)
}

func TestEngine_ExecuteParallelWorkflow(t *testing.T) {
	registry := &mockRegistry{agents: make(map[string]agent.Agent)}
	store := &mockStore{}

	agent1 := &mockAgent{
		id: "agent1",
		responses: map[string]string{
			"Task A": "Result A",
		},
	}
	agent2 := &mockAgent{
		id: "agent2",
		responses: map[string]string{
			"Task B": "Result B",
		},
	}

	registry.Register("agent1", agent1)
	registry.Register("agent2", agent2)

	engine := NewEngine(registry, store).(*engine)

	workflow := &Workflow{
		Name:     "Parallel Workflow",
		Parallel: true,
		Steps: []WorkflowStep{
			{
				ID:      "step1",
				AgentID: "agent1",
				Input:   "Task A",
			},
			{
				ID:      "step2",
				AgentID: "agent2",
				Input:   "Task B",
			},
		},
	}

	err := engine.RegisterWorkflow(workflow)
	require.NoError(t, err)

	sessionID := uuid.New()
	execution, err := engine.Execute(context.Background(), workflow.ID, sessionID, "Initial input")
	require.NoError(t, err)
	assert.Equal(t, "completed", execution.Status)
	assert.Equal(t, 2, len(execution.Steps))

	outputs := make(map[string]string)
	for _, step := range execution.Steps {
		outputs[step.StepID] = step.Output
	}
	assert.Equal(t, "Result A", outputs["step1"])
	assert.Equal(t, "Result B", outputs["step2"])
}

func TestEngine_ExecuteConditionalWorkflow(t *testing.T) {
	registry := &mockRegistry{agents: make(map[string]agent.Agent)}
	store := &mockStore{}

	agent1 := &mockAgent{
		id: "agent1",
		responses: map[string]string{
			"Check condition": "true",
		},
	}
	agent2 := &mockAgent{
		id: "agent2",
		responses: map[string]string{
			"Execute if true": "Executed",
		},
	}

	registry.Register("agent1", agent1)
	registry.Register("agent2", agent2)

	engine := NewEngine(registry, store).(*engine)

	workflow := &Workflow{
		Name: "Conditional Workflow",
		Steps: []WorkflowStep{
			{
				ID:      "step1",
				AgentID: "agent1",
				Input:   "Check condition",
			},
			{
				ID:        "step2",
				AgentID:   "agent2",
				Input:     "Execute if true",
				Condition: "{{step1}}",
			},
		},
	}

	err := engine.RegisterWorkflow(workflow)
	require.NoError(t, err)

	sessionID := uuid.New()
	execution, err := engine.Execute(context.Background(), workflow.ID, sessionID, "Initial input")
	require.NoError(t, err)
	assert.Equal(t, "completed", execution.Status)
	assert.Equal(t, 2, len(execution.Steps))
	assert.Equal(t, "Executed", execution.Output)
}

func TestEngine_ExecuteWorkflowWithMissingAgent(t *testing.T) {
	registry := &mockRegistry{agents: make(map[string]agent.Agent)}
	store := &mockStore{}

	engine := NewEngine(registry, store).(*engine)

	workflow := &Workflow{
		Name: "Failing Workflow",
		Steps: []WorkflowStep{
			{
				ID:      "step1",
				AgentID: "nonexistent",
				Input:   "Hello",
			},
		},
	}

	err := engine.RegisterWorkflow(workflow)
	require.NoError(t, err)

	sessionID := uuid.New()
	execution, err := engine.Execute(context.Background(), workflow.ID, sessionID, "Initial input")
	require.Error(t, err)
	assert.Equal(t, "failed", execution.Status)
	assert.Contains(t, execution.Error, "agent")
}

func TestEngine_GetExecution(t *testing.T) {
	registry := &mockRegistry{agents: make(map[string]agent.Agent)}
	store := &mockStore{}

	agent1 := &mockAgent{
		id: "agent1",
		responses: map[string]string{
			"Test": "Response",
		},
	}
	registry.Register("agent1", agent1)

	engine := NewEngine(registry, store).(*engine)

	workflow := &Workflow{
		Name: "Simple Workflow",
		Steps: []WorkflowStep{
			{
				ID:      "step1",
				AgentID: "agent1",
				Input:   "Test",
			},
		},
	}

	err := engine.RegisterWorkflow(workflow)
	require.NoError(t, err)

	sessionID := uuid.New()
	execution, err := engine.Execute(context.Background(), workflow.ID, sessionID, "Input")
	require.NoError(t, err)

	retrieved, err := engine.GetExecution(execution.ID)
	require.NoError(t, err)
	assert.Equal(t, execution.ID, retrieved.ID)
	assert.Equal(t, execution.Status, retrieved.Status)
	assert.Equal(t, execution.Output, retrieved.Output)
}
