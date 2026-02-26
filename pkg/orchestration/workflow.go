package orchestration

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
	"soasurs.dev/soasurs/adk/pkg/agent"
)

type WorkflowStep struct {
	ID          string         `json:"id"`
	AgentID     string         `json:"agent_id"`
	Description string         `json:"description,omitempty"`
	Input       string         `json:"input"`
	Condition   string         `json:"condition,omitempty"`
	Timeout     int            `json:"timeout,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type Workflow struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Steps       []WorkflowStep `json:"steps"`
	Parallel    bool           `json:"parallel,omitempty"`
	MaxRetries  int            `json:"max_retries,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
}

type StepResult struct {
	StepID   string    `json:"step_id"`
	AgentID  string    `json:"agent_id"`
	Input    string    `json:"input"`
	Output   string    `json:"output"`
	Error    string    `json:"error,omitempty"`
	Duration int64     `json:"duration,omitempty"`
	RunID    uuid.UUID `json:"run_id,omitempty"`
}

type WorkflowExecution struct {
	ID          string         `json:"id"`
	WorkflowID  string         `json:"workflow_id"`
	SessionID   uuid.UUID      `json:"session_id"`
	Status      string         `json:"status"`
	Steps       []StepResult   `json:"steps"`
	Input       string         `json:"input"`
	Output      string         `json:"output,omitempty"`
	Error       string         `json:"error,omitempty"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Context     map[string]any `json:"context,omitempty"`
}

type Engine interface {
	RegisterWorkflow(workflow *Workflow) error
	GetWorkflow(id string) (*Workflow, error)
	ListWorkflows() []*Workflow
	Execute(ctx context.Context, workflowID string, sessionID uuid.UUID, input string) (*WorkflowExecution, error)
	GetExecution(id string) (*WorkflowExecution, error)
}

type engine struct {
	mu         sync.RWMutex
	workflows  map[string]*Workflow
	executions map[string]*WorkflowExecution
	registry   agent.Registry
	store      storage.Store
}

func NewEngine(registry agent.Registry, store storage.Store) Engine {
	return &engine{
		workflows:  make(map[string]*Workflow),
		executions: make(map[string]*WorkflowExecution),
		registry:   registry,
		store:      store,
	}
}

func (e *engine) RegisterWorkflow(workflow *Workflow) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if workflow.ID == "" {
		workflow.ID = uuid.New().String()
	}
	if workflow.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if len(workflow.Steps) == 0 {
		return fmt.Errorf("workflow must have at least one step")
	}

	for i, step := range workflow.Steps {
		if step.AgentID == "" {
			return fmt.Errorf("step %d: agent_id is required", i)
		}
		if step.ID == "" {
			workflow.Steps[i].ID = fmt.Sprintf("step_%d", i)
		}
	}

	workflow.CreatedAt = time.Now()
	workflow.UpdatedAt = time.Now()
	e.workflows[workflow.ID] = workflow
	return nil
}

func (e *engine) GetWorkflow(id string) (*Workflow, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	workflow, exists := e.workflows[id]
	if !exists {
		return nil, fmt.Errorf("workflow %s not found", id)
	}
	return workflow, nil
}

func (e *engine) ListWorkflows() []*Workflow {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Workflow, 0, len(e.workflows))
	for _, w := range e.workflows {
		result = append(result, w)
	}
	return result
}

func (e *engine) Execute(ctx context.Context, workflowID string, sessionID uuid.UUID, input string) (*WorkflowExecution, error) {
	workflow, err := e.GetWorkflow(workflowID)
	if err != nil {
		return nil, err
	}

	executionID := uuid.New().String()
	startTime := time.Now()

	execution := &WorkflowExecution{
		ID:         executionID,
		WorkflowID: workflowID,
		SessionID:  sessionID,
		Status:     "running",
		Input:      input,
		StartedAt:  &startTime,
		CreatedAt:  time.Now(),
		Context:    make(map[string]any),
	}

	e.mu.Lock()
	e.executions[executionID] = execution
	e.mu.Unlock()

	defer func() {
		completionTime := time.Now()
		execution.CompletedAt = &completionTime
		if execution.Status == "running" {
			execution.Status = "completed"
		}
	}()

	stepResults := make([]StepResult, 0, len(workflow.Steps))
	execution.Context["input"] = input

	if workflow.Parallel {
		var wg sync.WaitGroup
		resultsChan := make(chan StepResult, len(workflow.Steps))
		errChan := make(chan error, len(workflow.Steps))

		for _, step := range workflow.Steps {
			wg.Add(1)
			go func(step WorkflowStep) {
				defer wg.Done()
				result, err := e.executeStep(ctx, step, sessionID, execution.Context)
				if err != nil {
					errChan <- fmt.Errorf("step %s failed: %w", step.ID, err)
					return
				}
				resultsChan <- result
			}(step)
		}

		wg.Wait()
		close(resultsChan)
		close(errChan)

		for err := range errChan {
			execution.Status = "failed"
			execution.Error = err.Error()
			return execution, err
		}

		for result := range resultsChan {
			stepResults = append(stepResults, result)
			execution.Context[result.StepID] = result.Output
		}
	} else {
		for _, step := range workflow.Steps {
			result, err := e.executeStep(ctx, step, sessionID, execution.Context)
			if err != nil {
				execution.Status = "failed"
				execution.Error = fmt.Sprintf("step %s failed: %v", step.ID, err)
				return execution, err
			}

			stepResults = append(stepResults, result)
			execution.Context[result.StepID] = result.Output
			execution.Context["last_output"] = result.Output
		}
	}

	execution.Steps = stepResults
	if len(stepResults) > 0 {
		execution.Output = stepResults[len(stepResults)-1].Output
	}
	execution.Status = "completed"

	return execution, nil
}

func (e *engine) executeStep(ctx context.Context, step WorkflowStep, sessionID uuid.UUID, context map[string]any) (StepResult, error) {
	startTime := time.Now()
	result := StepResult{
		StepID:  step.ID,
		AgentID: step.AgentID,
	}

	input, err := renderTemplate(step.Input, context)
	if err != nil {
		result.Error = fmt.Sprintf("template error: %v", err)
		return result, err
	}
	result.Input = input

	if step.Condition != "" {
		condition, err := renderTemplate(step.Condition, context)
		if err != nil {
			result.Error = fmt.Sprintf("condition template error: %v", err)
			return result, err
		}
		if condition != "true" && condition != "1" {
			result.Output = "step skipped due to condition"
			result.Duration = time.Since(startTime).Milliseconds()
			return result, nil
		}
	}

	agent, err := e.registry.Get(step.AgentID)
	if err != nil {
		result.Error = fmt.Sprintf("agent not found: %v", err)
		return result, err
	}

	runResult, err := agent.Run(ctx, sessionID, input)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	result.Output = runResult.Output
	result.RunID = runResult.RunID
	result.Duration = time.Since(startTime).Milliseconds()
	return result, nil
}

func (e *engine) GetExecution(id string) (*WorkflowExecution, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	execution, exists := e.executions[id]
	if !exists {
		return nil, fmt.Errorf("execution %s not found", id)
	}
	return execution, nil
}

func renderTemplate(tmpl string, data map[string]any) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}

	re := regexp.MustCompile(`\{\{([^}]+)\}\}`)
	result := re.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := match[2 : len(match)-2]
		key = strings.TrimSpace(key)
		if val, ok := data[key]; ok {
			return fmt.Sprint(val)
		}
		return match
	})

	return result, nil
}

var _ Engine = (*engine)(nil)
