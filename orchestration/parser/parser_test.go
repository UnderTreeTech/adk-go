package parser

import (
	"testing"

	"github.com/UnderTreeTech/adk-go/orchestration"
)

func TestParseValidSequential(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "TestPipeline"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}]
  },
  "agent": {
    "type": "sequential",
    "name": "Pipeline",
    "children": [
      {"type": "llm", "name": "Step1", "model": {"ref": "m1"}, "instruction": "Do step 1", "outputKey": "s1"},
      {"type": "llm", "name": "Step2", "model": {"ref": "m1"}, "instruction": "Do step 2 with {s1}", "outputKey": "s2"}
    ]
  }
}`

	schema, err := Parse([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if schema.Version != "1" {
		t.Errorf("Version = %q, want %q", schema.Version, "1")
	}
	if schema.Agent.Type != orchestration.AgentTypeSequential {
		t.Errorf("Agent.Type = %q, want %q", schema.Agent.Type, orchestration.AgentTypeSequential)
	}
	if len(schema.Agent.Children) != 2 {
		t.Errorf("len(Children) = %d, want 2", len(schema.Agent.Children))
	}
}

func TestParseValidParallelConditional(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "OrderPipeline"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}],
    "callbacks": [{"ref": "cb1", "provider": "conditional_skip", "config": {"conditionKey": "needs_check"}}]
  },
  "agent": {
    "type": "sequential",
    "name": "OrderPipeline",
    "children": [
      {"type": "llm", "name": "Classify", "model": {"ref": "m1"}, "outputKey": "cls"},
      {
        "type": "parallel",
        "name": "ParallelStep",
        "children": [
          {"type": "llm", "name": "Payment", "model": {"ref": "m1"}, "outputKey": "pay"},
          {"type": "llm", "name": "Risk", "model": {"ref": "m1"}, "outputKey": "risk",
           "callbacks": {"beforeAgent": [{"ref": "cb1"}]}}
        ]
      },
      {"type": "llm", "name": "Merge", "model": {"ref": "m1"}, "outputKey": "final"}
    ]
  }
}`

	schema, err := Parse([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(schema.Agent.Children) != 3 {
		t.Errorf("len(Children) = %d, want 3", len(schema.Agent.Children))
	}
	parallel := schema.Agent.Children[1]
	if parallel.Type != orchestration.AgentTypeParallel {
		t.Errorf("Children[1].Type = %q, want %q", parallel.Type, orchestration.AgentTypeParallel)
	}
}

func TestParseValidLoop(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "LoopTest"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}]
  },
  "agent": {
    "type": "loop",
    "name": "RefineLoop",
    "maxIterations": 3,
    "children": [
      {"type": "llm", "name": "Writer", "model": {"ref": "m1"}},
      {"type": "llm", "name": "Reviewer", "model": {"ref": "m1"}}
    ]
  }
}`

	schema, err := Parse([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if schema.Agent.Type != orchestration.AgentTypeLoop {
		t.Errorf("Agent.Type = %q, want %q", schema.Agent.Type, orchestration.AgentTypeLoop)
	}
	if schema.Agent.MaxIterations != 3 {
		t.Errorf("MaxIterations = %d, want 3", schema.Agent.MaxIterations)
	}
}

func TestParseInvalidVersion(t *testing.T) {
	jsonStr := `{
  "version": "99",
  "metadata": {"name": "Test"},
  "registries": {},
  "agent": {"type": "llm", "name": "A", "model": {"ref": "m1"}}
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for invalid version")
	}
	if !containsStr(err.Error(), "version") {
		t.Errorf("error should mention 'version', got: %v", err)
	}
}

func TestParseMissingMetadataName(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {},
  "registries": {},
  "agent": {"type": "llm", "name": "A", "model": {"ref": "m1"}}
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for missing metadata.name")
	}
}

func TestParseDuplicateAgentName(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}]
  },
  "agent": {
    "type": "sequential",
    "name": "Pipeline",
    "children": [
      {"type": "llm", "name": "Step", "model": {"ref": "m1"}},
      {"type": "llm", "name": "Step", "model": {"ref": "m1"}}
    ]
  }
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for duplicate agent name")
	}
	if !containsStr(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
}

func TestParseReservedAgentName(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}]
  },
  "agent": {"type": "llm", "name": "user", "model": {"ref": "m1"}}
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for reserved name 'user'")
	}
}

func TestParseMissingModelRef(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}]
  },
  "agent": {"type": "llm", "name": "A", "model": {"ref": "nonexistent"}}
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for unresolved model ref")
	}
}

func TestParseEmptyWorkflowChildren(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "registries": {},
  "agent": {"type": "sequential", "name": "Pipeline", "children": []}
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for empty workflow children")
	}
}

func TestParseDuplicateRegistryRef(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "registries": {
    "models": [
      {"ref": "m1", "provider": "openai", "config": {"modelName": "a"}},
      {"ref": "m1", "provider": "openai", "config": {"modelName": "b"}}
    ]
  },
  "agent": {"type": "llm", "name": "A", "model": {"ref": "m1"}}
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for duplicate model ref")
	}
}

func TestParseUnresolvedToolRef(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}]
  },
  "agent": {
    "type": "llm", "name": "A", "model": {"ref": "m1"},
    "tools": [{"ref": "nonexistent_tool"}]
  }
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for unresolved tool ref")
	}
}

func TestParseUnresolvedCallbackRef(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "registries": {
    "models": [{"ref": "m1", "provider": "openai", "config": {"modelName": "test"}}]
  },
  "agent": {
    "type": "llm", "name": "A", "model": {"ref": "m1"},
    "callbacks": {"beforeAgent": [{"ref": "nonexistent_cb"}]}
  }
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for unresolved callback ref")
	}
}

func TestNormalizeDefaultsDescription(t *testing.T) {
	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "Test"},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentTypeLLM,
			Name: "MyAgent",
		},
	}

	if err := Normalize(schema); err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}
	if schema.Agent.Description != "MyAgent" {
		t.Errorf("Agent.Description = %q, want %q (should default to Name)", schema.Agent.Description, "MyAgent")
	}
}

func TestNormalizeTrimsWhitespace(t *testing.T) {
	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "Test"},
		Agent: orchestration.AgentNode{
			Type:        orchestration.AgentTypeLLM,
			Name:        "  MyAgent  ",
			Instruction: "  Do something  ",
		},
	}

	if err := Normalize(schema); err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}
	if schema.Agent.Name != "MyAgent" {
		t.Errorf("Agent.Name = %q, want %q", schema.Agent.Name, "MyAgent")
	}
	if schema.Agent.Instruction != "Do something" {
		t.Errorf("Agent.Instruction = %q, want %q", schema.Agent.Instruction, "Do something")
	}
}

// helper
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && findSubstring(s, sub)))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
