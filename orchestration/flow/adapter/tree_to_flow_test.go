package adapter

import (
	"testing"

	"github.com/UnderTreeTech/adk-go/orchestration"
	"github.com/UnderTreeTech/adk-go/orchestration/flow"
)

func TestConvertSequentialPipeline(t *testing.T) {
	tree := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "SequentialPipeline"},
		Registries: orchestration.Registries{
			Models: []orchestration.ModelRef{
				{Ref: "m1", Provider: "openai", Config: orchestration.ModelProviderConfig{ModelName: "test"}},
			},
		},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentTypeSequential,
			Name: "Pipeline",
			Children: []orchestration.AgentNode{
				{Type: orchestration.AgentTypeLLM, Name: "Step1", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Do step 1", OutputKey: "s1"},
				{Type: orchestration.AgentTypeLLM, Name: "Step2", Model: &orchestration.ModelReference{Ref: "m1"}, Instruction: "Do step 2 with {s1}", OutputKey: "s2"},
			},
		},
	}

	result, err := Convert(tree)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if result.Version != "2" {
		t.Errorf("Version = %q, want %q", result.Version, "2")
	}
	if result.Metadata.Name != "SequentialPipeline" {
		t.Errorf("Metadata.Name = %q, want %q", result.Metadata.Name, "SequentialPipeline")
	}
	if len(result.Blocks) != 2 {
		t.Fatalf("len(Blocks) = %d, want 2", len(result.Blocks))
	}
	if len(result.Edges) != 1 {
		t.Fatalf("len(Edges) = %d, want 1", len(result.Edges))
	}

	// Verify blocks
	if result.Blocks[0].ID != "Step1" || result.Blocks[1].ID != "Step2" {
		t.Errorf("Blocks = %v, want [Step1, Step2]", blockIDs(result.Blocks))
	}

	// Verify edge: Step1 → Step2
	if result.Edges[0].SourceID != "Step1" || result.Edges[0].TargetID != "Step2" {
		t.Errorf("Edge = %v→%v, want Step1→Step2", result.Edges[0].SourceID, result.Edges[0].TargetID)
	}
}

func TestConvertDiamondPipeline(t *testing.T) {
	// Sequential[ClassifyOrder, Parallel[Payment, RiskCheck], MergeAndComplete]
	tree := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "OrderProcessingPipeline"},
		Registries: orchestration.Registries{
			Models: []orchestration.ModelRef{
				{Ref: "m1", Provider: "openai", Config: orchestration.ModelProviderConfig{ModelName: "test"}},
			},
		},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentTypeSequential,
			Name: "OrderProcessingPipeline",
			Children: []orchestration.AgentNode{
				{
					Type:        orchestration.AgentTypeLLM,
					Name:        "ClassifyOrder",
					Model:       &orchestration.ModelReference{Ref: "m1"},
					Instruction: "分析订单...",
					OutputKey:   "order_classification",
				},
				{
					Type: orchestration.AgentTypeParallel,
					Name: "ParallelProcessing",
					Children: []orchestration.AgentNode{
						{
							Type:        orchestration.AgentTypeLLM,
							Name:        "PaymentProcess",
							Model:       &orchestration.ModelReference{Ref: "m1"},
							Instruction: "处理支付...",
							OutputKey:   "payment_result",
						},
						{
							Type:        orchestration.AgentTypeLLM,
							Name:        "RiskCheck",
							Model:       &orchestration.ModelReference{Ref: "m1"},
							Instruction: "风控审查...",
							OutputKey:   "risk_result",
						},
					},
				},
				{
					Type:        orchestration.AgentTypeLLM,
					Name:        "MergeAndComplete",
					Model:       &orchestration.ModelReference{Ref: "m1"},
					Instruction: "汇总...",
					OutputKey:   "final_report",
				},
			},
		},
	}

	result, err := Convert(tree)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if len(result.Blocks) != 4 {
		t.Fatalf("len(Blocks) = %d, want 4; blocks: %v", len(result.Blocks), blockIDs(result.Blocks))
	}

	// Verify blocks: ClassifyOrder, PaymentProcess, RiskCheck, MergeAndComplete
	expectedBlocks := []string{"ClassifyOrder", "PaymentProcess", "RiskCheck", "MergeAndComplete"}
	gotBlocks := blockIDs(result.Blocks)
	for i, expected := range expectedBlocks {
		if gotBlocks[i] != expected {
			t.Errorf("Blocks[%d] = %q, want %q", i, gotBlocks[i], expected)
		}
	}

	// Verify edges: diamond pattern
	// ClassifyOrder → PaymentProcess
	// ClassifyOrder → RiskCheck
	// PaymentProcess → MergeAndComplete
	// RiskCheck → MergeAndComplete
	if len(result.Edges) != 4 {
		t.Fatalf("len(Edges) = %d, want 4; edges: %v", len(result.Edges), formatEdges(result.Edges))
	}

	expectedEdges := map[string]bool{
		"ClassifyOrder→PaymentProcess":   true,
		"ClassifyOrder→RiskCheck":        true,
		"PaymentProcess→MergeAndComplete": true,
		"RiskCheck→MergeAndComplete":      true,
	}
	for _, edge := range result.Edges {
		key := edge.SourceID + "→" + edge.TargetID
		if !expectedEdges[key] {
			t.Errorf("unexpected edge: %s", key)
		}
		delete(expectedEdges, key)
	}
	if len(expectedEdges) > 0 {
		t.Errorf("missing edges: %v", expectedEdges)
	}
}

func TestConvertLoopPipeline(t *testing.T) {
	tree := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "RefineLoop"},
		Registries: orchestration.Registries{
			Models: []orchestration.ModelRef{
				{Ref: "m1", Provider: "openai", Config: orchestration.ModelProviderConfig{ModelName: "test"}},
			},
		},
		Agent: orchestration.AgentNode{
			Type:          orchestration.AgentTypeLoop,
			Name:          "RefineLoop",
			MaxIterations: 3,
			Children: []orchestration.AgentNode{
				{Type: orchestration.AgentTypeLLM, Name: "Writer", Model: &orchestration.ModelReference{Ref: "m1"}, OutputKey: "draft"},
				{Type: orchestration.AgentTypeLLM, Name: "Reviewer", Model: &orchestration.ModelReference{Ref: "m1"}, OutputKey: "review"},
			},
		},
	}

	result, err := Convert(tree)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if len(result.Blocks) != 2 {
		t.Fatalf("len(Blocks) = %d, want 2", len(result.Blocks))
	}
	// Loop treated as sequential: Writer → Reviewer
	if len(result.Edges) != 1 {
		t.Fatalf("len(Edges) = %d, want 1", len(result.Edges))
	}
	if result.Edges[0].SourceID != "Writer" || result.Edges[0].TargetID != "Reviewer" {
		t.Errorf("Edge = %v→%v, want Writer→Reviewer", result.Edges[0].SourceID, result.Edges[0].TargetID)
	}
}

func TestConvertSingleLLMAgent(t *testing.T) {
	tree := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{Name: "SingleAgent"},
		Registries: orchestration.Registries{
			Models: []orchestration.ModelRef{
				{Ref: "m1", Provider: "openai", Config: orchestration.ModelProviderConfig{ModelName: "test"}},
			},
		},
		Agent: orchestration.AgentNode{
			Type:        orchestration.AgentTypeLLM,
			Name:        "MyAgent",
			Model:       &orchestration.ModelReference{Ref: "m1"},
			Instruction: "Do something",
			OutputKey:   "result",
		},
	}

	result, err := Convert(tree)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if len(result.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(result.Blocks))
	}
	if result.Blocks[0].Name != "MyAgent" {
		t.Errorf("Block.Name = %q, want %q", result.Blocks[0].Name, "MyAgent")
	}
	if result.Blocks[0].OutputKey != "result" {
		t.Errorf("Block.OutputKey = %q, want %q", result.Blocks[0].OutputKey, "result")
	}
	if len(result.Edges) != 0 {
		t.Errorf("len(Edges) = %d, want 0", len(result.Edges))
	}
}

func TestConvertNilSchema(t *testing.T) {
	_, err := Convert(nil)
	if err == nil {
		t.Fatal("Convert(nil) should fail")
	}
}

func TestConvertPreservesMetadata(t *testing.T) {
	tree := &orchestration.OrchestrationSchema{
		Version: "1",
		Metadata: orchestration.SchemaMetadata{
			Name:        "TestPipeline",
			Description: "A test pipeline",
			Labels:      map[string]string{"env": "test"},
		},
		Agent: orchestration.AgentNode{
			Type: orchestration.AgentTypeLLM,
			Name: "Agent1",
		},
	}

	result, err := Convert(tree)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if result.Metadata.Name != "TestPipeline" {
		t.Errorf("Metadata.Name = %q, want %q", result.Metadata.Name, "TestPipeline")
	}
	if result.Metadata.Description != "A test pipeline" {
		t.Errorf("Metadata.Description = %q, want %q", result.Metadata.Description, "A test pipeline")
	}
	if result.Metadata.Labels["env"] != "test" {
		t.Errorf("Metadata.Labels[env] = %q, want %q", result.Metadata.Labels["env"], "test")
	}
}

// ---- Helpers ----

func blockIDs(blocks []flow.Block) []string {
	ids := make([]string, len(blocks))
	for i, b := range blocks {
		ids[i] = b.ID
	}
	return ids
}

func formatEdges(edges []flow.Edge) []string {
	result := make([]string, len(edges))
	for i, e := range edges {
		result[i] = e.SourceID + "→" + e.TargetID
	}
	return result
}
