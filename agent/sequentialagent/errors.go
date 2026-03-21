package sequentialagent

import "errors"

// ErrNoAgents indicates that a SequentialAgent was constructed without any
// sub-agents.
var ErrNoAgents = errors.New("sequential: at least one agent is required")
