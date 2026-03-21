package parallelagent

import "errors"

// ErrNoAgents indicates that a ParallelAgent was constructed without any
// sub-agents.
var ErrNoAgents = errors.New("parallel: at least one agent is required")
