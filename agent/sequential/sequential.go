package sequential

import (
	"context"
	"iter"

	"soasurs.dev/soasurs/adk/agent"
	"soasurs.dev/soasurs/adk/model"
)

// Config holds the configuration for a SequentialAgent.
type Config struct {
	Name        string
	Description string
	Agents      []agent.Agent
}

// SequentialAgent runs a fixed list of agents one after another in order.
// Each agent receives the original input messages plus all messages produced
// by every preceding agent, giving it full context of what happened before.
// All messages from all agents are yielded in the order they are produced.
//
// Between agents, a handoff user message is injected so that each agent
// receives a conversation ending with a user turn, which LLMs expect.
//
// This is useful for building multi-step pipelines where the output of one
// step enriches the context for the next step:
//
//	research → draft → review
type SequentialAgent struct {
	config Config
}

// New creates a SequentialAgent from the given Config.
// At least one agent must be provided.
func New(cfg Config) agent.Agent {
	if len(cfg.Agents) == 0 {
		panic("sequential: at least one agent is required")
	}
	return &SequentialAgent{config: cfg}
}

func (s *SequentialAgent) Name() string        { return s.config.Name }
func (s *SequentialAgent) Description() string { return s.config.Description }

// Run executes the agent pipeline sequentially.
//
// For each agent in the list:
//  1. Build its input as: original messages + all messages produced so far.
//  2. For agents after the first, inject a handoff user message.
//  3. Run it and yield every message it produces.
//  4. Append those messages to the accumulated context for the next agent.
//
// Iteration stops early (without error) if the caller breaks out of the loop.
// If any agent returns an error, the error is yielded and iteration stops.
func (s *SequentialAgent) Run(ctx context.Context, messages []model.Message) iter.Seq2[model.Message, error] {
	return func(yield func(model.Message, error) bool) {
		// accumulated holds messages produced by agents that have already run.
		accumulated := make([]model.Message, 0)

		for i, a := range s.config.Agents {
			// Build this agent's input: original messages + accumulated context.
			input := make([]model.Message, 0, len(messages)+len(accumulated)+1)
			input = append(input, messages...)
			input = append(input, accumulated...)

			// For agents after the first, inject a handoff message so the LLM
			// sees a conversation ending with a user turn.
			if i > 0 && len(accumulated) > 0 {
				handoff := model.Message{
					Role:    model.RoleUser,
					Content: "Please proceed.",
				}
				input = append(input, handoff)
			}

			for msg, err := range a.Run(ctx, input) {
				if err != nil {
					yield(model.Message{}, err)
					return
				}
				if !yield(msg, nil) {
					return
				}
				accumulated = append(accumulated, msg)
			}
		}
	}
}
