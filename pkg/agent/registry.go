package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
)

type Registry interface {
	Register(id string, agent Agent) error
	Unregister(id string) error
	Get(id string) (Agent, error)
	GetInfo(id string) (AgentInfo, error)
	Has(id string) bool
	List() []AgentInfo
	Execute(ctx context.Context, agentID string, sessionID uuid.UUID, input string) (*RunResult, error)
}

type AgentInfo struct {
	ID          string
	Name        string
	Description string
	Config      *Config
}

type registry struct {
	mu     sync.RWMutex
	agents map[string]Agent
	infos  map[string]*AgentInfo
}

func NewRegistry() Registry {
	return &registry{
		agents: make(map[string]Agent),
		infos:  make(map[string]*AgentInfo),
	}
}

func (r *registry) Register(id string, agent Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if id == "" {
		return fmt.Errorf("agent id cannot be empty")
	}

	if _, exists := r.agents[id]; exists {
		return fmt.Errorf("agent with id %s already registered", id)
	}

	r.agents[id] = agent

	if cfg, ok := agent.(interface{ GetConfig() *Config }); ok {
		config := cfg.GetConfig()
		r.infos[id] = &AgentInfo{
			ID:          id,
			Name:        config.Name,
			Description: config.Description,
			Config:      config,
		}
	} else {
		r.infos[id] = &AgentInfo{
			ID:   id,
			Name: id,
		}
	}

	return nil
}

func (r *registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[id]; !exists {
		return fmt.Errorf("agent with id %s not found", id)
	}

	delete(r.agents, id)
	delete(r.infos, id)
	return nil
}

func (r *registry) Get(id string) (Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[id]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", id)
	}

	return agent, nil
}

func (r *registry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.agents[id]
	return exists
}

func (r *registry) List() []AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]AgentInfo, 0, len(r.infos))
	for _, info := range r.infos {
		result = append(result, *info)
	}
	return result
}

func (r *registry) GetInfo(id string) (AgentInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.infos[id]
	if !exists {
		return AgentInfo{}, fmt.Errorf("agent %s not found", id)
	}
	return *info, nil
}

func (r *registry) Execute(ctx context.Context, agentID string, sessionID uuid.UUID, input string) (*RunResult, error) {
	agent, err := r.Get(agentID)
	if err != nil {
		return nil, err
	}

	return agent.Run(ctx, sessionID, input)
}

func CreateAgentFromConfig(id string, cfg *Config, store storage.Store) Agent {
	agent := NewAgent(cfg, store)

	if agentWithID, ok := agent.(interface{ SetID(string) }); ok {
		agentWithID.SetID(id)
	}

	return agent
}

func DefaultAgentConfigs() map[string]*Config {
	return map[string]*Config{
		"default": {
			ID:               "default",
			Name:             "Default Assistant",
			Description:      "General purpose AI assistant",
			MaxIterations:    10,
			MaxHistory:       20,
			SystemPrompt:     "You are a helpful AI assistant.",
			MaxContextTokens: 8000,
			ContextStrategy:  "hybrid",
		},
		"code": {
			ID:               "code",
			Name:             "Code Assistant",
			Description:      "Specialized in programming and code analysis",
			MaxIterations:    15,
			MaxHistory:       30,
			SystemPrompt:     "You are an expert programming assistant. Help users write, debug, and understand code.",
			MaxContextTokens: 16000,
			ContextStrategy:  "sliding",
		},
		"writer": {
			ID:               "writer",
			Name:             "Writing Assistant",
			Description:      "Helps with writing, editing, and content creation",
			MaxIterations:    8,
			MaxHistory:       15,
			SystemPrompt:     "You are a professional writing assistant. Help users write, edit, and improve their content.",
			MaxContextTokens: 4000,
			ContextStrategy:  "summary",
		},
	}
}
