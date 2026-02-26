package dto

import (
	"time"

	"github.com/google/uuid"
)

type APIResponse struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

func SuccessResponse(data any) APIResponse {
	return APIResponse{
		Success: true,
		Data:    data,
	}
}

func ErrorResponse(err string) APIResponse {
	return APIResponse{
		Success: false,
		Error:   err,
	}
}

type CreateSessionRequest struct {
	AgentID  string         `json:"agent_id"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type SessionResponse struct {
	ID        uuid.UUID      `json:"id"`
	AgentID   string         `json:"agent_id"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type SendMessageRequest struct {
	Content string `json:"content"`
}

type MessageResponse struct {
	ID        uuid.UUID `json:"id"`
	SessionID uuid.UUID `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type RunResponse struct {
	ID          uuid.UUID          `json:"id"`
	SessionID   uuid.UUID          `json:"session_id"`
	Status      string             `json:"status"`
	Input       string             `json:"input"`
	Output      string             `json:"output,omitempty"`
	Error       string             `json:"error,omitempty"`
	ToolCalls   []ToolCallResponse `json:"tool_calls,omitempty"`
	StartedAt   *time.Time         `json:"started_at,omitempty"`
	CompletedAt *time.Time         `json:"completed_at,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
}

type ToolCallResponse struct {
	ID     uuid.UUID      `json:"id"`
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type StreamRequest struct {
	Content string `json:"content"`
}

type StreamChunk struct {
	Type     string            `json:"type"`
	Content  string            `json:"content,omitempty"`
	ToolCall *ToolCallResponse `json:"tool_call,omitempty"`
	Done     bool              `json:"done,omitempty"`
	Error    string            `json:"error,omitempty"`
}

type AgentResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Config      any    `json:"config,omitempty"`
}

type CreateWorkflowRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Steps       []WorkflowStepRequest `json:"steps"`
	Parallel    bool                  `json:"parallel,omitempty"`
	MaxRetries  int                   `json:"max_retries,omitempty"`
	Metadata    map[string]any        `json:"metadata,omitempty"`
}

type WorkflowStepRequest struct {
	ID          string         `json:"id,omitempty"`
	AgentID     string         `json:"agent_id"`
	Description string         `json:"description,omitempty"`
	Input       string         `json:"input"`
	Condition   string         `json:"condition,omitempty"`
	Timeout     int            `json:"timeout,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type WorkflowResponse struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Steps       []WorkflowStepResponse `json:"steps"`
	Parallel    bool                   `json:"parallel,omitempty"`
	MaxRetries  int                    `json:"max_retries,omitempty"`
	Metadata    map[string]any         `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"created_at,omitempty"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
}

type WorkflowStepResponse struct {
	ID          string         `json:"id"`
	AgentID     string         `json:"agent_id"`
	Description string         `json:"description,omitempty"`
	Input       string         `json:"input"`
	Condition   string         `json:"condition,omitempty"`
	Timeout     int            `json:"timeout,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type ExecuteWorkflowRequest struct {
	WorkflowID string `json:"workflow_id"`
	Input      string `json:"input,omitempty"`
}

type WorkflowExecutionResponse struct {
	ID          string                  `json:"id"`
	WorkflowID  string                  `json:"workflow_id"`
	SessionID   uuid.UUID               `json:"session_id"`
	Status      string                  `json:"status"`
	Steps       []StepExecutionResponse `json:"steps"`
	Input       string                  `json:"input"`
	Output      string                  `json:"output,omitempty"`
	Error       string                  `json:"error,omitempty"`
	StartedAt   *time.Time              `json:"started_at,omitempty"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
}

type StepExecutionResponse struct {
	StepID   string    `json:"step_id"`
	AgentID  string    `json:"agent_id"`
	Input    string    `json:"input"`
	Output   string    `json:"output"`
	Error    string    `json:"error,omitempty"`
	Duration int64     `json:"duration,omitempty"`
	RunID    uuid.UUID `json:"run_id,omitempty"`
}
