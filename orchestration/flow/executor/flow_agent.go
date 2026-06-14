package executor

import (
	"fmt"
	"iter"

	"github.com/UnderTreeTech/adk-go/agent"
	"github.com/UnderTreeTech/adk-go/orchestration/flow"
	"github.com/UnderTreeTech/adk-go/orchestration/flow/provider"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

// FlowDAGAgentConfig holds configuration for creating a FlowDAGAgent.
type FlowDAGAgentConfig struct {
	// Name is the root agent name.
	Name string

	// Schema is the validated FlowSchema.
	Schema *flow.FlowSchema

	// DAG is the pre-constructed DAG (must match Schema).
	DAG *DAG

	// Provider supplies pre-built adkagent.Agent instances for each block.
	Provider provider.AgentProvider
}

// NewFlowDAGAgent creates an adkagent.Agent that executes a DAG with
// edge-level condition evaluation and runtime pruning.
//
// Unlike the static SequentialAgent+ParallelAgent model (where all blocks
// are always executed), FlowDAGAgent evaluates Edge.Condition at runtime
// before each level. Unreachable blocks are pruned (not executed) and
// their SkipOutput is written to session state as default values.
//
// The returned agent implements adkagent.Agent via adkagent.New() with
// a custom Run function.
func NewFlowDAGAgent(cfg FlowDAGAgentConfig) (adkagent.Agent, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("orchestration/flow/executor: FlowDAGAgentConfig.Provider is required")
	}
	if cfg.DAG == nil {
		return nil, fmt.Errorf("orchestration/flow/executor: FlowDAGAgentConfig.DAG is required")
	}
	if cfg.Schema == nil {
		return nil, fmt.Errorf("orchestration/flow/executor: FlowDAGAgentConfig.Schema is required")
	}

	// Pre-resolve all agents from provider (fail fast)
	agentMap := make(map[string]adkagent.Agent)
	for _, block := range cfg.Schema.Blocks {
		if block.Type == flow.BlockTypeAgent {
			ag, err := cfg.Provider.Get(block.ID)
			if err != nil {
				return nil, fmt.Errorf("orchestration/flow/executor: resolve agent for block %q: %w", block.ID, err)
			}
			agentMap[block.ID] = ag
		}
	}

	// Capture config for the Run closure
	dag := cfg.DAG
	schema := cfg.Schema
	name := cfg.Name
	if name == "" {
		name = schema.Metadata.Name
	}

	// Create the agent with a custom Run function
	ag, err := adkagent.New(adkagent.Config{
		Name:        name,
		Description: fmt.Sprintf("Flow DAG Agent: %s", schema.Metadata.Description),
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return runFlowDAG(ctx, dag, schema, agentMap)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("orchestration/flow/executor: create FlowDAGAgent: %w", err)
	}

	return ag, nil
}

// runFlowDAG is the core execution loop for FlowDAGAgent.
// It processes the DAG level by level, evaluating edge conditions
// at runtime and pruning unreachable blocks.
func runFlowDAG(
	ctx adkagent.InvocationContext,
	dag *DAG,
	schema *flow.FlowSchema,
	agentMap map[string]adkagent.Agent,
) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		levels := dag.Levels()

		for _, level := range levels {
			// 1. Build condition evaluator from current session state
			evaluator := &sessionStateEvaluator{state: ctx.Session().State()}

			// 2. Compute reachability
			activeBlocks := dag.ActiveBlocks(evaluator)

			// 3. Partition current level into active and skipped blocks
			var activeInLevel []flow.Block
			var skippedInLevel []flow.Block
			for _, block := range level {
				if activeBlocks[block.ID] {
					activeInLevel = append(activeInLevel, block)
				} else {
					skippedInLevel = append(skippedInLevel, block)
				}
			}

			// 4. Write default values for skipped blocks to session state
			for _, block := range skippedInLevel {
				if block.OutputKey != "" && block.SkipOutput != "" {
					_ = ctx.Session().State().Set(block.OutputKey, block.SkipOutput)
				}
			}

			// 5. Execute active blocks in this level
			switch len(activeInLevel) {
			case 0:
				// All blocks in this level were pruned, continue to next level
				continue

			case 1:
				// Single active block — execute directly
				ag := resolveFlowAgent(activeInLevel[0], agentMap)
				for event, err := range ag.Run(ctx) {
					if !yield(event, err) {
						return
					}
				}

			default:
				// Multiple active blocks — execute in parallel
				for event, err := range runLevelParallel(ctx, activeInLevel, agentMap) {
					if !yield(event, err) {
						return
					}
				}
			}
		}
	}
}

// runLevelParallel runs multiple active blocks concurrently using
// ADK's ParallelAgent for proper branch isolation and errgroup
// concurrency.
func runLevelParallel(
	ctx adkagent.InvocationContext,
	blocks []flow.Block,
	agentMap map[string]adkagent.Agent,
) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		var subAgents []adkagent.Agent
		for _, block := range blocks {
			subAgents = append(subAgents, resolveFlowAgent(block, agentMap))
		}

		parallelName := fmt.Sprintf("Level_Parallel_%s", blocks[0].ID)
		parallelCfg := agent.Config{
			Config: adkagent.Config{
				Name:      parallelName,
				SubAgents: subAgents,
			},
			DisableDefaultCallbacks: true,
		}
		parallelAgent, err := agent.NewParallelAgent(parallelCfg)
		if err != nil {
			yield(nil, fmt.Errorf("orchestration/flow/executor: create parallel agent: %w", err))
			return
		}

		for event, err := range parallelAgent.Run(ctx) {
			if !yield(event, err) {
				return
			}
		}
	}
}

// resolveFlowAgent returns the adkagent.Agent for a block.
// For agent-type blocks, it looks up the pre-resolved agent from agentMap.
// For start/end blocks, it creates a passthrough agent.
func resolveFlowAgent(block flow.Block, agentMap map[string]adkagent.Agent) adkagent.Agent {
	switch block.Type {
	case flow.BlockTypeAgent:
		if ag, ok := agentMap[block.ID]; ok {
			return ag
		}
		// Fallback: create a passthrough agent
		ag, _ := adkagent.New(adkagent.Config{Name: block.Name})
		return ag
	case flow.BlockTypeStart, flow.BlockTypeEnd:
		ag, _ := adkagent.New(adkagent.Config{Name: block.Name})
		return ag
	default:
		ag, _ := adkagent.New(adkagent.Config{Name: block.Name})
		return ag
	}
}

// sessionStateEvaluator bridges session.State to the ConditionEvaluator
// interface used by the DAG layer.
type sessionStateEvaluator struct {
	state session.State
}

// IsActive evaluates a condition by reading the corresponding key from
// session state. Returns true when the value is truthy (bool true, or
// string not in {"false", "no", "0"}). Returns false when the key is
// missing, the value is bool false, or the value is a falsy string.
func (e *sessionStateEvaluator) IsActive(stateKey string) bool {
	val, err := e.state.Get(stateKey)
	if err != nil {
		// Key not found in state — default to not active
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return !(v == "false" || v == "no" || v == "0")
	default:
		return false
	}
}

// compile-time check that sessionStateEvaluator implements ConditionEvaluator
var _ ConditionEvaluator = (*sessionStateEvaluator)(nil)
