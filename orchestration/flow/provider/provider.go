// Package provider defines the AgentProvider interface that supplies
// pre-built agent.Agent instances for flow blocks.
//
// The caller (business layer) creates fully-configured agents with their
// model, tools, MCP, skills, knowledge, etc., and registers them with
// an AgentProvider. The flow orchestration system only handles the
// arrangement (data flow, branching, merging), not agent execution.
package provider

import (
	"fmt"
	"sort"
	"sync"

	adkagent "google.golang.org/adk/agent"
)

// AgentProvider supplies pre-built adkagent.Agent instances for flow blocks.
//
// The caller creates agents with their model, tools, MCP, skills, knowledge
// and registers them here. The flow system only handles arrangement
// (data flow, branching, merging) — it does not care about how each
// individual agent executes.
type AgentProvider interface {
	// Get returns the pre-built agent for the given block ID.
	// Returns error if no agent is registered for the block.
	Get(blockID string) (adkagent.Agent, error)

	// BlockIDs returns all registered block IDs in sorted order.
	BlockIDs() []string
}

// MapAgentProvider implements AgentProvider with a concurrent-safe map.
type MapAgentProvider struct {
	mu     sync.RWMutex
	agents map[string]adkagent.Agent
}

// NewMapAgentProvider creates an empty MapAgentProvider.
func NewMapAgentProvider() *MapAgentProvider {
	return &MapAgentProvider{
		agents: make(map[string]adkagent.Agent),
	}
}

// Register adds a pre-built agent under the given block ID.
// Returns error if a duplicate block ID is registered.
func (p *MapAgentProvider) Register(blockID string, agent adkagent.Agent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.agents[blockID]; exists {
		return fmt.Errorf("orchestration/flow/provider: duplicate agent registration for block %q", blockID)
	}
	p.agents[blockID] = agent
	return nil
}

// Get returns the pre-built agent for the given block ID.
func (p *MapAgentProvider) Get(blockID string) (adkagent.Agent, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	agent, ok := p.agents[blockID]
	if !ok {
		return nil, fmt.Errorf("orchestration/flow/provider: no agent registered for block %q", blockID)
	}
	return agent, nil
}

// BlockIDs returns all registered block IDs in sorted order.
func (p *MapAgentProvider) BlockIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]string, 0, len(p.agents))
	for id := range p.agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AgentProviderFunc adapts a function to the AgentProvider interface.
// Useful for dynamic agent resolution or lazy construction.
type AgentProviderFunc func(blockID string) (adkagent.Agent, error)

// Get calls the underlying function to resolve the agent for the block.
func (f AgentProviderFunc) Get(blockID string) (adkagent.Agent, error) {
	return f(blockID)
}

// BlockIDs returns nil for function-based providers since the set of
// available block IDs is not known a priori.
func (f AgentProviderFunc) BlockIDs() []string {
	return nil
}
