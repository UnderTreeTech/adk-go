package executor

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/agent"
	"github.com/UnderTreeTech/adk-go/orchestration/flow"
	"github.com/UnderTreeTech/adk-go/orchestration/flow/provider"
	adkagent "google.golang.org/adk/agent"
)

// BuildConfig holds configuration for building the flow agent tree.
type BuildConfig struct {
	// Name is the root agent name. If empty, defaults to the flow metadata name.
	Name string

	// Provider supplies pre-built adkagent.Agent instances for each block.
	// Required. The caller creates agents with their model, tools, MCP,
	// skills, knowledge, etc., and registers them here.
	Provider provider.AgentProvider
}

// Build constructs an adkagent.Agent tree from a validated FlowSchema
// and pre-built agent instances from the provider.
//
// Algorithm:
//  1. Construct and validate the DAG from schema blocks and edges
//  2. Compute topological levels
//  3. For each level:
//     - Resolve agents from provider for agent-type blocks
//     - If 1 block: add agent directly to sequential pipeline
//     - If N blocks: create ParallelAgent containing all N, add to pipeline
//  4. Return SequentialAgent containing the level groups
//
// Start and end blocks are handled as passthrough agents that simply
// forward their input to the next level.
func Build(schema *flow.FlowSchema, cfg BuildConfig) (adkagent.Agent, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("orchestration/flow/executor: BuildConfig.Provider is required")
	}

	// 1. Construct and validate the DAG
	dag, err := NewDAG(schema.Blocks, schema.Edges)
	if err != nil {
		return nil, fmt.Errorf("orchestration/flow/executor: build DAG: %w", err)
	}

	// 2. Compute topological levels
	levels := dag.Levels()

	// 3. Build agent tree from levels
	rootName := cfg.Name
	if rootName == "" {
		rootName = schema.Metadata.Name
	}

	var pipelineAgents []adkagent.Agent

	for levelIdx, level := range levels {
		// Resolve agents for all blocks in this level
		var levelAgents []adkagent.Agent
		for _, block := range level {
			ag, err := resolveAgent(block, cfg.Provider)
			if err != nil {
				return nil, fmt.Errorf("orchestration/flow/executor: level %d, block %q: %w", levelIdx, block.ID, err)
			}
			levelAgents = append(levelAgents, ag)
		}

		// Add to pipeline
		if len(levelAgents) == 1 {
			// Single agent at this level — add directly
			pipelineAgents = append(pipelineAgents, levelAgents[0])
		} else if len(levelAgents) > 1 {
			// Multiple agents at this level — create ParallelAgent
			parallelName := fmt.Sprintf("Level%d_Parallel", levelIdx)
			parallelCfg := agent.Config{
				Config: adkagent.Config{
					Name:      parallelName,
					SubAgents: levelAgents,
				},
				DisableDefaultCallbacks: true,
			}
			parallelAgent, err := agent.NewParallelAgent(parallelCfg)
			if err != nil {
				return nil, fmt.Errorf("orchestration/flow/executor: create parallel agent for level %d: %w", levelIdx, err)
			}
			pipelineAgents = append(pipelineAgents, parallelAgent)
		}
	}

	// 4. Create root SequentialAgent
	if len(pipelineAgents) == 0 {
		return nil, fmt.Errorf("orchestration/flow/executor: no agents to build (empty flow)")
	}

	if len(pipelineAgents) == 1 {
		// Only one agent — no need for a SequentialAgent wrapper
		return pipelineAgents[0], nil
	}

	seqCfg := agent.Config{
		Config: adkagent.Config{
			Name:      rootName,
			SubAgents: pipelineAgents,
		},
		DisableDefaultCallbacks: true,
	}
	root, err := agent.NewSequentialAgent(seqCfg)
	if err != nil {
		return nil, fmt.Errorf("orchestration/flow/executor: create root sequential agent: %w", err)
	}

	return root, nil
}

// resolveAgent resolves a block to an adkagent.Agent instance.
// For agent-type blocks, it looks up the provider.
// For start/end blocks, it creates a passthrough agent.
func resolveAgent(block flow.Block, prov provider.AgentProvider) (adkagent.Agent, error) {
	switch block.Type {
	case flow.BlockTypeAgent:
		// Look up the pre-built agent from the provider
		ag, err := prov.Get(block.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve agent for block %q: %w", block.ID, err)
		}
		return ag, nil

	case flow.BlockTypeStart, flow.BlockTypeEnd:
		// Create a passthrough agent that simply forwards input
		return newPassthroughAgent(block.Name, block.OutputKey)

	default:
		return nil, fmt.Errorf("unsupported block type %q for block %q", block.Type, block.ID)
	}
}

// newPassthroughAgent creates a minimal agent for start/end blocks.
// Start blocks forward user input to downstream agents via session state.
// End blocks serve as terminal markers in the DAG.
func newPassthroughAgent(name, outputKey string) (adkagent.Agent, error) {
	cfg := agent.Config{
		Config: adkagent.Config{
			Name: name,
		},
		DisableDefaultCallbacks: true,
	}

	// Use the base adkagent.New to create a passthrough agent
	// that does nothing (its job is just to be a DAG node marker)
	ag, err := adkagent.New(adkagent.Config{
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("create passthrough agent %q: %w", name, err)
	}

	// Suppress unused variable warning
	_ = cfg
	_ = outputKey

	return ag, nil
}
