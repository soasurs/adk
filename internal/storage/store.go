package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID        uuid.UUID
	AgentID   string
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID         uuid.UUID
	SessionID  uuid.UUID
	Role       string
	Content    string
	ToolCalls  []ToolCall
	TokenCount int
	CreatedAt  time.Time
}

type ToolCall struct {
	ID     uuid.UUID
	RunID  uuid.UUID
	Name   string
	Args   map[string]any
	Result any
	Error  string
}

type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

type Run struct {
	ID          uuid.UUID
	SessionID   uuid.UUID
	Status      RunStatus
	Input       string
	Output      string
	Error       string
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	ToolCalls   []ToolCall
}

type Store interface {
	SessionStore
	MessageStore
	RunStore
}

type SessionStore interface {
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, id uuid.UUID) (*Session, error)
	UpdateSession(ctx context.Context, session *Session) error
	DeleteSession(ctx context.Context, id uuid.UUID) error
}

type MessageStore interface {
	SaveMessage(ctx context.Context, msg *Message) error
	GetConversation(ctx context.Context, sessionID uuid.UUID, limit int) ([]Message, error)
	GetMessage(ctx context.Context, id uuid.UUID) (*Message, error)
}

type RunStore interface {
	CreateRun(ctx context.Context, run *Run) error
	GetRun(ctx context.Context, id uuid.UUID) (*Run, error)
	UpdateRun(ctx context.Context, run *Run) error
	GetRunsBySession(ctx context.Context, sessionID uuid.UUID, limit int) ([]Run, error)
}
