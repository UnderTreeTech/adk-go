// Package builder constructs adkagent.Agent trees from validated
// orchestration.OrchestrationSchema. It recursively builds the agent
// tree using the existing agent.NewXxxAgent factory functions, resolving
// model, tool, and callback references via registries.
package builder

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/orchestration"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	adkagent "google.golang.org/adk/agent"
)

// BuilderConfig holds the registries needed to resolve references.
type BuilderConfig struct {
	// ModelRegistry resolves model references to model.LLM instances.
	ModelRegistry registry.ModelRegistry

	// ToolRegistry resolves tool references to tool.Tool instances.
	ToolRegistry registry.ToolRegistry

	// CallbackRegistry resolves callback references to actual callback functions.
	CallbackRegistry registry.CallbackRegistry
}

// Builder constructs adkagent.Agent trees from validated OrchestrationSchema.
type Builder struct {
	cfg BuilderConfig
}

// New creates a Builder with the given registries.
func New(cfg BuilderConfig) *Builder {
	return &Builder{cfg: cfg}
}

// Build constructs the root agent from a validated schema.
// The registries in BuilderConfig must already be populated.
func (b *Builder) Build(schema *orchestration.OrchestrationSchema) (adkagent.Agent, error) {
	agent, err := b.buildNode(schema.Agent)
	if err != nil {
		return nil, fmt.Errorf("orchestration/builder: schema %q: %w", schema.Metadata.Name, err)
	}
	return agent, nil
}

// buildNode recursively constructs an adkagent.Agent from an AgentNode.
func (b *Builder) buildNode(node orchestration.AgentNode) (adkagent.Agent, error) {
	switch node.Type {
	case orchestration.AgentTypeLLM:
		return b.buildLLMAgent(node)
	case orchestration.AgentTypeSequential:
		return b.buildSequentialAgent(node)
	case orchestration.AgentTypeParallel:
		return b.buildParallelAgent(node)
	case orchestration.AgentTypeLoop:
		return b.buildLoopAgent(node)
	default:
		return nil, fmt.Errorf("unknown agent type %q", node.Type)
	}
}

// buildChildren recursively builds all children and returns the list of agents.
func (b *Builder) buildChildren(children []orchestration.AgentNode) ([]adkagent.Agent, error) {
	agents := make([]adkagent.Agent, 0, len(children))
	for i, child := range children {
		ag, err := b.buildNode(child)
		if err != nil {
			return nil, fmt.Errorf("children[%d]: %w", i, err)
		}
		agents = append(agents, ag)
	}
	return agents, nil
}
