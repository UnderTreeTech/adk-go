// Package executor provides the DAG topology engine and agent tree builder
// for the v2 flow orchestration system.
//
// The executor converts a validated FlowSchema (blocks + edges) into a
// topologically ordered execution plan and then builds an adk-go agent
// tree (SequentialAgent containing level groups of ParallelAgents).
package executor

import (
	"fmt"
	"sort"

	"github.com/UnderTreeTech/adk-go/orchestration/flow"
)

// DAG represents a validated directed acyclic graph of flow blocks and edges.
// It provides topology analysis: level computation, cycle detection,
// topological sorting, and upstream/downstream queries.
type DAG struct {
	blocks    []flow.Block
	edges     []flow.Edge
	blockMap  map[string]*flow.Block // ID → Block
	adjacency map[string][]string    // sourceID → []targetID
	reverse   map[string][]string    // targetID → []sourceID
	inDegree  map[string]int         // blockID → number of incoming edges
	levels    map[string]int         // blockID → topological level (computed)
}

// NewDAG constructs and validates a DAG from blocks and edges.
// Returns error if:
//   - Edge references a non-existent block ID
//   - The graph contains a cycle
//   - A self-loop is detected
func NewDAG(blocks []flow.Block, edges []flow.Edge) (*DAG, error) {
	d := &DAG{
		blocks:    blocks,
		edges:     edges,
		blockMap:  make(map[string]*flow.Block),
		adjacency: make(map[string][]string),
		reverse:   make(map[string][]string),
		inDegree:  make(map[string]int),
		levels:    make(map[string]int),
	}

	// Index blocks
	for i := range blocks {
		id := blocks[i].ID
		if _, exists := d.blockMap[id]; exists {
			return nil, fmt.Errorf("orchestration/flow/executor: duplicate block ID %q", id)
		}
		d.blockMap[id] = &blocks[i]
		d.inDegree[id] = 0
	}

	// Build adjacency lists and check edge references
	for _, edge := range edges {
		if _, exists := d.blockMap[edge.SourceID]; !exists {
			return nil, fmt.Errorf("orchestration/flow/executor: edge source %q not found in blocks", edge.SourceID)
		}
		if _, exists := d.blockMap[edge.TargetID]; !exists {
			return nil, fmt.Errorf("orchestration/flow/executor: edge target %q not found in blocks", edge.TargetID)
		}

		// Self-loop check
		if edge.SourceID == edge.TargetID {
			return nil, fmt.Errorf("orchestration/flow/executor: self-loop detected on block %q", edge.SourceID)
		}

		d.adjacency[edge.SourceID] = append(d.adjacency[edge.SourceID], edge.TargetID)
		d.reverse[edge.TargetID] = append(d.reverse[edge.TargetID], edge.SourceID)
		d.inDegree[edge.TargetID]++
	}

	// Compute levels using Kahn's algorithm (BFS topological sort)
	if err := d.computeLevels(); err != nil {
		return nil, err
	}

	return d, nil
}

// computeLevels uses Kahn's algorithm to compute topological levels.
// Level 0 contains blocks with no incoming edges. Blocks at the same
// level can execute in parallel. Returns error if a cycle is detected.
func (d *DAG) computeLevels() error {
	// Copy inDegrees since we'll modify them
	inDeg := make(map[string]int)
	for id, deg := range d.inDegree {
		inDeg[id] = deg
	}

	// Initialize queue with all blocks that have inDegree 0
	var queue []string
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue) // Deterministic ordering for reproducibility

	level := 0
	processedCount := 0

	for len(queue) > 0 {
		// All blocks in the current queue are at the same level
		var nextQueue []string
		for _, id := range queue {
			d.levels[id] = level
			processedCount++

			// Decrement inDegree of downstream blocks
			for _, downstream := range d.adjacency[id] {
				inDeg[downstream]--
				if inDeg[downstream] == 0 {
					nextQueue = append(nextQueue, downstream)
				}
			}
		}

		sort.Strings(nextQueue) // Deterministic ordering
		queue = nextQueue
		level++
	}

	// If not all blocks are processed, there's a cycle
	if processedCount != len(d.blocks) {
		// Find the blocks involved in the cycle
		var cycleBlocks []string
		for id, deg := range inDeg {
			if deg > 0 {
				cycleBlocks = append(cycleBlocks, id)
			}
		}
		sort.Strings(cycleBlocks)
		return fmt.Errorf("orchestration/flow/executor: cycle detected involving blocks %v", cycleBlocks)
	}

	return nil
}

// Levels returns blocks grouped by topological level, sorted ascending.
// Blocks at the same level have no dependency between them and can
// execute in parallel. Level 0 blocks have no incoming edges.
//
// Within each level, blocks are sorted by ID for deterministic ordering.
func (d *DAG) Levels() [][]flow.Block {
	maxLevel := -1
	for _, l := range d.levels {
		if l > maxLevel {
			maxLevel = l
		}
	}

	result := make([][]flow.Block, maxLevel+1)
	for id, level := range d.levels {
		block := d.blockMap[id]
		result[level] = append(result[level], *block)
	}

	// Sort blocks within each level by ID for deterministic ordering
	for i := range result {
		sort.Slice(result[i], func(a, b int) bool {
			return result[i][a].ID < result[i][b].ID
		})
	}

	return result
}

// TopologicalSort returns all block IDs in a valid topological order.
// Blocks at the same level are sorted by ID.
func (d *DAG) TopologicalSort() []string {
	levels := d.Levels()
	var result []string
	for _, level := range levels {
		for _, block := range level {
			result = append(result, block.ID)
		}
	}
	return result
}

// InDegree returns the number of incoming edges for a block.
// Returns -1 if the block ID does not exist.
func (d *DAG) InDegree(blockID string) int {
	if deg, ok := d.inDegree[blockID]; ok {
		return deg
	}
	return -1
}

// Downstream returns block IDs that are direct targets of the given block.
// Returns nil if the block has no downstream blocks or does not exist.
func (d *DAG) Downstream(blockID string) []string {
	targets, ok := d.adjacency[blockID]
	if !ok {
		return nil
	}
	result := make([]string, len(targets))
	copy(result, targets)
	sort.Strings(result)
	return result
}

// Upstream returns block IDs that are direct sources of the given block.
// Returns nil if the block has no upstream blocks or does not exist.
func (d *DAG) Upstream(blockID string) []string {
	sources, ok := d.reverse[blockID]
	if !ok {
		return nil
	}
	result := make([]string, len(sources))
	copy(result, sources)
	sort.Strings(result)
	return result
}

// Block returns the block with the given ID, or nil if not found.
func (d *DAG) Block(blockID string) *flow.Block {
	return d.blockMap[blockID]
}

// LevelOf returns the topological level of the given block.
// Returns -1 if the block does not exist.
func (d *DAG) LevelOf(blockID string) int {
	if level, ok := d.levels[blockID]; ok {
		return level
	}
	return -1
}
