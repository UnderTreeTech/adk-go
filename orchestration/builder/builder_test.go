package builder

import (
	"context"
	"iter"
	"testing"

	"github.com/UnderTreeTech/adk-go/orchestration"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	adkagent "google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

// mockLLM is a minimal mock model.LLM for testing.
type mockLLM struct {
	name string
}

func (m *mockLLM) Name() string { return m.name }
func (m *mockLLM) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(&adkmodel.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "mock response"}}}}, nil)
	}
}

func TestBuildSequentialAgent(t *testing.T) {
	modelReg := registry.NewModelRegistry()
	mockModel := &mockLLM{name: "test-model"}
	if err := modelReg.Register("m1", mockModel); err != nil {
		t.Fatalf("Register model: %v", err)
	}

	callbackReg := registry.NewCallbackRegistry()

	b := New(BuilderConfig{
		ModelRegistry:    modelReg,
		ToolRegistry:     registry.NewToolRegistry(),
		CallbackRegistry: callbackReg,
	})

	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "TestSequential"},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentTypeSequential,
			Name: "Pipeline",
			Children: []orchestration.AgentNode{
				{Type: orchestration.AgentTypeLLM, Name: "Step1", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Do step 1", OutputKey: "s1"},
				{Type: orchestration.AgentTypeLLM, Name: "Step2", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Do step 2 with {s1}", OutputKey: "s2"},
			},
		},
	}

	ag, err := b.Build(schema)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	if ag.Name() != "Pipeline" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "Pipeline")
	}
	if len(ag.SubAgents()) != 2 {
		t.Errorf("len(SubAgents) = %d, want 2", len(ag.SubAgents()))
	}
	if ag.SubAgents()[0].Name() != "Step1" {
		t.Errorf("SubAgents[0].Name() = %q, want %q", ag.SubAgents()[0].Name(), "Step1")
	}
	if ag.SubAgents()[1].Name() != "Step2" {
		t.Errorf("SubAgents[1].Name() = %q, want %q", ag.SubAgents()[1].Name(), "Step2")
	}
}

func TestBuildParallelAgent(t *testing.T) {
	modelReg := registry.NewModelRegistry()
	mockModel := &mockLLM{name: "test-model"}
	modelReg.Register("m1", mockModel)

	b := New(BuilderConfig{
		ModelRegistry:    modelReg,
		ToolRegistry:     registry.NewToolRegistry(),
		CallbackRegistry: registry.NewCallbackRegistry(),
	})

	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "TestParallel"},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentTypeParallel,
			Name: "ParallelBranch",
			Children: []orchestration.AgentNode{
				{Type: orchestration.AgentTypeLLM, Name: "BranchA", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Branch A", OutputKey: "a"},
				{Type: orchestration.AgentTypeLLM, Name: "BranchB", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Branch B", OutputKey: "b"},
			},
		},
	}

	ag, err := b.Build(schema)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	if ag.Name() != "ParallelBranch" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "ParallelBranch")
	}
	if len(ag.SubAgents()) != 2 {
		t.Errorf("len(SubAgents) = %d, want 2", len(ag.SubAgents()))
	}
}

func TestBuildLoopAgent(t *testing.T) {
	modelReg := registry.NewModelRegistry()
	mockModel := &mockLLM{name: "test-model"}
	modelReg.Register("m1", mockModel)

	b := New(BuilderConfig{
		ModelRegistry:    modelReg,
		ToolRegistry:     registry.NewToolRegistry(),
		CallbackRegistry: registry.NewCallbackRegistry(),
	})

	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "TestLoop"},
		Agent: orchestration.AgentNode{
			Type:          orchestration.AgentTypeLoop,
			Name:          "RefineLoop",
			MaxIterations: 3,
			Children: []orchestration.AgentNode{
				{Type: orchestration.AgentTypeLLM, Name: "Writer", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Write"},
				{Type: orchestration.AgentTypeLLM, Name: "Reviewer", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Review"},
			},
		},
	}

	ag, err := b.Build(schema)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	if ag.Name() != "RefineLoop" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "RefineLoop")
	}
}

func TestBuildNestedSequentialWithParallel(t *testing.T) {
	modelReg := registry.NewModelRegistry()
	mockModel := &mockLLM{name: "test-model"}
	modelReg.Register("m1", mockModel)

	b := New(BuilderConfig{
		ModelRegistry:    modelReg,
		ToolRegistry:     registry.NewToolRegistry(),
		CallbackRegistry: registry.NewCallbackRegistry(),
	})

	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "TestNested"},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentTypeSequential,
			Name: "OrderPipeline",
			Children: []orchestration.AgentNode{
				{Type: orchestration.AgentTypeLLM, Name: "Classify", Model: &orchestration.ModelReference{Ref: "m1"}, OutputKey: "cls"},
				{
					Type: orchestration.AgentTypeParallel,
					Name: "ParallelStep",
					Children: []orchestration.AgentNode{
						{Type: orchestration.AgentTypeLLM, Name: "Payment", Model: &orchestration.ModelReference{Ref: "m1"}, OutputKey: "pay"},
						{Type: orchestration.AgentTypeLLM, Name: "Risk", Model: &orchestration.ModelReference{Ref: "m1"}, OutputKey: "risk"},
					},
				},
				{Type: orchestration.AgentTypeLLM, Name: "Merge", Model: &orchestration.ModelReference{Ref: "m1"}, OutputKey: "final"},
			},
		},
	}

	ag, err := b.Build(schema)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	if ag.Name() != "OrderPipeline" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "OrderPipeline")
	}
	if len(ag.SubAgents()) != 3 {
		t.Errorf("len(SubAgents) = %d, want 3", len(ag.SubAgents()))
	}

	// The middle child should be the parallel agent
	parallel := ag.SubAgents()[1]
	if parallel.Name() != "ParallelStep" {
		t.Errorf("SubAgents[1].Name() = %q, want %q", parallel.Name(), "ParallelStep")
	}
	if len(parallel.SubAgents()) != 2 {
		t.Errorf("len(ParallelStep.SubAgents) = %d, want 2", len(parallel.SubAgents()))
	}
}

func TestBuildWithCallbacks(t *testing.T) {
	modelReg := registry.NewModelRegistry()
	mockModel := &mockLLM{name: "test-model"}
	modelReg.Register("m1", mockModel)

	callbackReg := registry.NewCallbackRegistry()

	// Register a beforeAgent callback (mock that does nothing)
	mockBeforeCallback := func(ctx adkagent.CallbackContext) (*genai.Content, error) {
		return nil, nil
	}
	callbackReg.RegisterBeforeAgent("cb1", mockBeforeCallback)

	b := New(BuilderConfig{
		ModelRegistry:    modelReg,
		ToolRegistry:     registry.NewToolRegistry(),
		CallbackRegistry: callbackReg,
	})

	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "TestCallback"},
		Agent: orchestration.AgentNode{
			Type:        orchestration.AgentTypeLLM,
			Name:        "WithCallback",
			Model:       &orchestration.ModelReference{Ref: "m1"},
			Instruction: "Test",
			Callbacks: orchestration.AgentCallbacks{
				BeforeAgent: []orchestration.CallbackReference{{Ref: "cb1"}},
			},
		},
	}

	ag, err := b.Build(schema)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	if ag.Name() != "WithCallback" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "WithCallback")
	}
}

func TestBuildUnknownAgentType(t *testing.T) {
	b := New(BuilderConfig{
		ModelRegistry:    registry.NewModelRegistry(),
		ToolRegistry:     registry.NewToolRegistry(),
		CallbackRegistry: registry.NewCallbackRegistry(),
	})

	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "TestBadType"},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentType("unknown"),
			Name: "BadAgent",
		},
	}

	_, err := b.Build(schema)
	if err == nil {
		t.Fatal("Build() should fail for unknown agent type")
	}
}

func TestBuildMissingModelRef(t *testing.T) {
	b := New(BuilderConfig{
		ModelRegistry:    registry.NewModelRegistry(), // empty
		ToolRegistry:     registry.NewToolRegistry(),
		CallbackRegistry: registry.NewCallbackRegistry(),
	})

	schema := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "TestNoModel"},
		Agent: orchestration.AgentNode{
			Type:  orchestration.AgentTypeLLM,
			Name:  "NoModel",
			Model: &orchestration.ModelReference{Ref: "nonexistent"},
		},
	}

	_, err := b.Build(schema)
	if err == nil {
		t.Fatal("Build() should fail for missing model ref")
	}
}
