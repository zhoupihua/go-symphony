package agent

import (
	"fmt"
	"sync"
)

// AgentFactory creates an Agent from adapter-specific config.
type AgentFactory func(config map[string]any) (Agent, error)

var (
	agentRegistry = map[string]AgentFactory{}
	agentMu       sync.RWMutex
)

// RegisterAgent registers an AgentFactory under the given kind.
// It panics if a factory is already registered for that kind.
func RegisterAgent(kind string, factory AgentFactory) {
	agentMu.Lock()
	defer agentMu.Unlock()

	if _, exists := agentRegistry[kind]; exists {
		panic(fmt.Sprintf("agent already registered: %s", kind))
	}
	agentRegistry[kind] = factory
}

// NewAgent creates an Agent of the given kind using the provided config.
// Returns an error if the kind is unknown.
func NewAgent(kind string, config map[string]any) (Agent, error) {
	agentMu.RLock()
	factory, ok := agentRegistry[kind]
	agentMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return factory(config)
}
