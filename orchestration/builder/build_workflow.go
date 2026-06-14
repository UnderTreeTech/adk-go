package builder

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/agent"
	"github.com/UnderTreeTech/adk-go/orchestration"
	adkagent "google.golang.org/adk/agent"
)

// buildSequentialAgent constructs a SequentialAgent from the schema node.
func (b *Builder) buildSequentialAgent(node orchestration.AgentNode) (adkagent.Agent, error) {
	subAgents, err := b.buildChildren(node.Children)
	if err != nil {
		return nil, fmt.Errorf("agent %q: build children: %w", node.Name, err)
	}

	beforeCallbacks, err := b.resolveBeforeAgentCallbacks(node.Callbacks.BeforeAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve callbacks: %w", node.Name, err)
	}
	afterCallbacks, err := b.resolveAfterAgentCallbacks(node.Callbacks.AfterAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve callbacks: %w", node.Name, err)
	}

	cfg := agent.Config{
		Config: adkagent.Config{
			Name:                 node.Name,
			Description:          node.Description,
			SubAgents:            subAgents,
			BeforeAgentCallbacks: beforeCallbacks,
			AfterAgentCallbacks:  afterCallbacks,
		},
		DisableDefaultCallbacks: node.DisableDefaultCallbacks,
	}

	ag, err := agent.NewSequentialAgent(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent %q: create sequential agent: %w", node.Name, err)
	}
	return ag, nil
}

// buildParallelAgent constructs a ParallelAgent from the schema node.
func (b *Builder) buildParallelAgent(node orchestration.AgentNode) (adkagent.Agent, error) {
	subAgents, err := b.buildChildren(node.Children)
	if err != nil {
		return nil, fmt.Errorf("agent %q: build children: %w", node.Name, err)
	}

	beforeCallbacks, err := b.resolveBeforeAgentCallbacks(node.Callbacks.BeforeAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve callbacks: %w", node.Name, err)
	}
	afterCallbacks, err := b.resolveAfterAgentCallbacks(node.Callbacks.AfterAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve callbacks: %w", node.Name, err)
	}

	cfg := agent.Config{
		Config: adkagent.Config{
			Name:                 node.Name,
			Description:          node.Description,
			SubAgents:            subAgents,
			BeforeAgentCallbacks: beforeCallbacks,
			AfterAgentCallbacks:  afterCallbacks,
		},
		DisableDefaultCallbacks: node.DisableDefaultCallbacks,
	}

	ag, err := agent.NewParallelAgent(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent %q: create parallel agent: %w", node.Name, err)
	}
	return ag, nil
}

// buildLoopAgent constructs a LoopAgent from the schema node.
func (b *Builder) buildLoopAgent(node orchestration.AgentNode) (adkagent.Agent, error) {
	subAgents, err := b.buildChildren(node.Children)
	if err != nil {
		return nil, fmt.Errorf("agent %q: build children: %w", node.Name, err)
	}

	beforeCallbacks, err := b.resolveBeforeAgentCallbacks(node.Callbacks.BeforeAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve callbacks: %w", node.Name, err)
	}
	afterCallbacks, err := b.resolveAfterAgentCallbacks(node.Callbacks.AfterAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve callbacks: %w", node.Name, err)
	}

	cfg := agent.Config{
		Config: adkagent.Config{
			Name:                 node.Name,
			Description:          node.Description,
			SubAgents:            subAgents,
			BeforeAgentCallbacks: beforeCallbacks,
			AfterAgentCallbacks:  afterCallbacks,
		},
		MaxIterations:           node.MaxIterations,
		DisableDefaultCallbacks: node.DisableDefaultCallbacks,
	}

	ag, err := agent.NewLoopAgent(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent %q: create loop agent: %w", node.Name, err)
	}
	return ag, nil
}
