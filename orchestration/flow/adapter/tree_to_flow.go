// Package adapter provides conversion utilities between v1 tree-based
// OrchestrationSchema and v2 graph-based FlowSchema.
package adapter

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/orchestration"
	"github.com/UnderTreeTech/adk-go/orchestration/flow"
)

// Convert converts a v1 tree-based OrchestrationSchema to a v2
// graph-based FlowSchema.
//
// Algorithm:
//  1. Flatten the tree: extract all LLM agents as blocks
//  2. Determine edges based on tree structure:
//     - Sequential: each child's output flows to the next child
//     - Parallel: no edges between parallel siblings; all see same upstream
//     - Loop: treated as sequential for edge purposes
//  3. Workflow agent nodes (sequential/parallel/loop) are NOT
//     included as blocks — only leaf LLM agents become blocks.
//
// The output FlowSchema uses the same OutputKey mechanism for data flow
// as the original tree schema, so instructions with {key} template
// substitution continue to work.
func Convert(tree *orchestration.OrchestrationSchema) (*flow.FlowSchema, error) {
	if tree == nil {
		return nil, fmt.Errorf("orchestration/flow/adapter: nil schema")
	}

	result := &flow.FlowSchema{
		Version: flow.FlowSchemaVersion,
		Metadata: flow.FlowMetadata{
			Name:        tree.Metadata.Name,
			Description: tree.Metadata.Description,
			Labels:      tree.Metadata.Labels,
		},
	}

	// Convert the agent tree into blocks and edges
	prevBlocks := convertNode(tree.Agent, result, nil)

	_ = prevBlocks // used during recursion

	return result, nil
}

// convertNode recursively converts an AgentNode tree into flow blocks and edges.
// prevBlockIDs contains the block IDs whose outputs are available as inputs.
// Returns the block IDs produced by this node.
func convertNode(node orchestration.AgentNode, schema *flow.FlowSchema, prevBlockIDs []string) []string {
	switch node.Type {
	case orchestration.AgentTypeLLM:
		return convertLLMNode(node, schema, prevBlockIDs)

	case orchestration.AgentTypeSequential:
		return convertSequentialNode(node, schema, prevBlockIDs)

	case orchestration.AgentTypeParallel:
		return convertParallelNode(node, schema, prevBlockIDs)

	case orchestration.AgentTypeLoop:
		return convertLoopNode(node, schema, prevBlockIDs)

	default:
		// Unknown type — skip
		return prevBlockIDs
	}
}

// convertLLMNode converts an LLM agent node into a flow block and adds
// edges from all previous blocks.
func convertLLMNode(node orchestration.AgentNode, schema *flow.FlowSchema, prevBlockIDs []string) []string {
	blockID := node.Name // Use agent name as block ID

	block := flow.Block{
		ID:          blockID,
		Name:        node.Name,
		Type:        flow.BlockTypeAgent,
		Description: node.Description,
		OutputKey:   node.OutputKey,
	}

	// Derive inputKeys from the instruction template references
	// and the previous blocks' outputKeys
	if len(prevBlockIDs) > 0 {
		block.InputKeys = prevBlockIDs
	}

	schema.Blocks = append(schema.Blocks, block)

	// Add edges from all previous blocks to this block
	for _, prevID := range prevBlockIDs {
		schema.Edges = append(schema.Edges, flow.Edge{
			SourceID: prevID,
			TargetID: blockID,
		})
	}

	return []string{blockID}
}

// convertSequentialNode converts a sequential agent node.
// Children execute in order; each child sees outputs from all previous children.
func convertSequentialNode(node orchestration.AgentNode, schema *flow.FlowSchema, prevBlockIDs []string) []string {
	currentPrev := prevBlockIDs

	for _, child := range node.Children {
		childBlocks := convertNode(child, schema, currentPrev)
		currentPrev = childBlocks
	}

	return currentPrev
}

// convertParallelNode converts a parallel agent node.
// All children see the same upstream; their outputs are all available downstream.
func convertParallelNode(node orchestration.AgentNode, schema *flow.FlowSchema, prevBlockIDs []string) []string {
	var allChildBlocks []string

	for _, child := range node.Children {
		childBlocks := convertNode(child, schema, prevBlockIDs)
		allChildBlocks = append(allChildBlocks, childBlocks...)
	}

	return allChildBlocks
}

// convertLoopNode converts a loop agent node.
// Treated as sequential for edge purposes.
func convertLoopNode(node orchestration.AgentNode, schema *flow.FlowSchema, prevBlockIDs []string) []string {
	currentPrev := prevBlockIDs

	for _, child := range node.Children {
		childBlocks := convertNode(child, schema, currentPrev)
		currentPrev = childBlocks
	}

	return currentPrev
}
