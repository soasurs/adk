package parallelagent

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/soasurs/adk/agent"
	"github.com/soasurs/adk/model"
)

// MergeFunc combines the collected outputs of all agents into a single
// assistant message that is yielded by the ParallelAgent.
//
// results contains one entry per agent in definition order. Each entry holds
// the agent's name and all messages it produced during its Run.
type MergeFunc func(results []AgentOutput) model.Message

// AgentOutput holds the name of an agent and the messages it produced.
type AgentOutput struct {
	// Name is the agent's Name() value.
	Name string
	// Messages are all messages yielded by the agent during its Run, in order.
	Messages []model.Message
}

// Config holds the configuration for a ParallelAgent.
type Config struct {
	// Name is the agent's name, returned by Name().
	Name string
	// Description is a human-readable description of the agent's purpose.
	Description string
	// Agents is the list of sub-agents to run concurrently.
	// At least one agent is required.
	Agents []agent.Agent
	// MergeFunc combines all agent outputs into a single assistant message.
	// Defaults to DefaultMergeFunc when nil.
	MergeFunc MergeFunc
}

// DefaultMergeFunc is the default result merger. Each agent's final assistant
// text content is formatted with an attribution header, in definition order:
//
//	[agent-name]
//	content of the last assistant text message
//
//	[agent-name-2]
//	content ...
//
// Agents that produce no assistant text are omitted.
func DefaultMergeFunc(results []AgentOutput) model.Message {
	var parts []string
	for _, r := range results {
		// Use the last non-empty assistant text produced by this agent.
		for i := len(r.Messages) - 1; i >= 0; i-- {
			if r.Messages[i].Role == model.RoleAssistant && r.Messages[i].Content != "" {
				parts = append(parts, fmt.Sprintf("[%s]\n%s", r.Name, r.Messages[i].Content))
				break
			}
		}
	}
	return model.Message{
		Role:    model.RoleAssistant,
		Content: strings.Join(parts, "\n\n"),
	}
}

// ParallelAgent fans out to all sub-agents concurrently with the same input
// messages. Each agent is independent — they do not share state and cannot see
// each other's output.
//
// Once all agents have finished, their results are passed to the configured
// MergeFunc (defaulting to DefaultMergeFunc), and the single merged assistant
// message is yielded. This ensures the output is always one message regardless
// of how many agents ran, keeping downstream conversation history well-formed.
//
// If any agent returns an error, the shared context is cancelled so that
// sibling agents can exit promptly. The error is then yielded and iteration ends.
//
// Typical use-cases:
//   - Fan-out: run multiple specialist agents on the same task, collect all answers.
//   - Multi-model: compare responses from different LLMs side-by-side.
//   - Independent tasks: run a translator and a summariser on the same text at once.
type ParallelAgent struct {
	config Config
}

// New creates a ParallelAgent from the given Config.
// Config.Agents must contain at least one agent.
// If Config.MergeFunc is nil, DefaultMergeFunc is used.
func New(cfg Config) agent.Agent {
	if len(cfg.Agents) == 0 {
		panic("parallel: at least one agent is required")
	}
	if cfg.MergeFunc == nil {
		cfg.MergeFunc = DefaultMergeFunc
	}
	return &ParallelAgent{config: cfg}
}

func (p *ParallelAgent) Name() string        { return p.config.Name }
func (p *ParallelAgent) Description() string { return p.config.Description }

// agentResult holds the collected output (or error) from a single agent run.
type agentResult struct {
	output AgentOutput
	err    error
}

// Run fans out to all agents concurrently, waits for all to complete, merges
// their outputs via MergeFunc, and yields the single resulting complete event.
//
// Execution model:
//  1. A child context is derived from ctx; all agents share it.
//  2. Each agent is launched in its own goroutine.
//  3. Run blocks until every goroutine finishes (via sync.WaitGroup).
//  4. If any agent errors, the child context is cancelled immediately so that
//     sibling agents can detect cancellation and exit early.
//  5. All AgentOutputs are passed to MergeFunc; the returned message is yielded.
//
// Note: partial (streaming) events from sub-agents are consumed silently;
// only complete messages are collected and merged.
func (p *ParallelAgent) Run(ctx context.Context, messages []model.Message) iter.Seq2[*model.Event, error] {
	return func(yield func(*model.Event, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		results := make([]agentResult, len(p.config.Agents))
		var wg sync.WaitGroup

		for i, a := range p.config.Agents {
			wg.Add(1)
			go func(idx int, ag agent.Agent) {
				defer wg.Done()
				var collected []model.Message
				for event, err := range ag.Run(ctx, messages) {
					if err != nil {
						results[idx] = agentResult{err: err}
						cancel() // signal sibling agents to stop
						return
					}
					// Only accumulate complete messages for merging.
					if !event.Partial {
						collected = append(collected, event.Message)
					}
				}
				results[idx] = agentResult{
					output: AgentOutput{
						Name:     ag.Name(),
						Messages: collected,
					},
				}
			}(i, a)
		}

		wg.Wait()

		// Check for errors in definition order; stop at the first one.
		outputs := make([]AgentOutput, 0, len(results))
		for _, r := range results {
			if r.err != nil {
				yield(nil, r.err)
				return
			}
			outputs = append(outputs, r.output)
		}

		// Merge all outputs into a single message and yield it as a complete event.
		merged := p.config.MergeFunc(outputs)
		yield(&model.Event{Message: merged, Partial: false}, nil)
	}
}
