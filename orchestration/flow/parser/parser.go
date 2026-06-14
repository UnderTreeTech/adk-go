// Package parser parses, validates, and normalizes v2 flow schema JSON.
//
// Usage:
//
//	schema, err := parser.Parse(jsonBytes)
//	if err != nil {
//	    // err contains all validation errors with JSON paths
//	}
package parser

import (
	"encoding/json"
	"fmt"

	"github.com/UnderTreeTech/adk-go/orchestration/flow"
)

// Parse unmarshals JSON bytes into a FlowSchema, then validates
// and normalizes it. Returns all validation errors aggregated with JSON paths.
func Parse(data []byte) (*flow.FlowSchema, error) {
	var schema flow.FlowSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("orchestration/flow/parser: unmarshal JSON: %w", err)
	}

	if err := Validate(&schema); err != nil {
		return nil, err
	}

	if err := Normalize(&schema); err != nil {
		return nil, err
	}

	return &schema, nil
}

// Validate checks a FlowSchema for structural correctness without modifying it.
// Returns all errors aggregated (not just the first), each with a JSON path.
//
// Validation rules:
//  1. version must be "2"
//  2. metadata.name must be non-empty
//  3. blocks must be non-empty
//  4. block IDs must be unique
//  5. block names must be non-empty (for agent blocks)
//  6. block names must not be "user" (ADK reserved)
//  7. edge sourceId/targetId must reference existing blocks
//  8. no duplicate edges
//  9. no self-loops
// 10. edges must not create cycles (DAG validation)
func Validate(schema *flow.FlowSchema) error {
	var errs multiError

	// Version check
	if schema.Version != flow.FlowSchemaVersion {
		errs.addf("version", "must be %q, got %q", flow.FlowSchemaVersion, schema.Version)
	}

	// Metadata
	if schema.Metadata.Name == "" {
		errs.add("metadata.name", "must be non-empty")
	}

	// Blocks
	if len(schema.Blocks) == 0 {
		errs.add("blocks", "must be non-empty")
	}

	// Validate blocks
	blockIDs := make(map[string]int) // ID → first index
	for i, block := range schema.Blocks {
		path := fmt.Sprintf("blocks[%d]", i)

		if block.ID == "" {
			errs.add(path+".id", "must be non-empty")
		} else if firstIdx, exists := blockIDs[block.ID]; exists {
			errs.addf(path+".id", "duplicate block ID %q (first declared at index %d)", block.ID, firstIdx)
		} else {
			blockIDs[block.ID] = i
		}

		if block.Name == "" {
			errs.add(path+".name", "must be non-empty")
		} else if block.Name == "user" {
			errs.addf(path+".name", "block name %q is reserved by ADK", block.Name)
		}

		if block.Type == "" {
			errs.add(path+".type", "must be non-empty")
		} else if !isValidBlockType(block.Type) {
			errs.addf(path+".type", "invalid block type %q, must be one of %v", block.Type, flow.ValidBlockTypes())
		}
	}

	// Validate edges
	edgeSet := make(map[string]bool) // "source→target" for duplicate detection
	for i, edge := range schema.Edges {
		path := fmt.Sprintf("edges[%d]", i)

		if edge.SourceID == "" {
			errs.add(path+".sourceId", "must be non-empty")
		} else if _, exists := blockIDs[edge.SourceID]; !exists {
			errs.addf(path+".sourceId", "source block %q not found in blocks", edge.SourceID)
		}

		if edge.TargetID == "" {
			errs.add(path+".targetId", "must be non-empty")
		} else if _, exists := blockIDs[edge.TargetID]; !exists {
			errs.addf(path+".targetId", "target block %q not found in blocks", edge.TargetID)
		}

		// Self-loop check
		if edge.SourceID == edge.TargetID && edge.SourceID != "" {
			errs.addf(path, "self-loop on block %q", edge.SourceID)
		}

		// Duplicate edge check
		key := edge.SourceID + "→" + edge.TargetID
		if edgeSet[key] {
			errs.addf(path, "duplicate edge %s", key)
		} else {
			edgeSet[key] = true
		}
	}

	// DAG cycle detection (only if no prior errors that would make it meaningless)
	if len(errs) == 0 {
		if cycleErr := detectCycle(schema.Blocks, schema.Edges); cycleErr != nil {
			errs.add("edges", cycleErr.Error())
		}
	}

	// OutputKey uniqueness check
	outputKeys := make(map[string]string) // outputKey → blockID
	for _, block := range schema.Blocks {
		if block.OutputKey != "" {
			if existingID, exists := outputKeys[block.OutputKey]; exists {
				errs.addf("blocks", "duplicate outputKey %q on blocks %q and %q", block.OutputKey, existingID, block.ID)
			} else {
				outputKeys[block.OutputKey] = block.ID
			}
		}
	}

	if len(errs) > 0 {
		return &errs
	}
	return nil
}

// detectCycle checks if the graph contains a cycle using Kahn's algorithm.
// Returns nil if no cycle is detected.
func detectCycle(blocks []flow.Block, edges []flow.Edge) error {
	inDegree := make(map[string]int)
	adjacency := make(map[string][]string)

	for _, block := range blocks {
		inDegree[block.ID] = 0
	}
	for _, edge := range edges {
		adjacency[edge.SourceID] = append(adjacency[edge.SourceID], edge.TargetID)
		inDegree[edge.TargetID]++
	}

	queue := make([]string, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	processedCount := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		processedCount++

		for _, downstream := range adjacency[id] {
			inDegree[downstream]--
			if inDegree[downstream] == 0 {
				queue = append(queue, downstream)
			}
		}
	}

	if processedCount != len(blocks) {
		var cycleBlocks []string
		for id, deg := range inDegree {
			if deg > 0 {
				cycleBlocks = append(cycleBlocks, id)
			}
		}
		return fmt.Errorf("cycle detected involving blocks %v", cycleBlocks)
	}

	return nil
}

// Normalize applies defaults and normalizes names. Must be called after
// Validate succeeds.
func Normalize(schema *flow.FlowSchema) error {
	if schema.Version == "" {
		schema.Version = flow.FlowSchemaVersion
	}

	for i := range schema.Blocks {
		block := &schema.Blocks[i]
		block.ID = trimSpace(block.ID)
		block.Name = trimSpace(block.Name)
		block.Description = trimSpace(block.Description)
		block.OutputKey = trimSpace(block.OutputKey)

		if block.Description == "" {
			block.Description = block.Name
		}
	}

	return nil
}

// ---- Helpers ----

func isValidBlockType(t flow.BlockType) bool {
	for _, valid := range flow.ValidBlockTypes() {
		if t == valid {
			return true
		}
	}
	return false
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// ---- Error types ----

// validationError represents a single validation failure at a JSON path.
type validationError struct {
	Path    string
	Message string
}

func (e *validationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// multiError aggregates multiple validation errors.
type multiError []*validationError

func (me *multiError) add(path, message string) {
	*me = append(*me, &validationError{
		Path:    path,
		Message: message,
	})
}

func (me *multiError) addf(path, format string, args ...any) {
	*me = append(*me, &validationError{
		Path:    path,
		Message: fmt.Sprintf(format, args...),
	})
}

func (me multiError) Error() string {
	if len(me) == 0 {
		return ""
	}
	result := me[0].Error()
	for _, e := range me[1:] {
		result += "; " + e.Error()
	}
	return result
}
