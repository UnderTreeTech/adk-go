// Package parser parses, validates, and normalizes orchestration schema JSON.
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

	"github.com/UnderTreeTech/adk-go/orchestration"
)

// Parse unmarshals JSON bytes into an OrchestrationSchema, then validates
// and normalizes it. Returns all validation errors aggregated with JSON paths.
func Parse(data []byte) (*orchestration.OrchestrationSchema, error) {
	var schema orchestration.OrchestrationSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parser: unmarshal JSON: %w", err)
	}

	if err := Validate(&schema); err != nil {
		return nil, err
	}

	if err := Normalize(&schema); err != nil {
		return nil, err
	}

	return &schema, nil
}

// Validate checks a schema for structural correctness without modifying it.
// Returns all errors aggregated (not just the first), each with a JSON path.
func Validate(schema *orchestration.OrchestrationSchema) error {
	var errs multiError

	// Version check
	if schema.Version != orchestration.SchemaVersion {
		errs.add("version", "must be %q, got %q", orchestration.SchemaVersion, schema.Version)
	}

	// Metadata
	if schema.Metadata.Name == "" {
		errs.add("metadata.name", "must be non-empty")
	}

	// Agent root
	if schema.Agent.Type == "" {
		errs.add("agent.type", "must be non-empty")
	} else if !isValidAgentType(schema.Agent.Type) {
		errs.add("agent.type", "invalid agent type %q, must be one of %v",
			schema.Agent.Type, orchestration.ValidAgentTypes())
	}

	if schema.Agent.Name == "" {
		errs.add("agent.name", "must be non-empty")
	}

	// Validate agent tree recursively
	validateAgentNode(&errs, schema.Agent, "agent", schema)

	// Validate registries
	validateRegistries(&errs, schema)

	if len(errs) > 0 {
		return &errs
	}
	return nil
}

// Normalize applies defaults and normalizes names. Must be called after
// Validate succeeds.
func Normalize(schema *orchestration.OrchestrationSchema) error {
	if schema.Version == "" {
		schema.Version = orchestration.SchemaVersion
	}

	// Normalize agent tree recursively
	normalizeAgentNode(&schema.Agent)

	return nil
}

// normalizeAgentNode trims whitespace and applies defaults recursively.
func normalizeAgentNode(node *orchestration.AgentNode) {
	node.Name = trimSpace(node.Name)
	node.Description = trimSpace(node.Description)
	node.Instruction = trimSpace(node.Instruction)
	node.GlobalInstruction = trimSpace(node.GlobalInstruction)
	node.OutputKey = trimSpace(node.OutputKey)

	if node.Description == "" {
		node.Description = node.Name
	}

	for i := range node.Children {
		normalizeAgentNode(&node.Children[i])
	}
}

func trimSpace(s string) string {
	// Simple whitespace trim - doesn't strip internal whitespace
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// ---- Validation helpers ----

func isValidAgentType(t orchestration.AgentType) bool {
	for _, valid := range orchestration.ValidAgentTypes() {
		if t == valid {
			return true
		}
	}
	return false
}

// validateAgentNode recursively validates an agent node and its children.
func validateAgentNode(errs *multiError, node orchestration.AgentNode, path string, schema *orchestration.OrchestrationSchema) {
	// Name uniqueness is checked at the tree level via validateNameUniqueness
	// Name "user" is reserved by ADK
	if node.Name == "user" {
		errs.add(path+".name", "agent name %q is reserved by ADK", node.Name)
	}

	switch node.Type {
	case orchestration.AgentTypeLLM:
		validateLLMAgent(errs, node, path, schema)

	case orchestration.AgentTypeSequential, orchestration.AgentTypeParallel, orchestration.AgentTypeLoop:
		if len(node.Children) == 0 {
			errs.add(path+".children", "%s agent must have at least one child", node.Type)
		}
		for i, child := range node.Children {
			childPath := fmt.Sprintf("%s.children[%d]", path, i)
			if child.Type == "" {
				errs.add(childPath+".type", "must be non-empty")
			} else if !isValidAgentType(child.Type) {
				errs.add(childPath+".type", "invalid agent type %q", child.Type)
			}
			if child.Name == "" {
				errs.add(childPath+".name", "must be non-empty")
			}
			validateAgentNode(errs, child, childPath, schema)
		}
	}
}

// validateLLMAgent validates LLM agent-specific constraints.
func validateLLMAgent(errs *multiError, node orchestration.AgentNode, path string, schema *orchestration.OrchestrationSchema) {
	// Model reference is required for LLM agents
	if node.Model == nil || node.Model.Ref == "" {
		errs.add(path+".model.ref", "LLM agent must reference a model")
	} else if !refExistsInModels(node.Model.Ref, schema.Registries.Models) {
		errs.add(path+".model.ref", "model reference %q not found in registries.models", node.Model.Ref)
	}

	// Tool references must resolve
	for i, toolRef := range node.Tools {
		toolPath := fmt.Sprintf("%s.tools[%d].ref", path, i)
		if toolRef.Ref == "" {
			errs.add(toolPath, "tool reference must be non-empty")
		} else if !refExistsInTools(toolRef.Ref, schema.Registries.Tools) {
			errs.add(toolPath, "tool reference %q not found in registries.tools", toolRef.Ref)
		}
	}

	// Callback references must resolve
	for i, cbRef := range node.Callbacks.BeforeAgent {
		cbPath := fmt.Sprintf("%s.callbacks.beforeAgent[%d].ref", path, i)
		if cbRef.Ref == "" {
			errs.add(cbPath, "callback reference must be non-empty")
		} else if !refExistsInCallbacks(cbRef.Ref, schema.Registries.Callbacks) {
			errs.add(cbPath, "callback reference %q not found in registries.callbacks", cbRef.Ref)
		}
	}
	for i, cbRef := range node.Callbacks.AfterAgent {
		cbPath := fmt.Sprintf("%s.callbacks.afterAgent[%d].ref", path, i)
		if cbRef.Ref == "" {
			errs.add(cbPath, "callback reference must be non-empty")
		} else if !refExistsInCallbacks(cbRef.Ref, schema.Registries.Callbacks) {
			errs.add(cbPath, "callback reference %q not found in registries.callbacks", cbRef.Ref)
		}
	}
}

// validateRegistries validates registry-level constraints.
func validateRegistries(errs *multiError, schema *orchestration.OrchestrationSchema) {
	// Check unique refs within each registry
	checkUniqueRefs(errs, "registries.services", schema.Registries.Services, func(s orchestration.ServiceRef) string { return s.Ref })
	checkUniqueRefs(errs, "registries.models", schema.Registries.Models, func(m orchestration.ModelRef) string { return m.Ref })
	checkUniqueRefs(errs, "registries.tools", schema.Registries.Tools, func(t orchestration.ToolRef) string { return t.Ref })
	checkUniqueRefs(errs, "registries.callbacks", schema.Registries.Callbacks, func(c orchestration.CallbackRef) string { return c.Ref })

	// Check agent name uniqueness across the entire tree
	names := make(map[string]string) // name → path
	collectNames(errs, schema.Agent, "agent", names)
}

// checkUniqueRefs checks that ref values are unique within a registry slice.
func checkUniqueRefs[T any](errs *multiError, basePath string, items []T, getRef func(T) string) {
	seen := make(map[string]int) // ref → first index
	for i, item := range items {
		ref := getRef(item)
		if ref == "" {
			errs.add(fmt.Sprintf("%s[%d].ref", basePath, i), "ref must be non-empty")
			continue
		}
		if firstIdx, exists := seen[ref]; exists {
			errs.add(fmt.Sprintf("%s[%d].ref", basePath, i), "duplicate ref %q (first declared at index %d)", ref, firstIdx)
		} else {
			seen[ref] = i
		}
	}
}

// collectNames checks for duplicate agent names in the tree.
func collectNames(errs *multiError, node orchestration.AgentNode, path string, names map[string]string) {
	if node.Name == "" {
		return // empty names caught elsewhere
	}
	if existingPath, exists := names[node.Name]; exists {
		errs.add(path+".name", "duplicate agent name %q (first declared at %s)", node.Name, existingPath)
	} else {
		names[node.Name] = path
	}
	for i, child := range node.Children {
		collectNames(errs, child, fmt.Sprintf("%s.children[%d]", path, i), names)
	}
}

// refExistsInModels checks if a ref exists in the models registry.
func refExistsInModels(ref string, models []orchestration.ModelRef) bool {
	for _, m := range models {
		if m.Ref == ref {
			return true
		}
	}
	return false
}

// refExistsInTools checks if a ref exists in the tools registry.
func refExistsInTools(ref string, tools []orchestration.ToolRef) bool {
	for _, t := range tools {
		if t.Ref == ref {
			return true
		}
	}
	return false
}

// refExistsInCallbacks checks if a ref exists in the callbacks registry.
func refExistsInCallbacks(ref string, callbacks []orchestration.CallbackRef) bool {
	for _, c := range callbacks {
		if c.Ref == ref {
			return true
		}
	}
	return false
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

func (me *multiError) add(path, format string, args ...any) {
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
