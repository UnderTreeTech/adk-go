package orchestration

import (
	"encoding/json"
	"testing"
)

// TestAgentTypeIsWorkflow verifies the IsWorkflow method for all agent types.
func TestAgentTypeIsWorkflow(t *testing.T) {
	tests := []struct {
		typ      AgentType
		expected bool
	}{
		{AgentTypeLLM, false},
		{AgentTypeSequential, true},
		{AgentTypeParallel, true},
		{AgentTypeLoop, true},
		{AgentType("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.typ.IsWorkflow(); got != tt.expected {
			t.Errorf("AgentType(%q).IsWorkflow() = %v, want %v", tt.typ, got, tt.expected)
		}
	}
}

// TestValidAgentTypes verifies that all expected types are returned.
func TestValidAgentTypes(t *testing.T) {
	types := ValidAgentTypes()
	if len(types) != 4 {
		t.Fatalf("ValidAgentTypes() returned %d types, want 4", len(types))
	}
	expected := map[AgentType]bool{
		AgentTypeLLM:        true,
		AgentTypeSequential: true,
		AgentTypeParallel:   true,
		AgentTypeLoop:       true,
	}
	for _, typ := range types {
		if !expected[typ] {
			t.Errorf("unexpected agent type: %q", typ)
		}
	}
}

// TestOrchestrationSchemaRoundTrip verifies JSON marshal/unmarshal round-trip.
func TestOrchestrationSchemaRoundTrip(t *testing.T) {
	original := OrchestrationSchema{
		Schema: SchemaURI,
		Version: SchemaVersion,
		Metadata: SchemaMetadata{
			Name:        "TestPipeline",
			Description: "A test pipeline",
			Labels:      map[string]string{"env": "test"},
		},
		Registries: Registries{
			Services: []ServiceRef{
				{
					Ref:      "disk_artifact",
					Provider: "disk_artifact",
					Config:   map[string]any{"rootDir": "/tmp/artifacts"},
				},
			},
			Models: []ModelRef{
				{
					Ref:      "deepseek-v4",
					Provider: "openai",
					Config: ModelProviderConfig{
						ModelName: "deepseek-v4-pro",
						APIKeyEnv: "OPENAI_API_KEY",
						BaseURLEnv: "OPENAI_BASE_URL",
					},
				},
			},
			Tools: []ToolRef{
				{
					Ref:      "generate_file",
					Provider: "filegentool",
					Config:   map[string]any{"serviceRef": "disk_artifact"},
				},
			},
			Callbacks: []CallbackRef{
				{
					Ref:      "skip_if_no_risk",
					Provider: "conditional_skip",
					Config: map[string]any{
						"conditionKey": "needs_risk_check",
						"outputKey":    "risk_result",
						"defaultValue": `{"status":"auto_approved"}`,
					},
				},
			},
		},
		Agent: AgentNode{
			Type:        AgentTypeSequential,
			Name:        "TestPipeline",
			Description: "A test sequential pipeline",
			Children: []AgentNode{
				{
					Type:        AgentTypeLLM,
					Name:        "Step1",
					Description: "First step",
					Model:       &ModelReference{Ref: "deepseek-v4"},
					Instruction: "Do step 1",
					OutputKey:   "step1_result",
					Tools:       []ToolReference{{Ref: "generate_file"}},
				},
				{
					Type:        AgentTypeParallel,
					Name:        "ParallelStep",
					Description: "Parallel execution",
					Children: []AgentNode{
						{
							Type:        AgentTypeLLM,
							Name:        "BranchA",
							Instruction: "Branch A instruction",
							OutputKey:   "branch_a_result",
							Model:       &ModelReference{Ref: "deepseek-v4"},
						},
						{
							Type:        AgentTypeLLM,
							Name:        "BranchB",
							Instruction: "Branch B instruction",
							OutputKey:   "branch_b_result",
							Model:       &ModelReference{Ref: "deepseek-v4"},
							Callbacks: AgentCallbacks{
								BeforeAgent: []CallbackReference{{Ref: "skip_if_no_risk"}},
							},
						},
					},
				},
			},
		},
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	// Unmarshal back
	var decoded OrchestrationSchema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Verify key fields
	if decoded.Version != original.Version {
		t.Errorf("Version = %q, want %q", decoded.Version, original.Version)
	}
	if decoded.Metadata.Name != original.Metadata.Name {
		t.Errorf("Metadata.Name = %q, want %q", decoded.Metadata.Name, original.Metadata.Name)
	}
	if decoded.Agent.Type != original.Agent.Type {
		t.Errorf("Agent.Type = %q, want %q", decoded.Agent.Type, original.Agent.Type)
	}
	if decoded.Agent.Name != original.Agent.Name {
		t.Errorf("Agent.Name = %q, want %q", decoded.Agent.Name, original.Agent.Name)
	}
	if len(decoded.Agent.Children) != len(original.Agent.Children) {
		t.Fatalf("len(Agent.Children) = %d, want %d", len(decoded.Agent.Children), len(original.Agent.Children))
	}

	// Verify LLM agent child
	child0 := decoded.Agent.Children[0]
	if child0.Type != AgentTypeLLM {
		t.Errorf("Children[0].Type = %q, want %q", child0.Type, AgentTypeLLM)
	}
	if child0.Model == nil || child0.Model.Ref != "deepseek-v4" {
		t.Errorf("Children[0].Model.Ref = %v, want %q", child0.Model, "deepseek-v4")
	}
	if child0.OutputKey != "step1_result" {
		t.Errorf("Children[0].OutputKey = %q, want %q", child0.OutputKey, "step1_result")
	}

	// Verify parallel child
	child1 := decoded.Agent.Children[1]
	if child1.Type != AgentTypeParallel {
		t.Errorf("Children[1].Type = %q, want %q", child1.Type, AgentTypeParallel)
	}
	if len(child1.Children) != 2 {
		t.Fatalf("len(Children[1].Children) = %d, want 2", len(child1.Children))
	}

	// Verify callback reference on BranchB
	branchB := child1.Children[1]
	if len(branchB.Callbacks.BeforeAgent) != 1 {
		t.Fatalf("len(BranchB.Callbacks.BeforeAgent) = %d, want 1", len(branchB.Callbacks.BeforeAgent))
	}
	if branchB.Callbacks.BeforeAgent[0].Ref != "skip_if_no_risk" {
		t.Errorf("BranchB.Callbacks.BeforeAgent[0].Ref = %q, want %q", branchB.Callbacks.BeforeAgent[0].Ref, "skip_if_no_risk")
	}

	// Verify registries
	if len(decoded.Registries.Models) != 1 {
		t.Errorf("len(Registries.Models) = %d, want 1", len(decoded.Registries.Models))
	}
	if len(decoded.Registries.Tools) != 1 {
		t.Errorf("len(Registries.Tools) = %d, want 1", len(decoded.Registries.Tools))
	}
	if len(decoded.Registries.Callbacks) != 1 {
		t.Errorf("len(Registries.Callbacks) = %d, want 1", len(decoded.Registries.Callbacks))
	}
	if len(decoded.Registries.Services) != 1 {
		t.Errorf("len(Registries.Services) = %d, want 1", len(decoded.Registries.Services))
	}
}

// TestLoopAgentSchema verifies LoopAgent-specific fields.
func TestLoopAgentSchema(t *testing.T) {
	schema := OrchestrationSchema{
		Version: SchemaVersion,
		Metadata: SchemaMetadata{Name: "LoopTest"},
		Agent: AgentNode{
			Type:          AgentTypeLoop,
			Name:          "RefinementLoop",
			MaxIterations: 3,
			Children: []AgentNode{
				{Type: AgentTypeLLM, Name: "Writer", Model: &ModelReference{Ref: "model1"}},
				{Type: AgentTypeLLM, Name: "Reviewer", Model: &ModelReference{Ref: "model1"}},
			},
		},
	}

	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded OrchestrationSchema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Agent.MaxIterations != 3 {
		t.Errorf("MaxIterations = %d, want 3", decoded.Agent.MaxIterations)
	}
	if len(decoded.Agent.Children) != 2 {
		t.Errorf("len(Children) = %d, want 2", len(decoded.Agent.Children))
	}
}

// TestParallelConditionalJSON parses the canonical parallel-conditional example.
func TestParallelConditionalJSON(t *testing.T) {
	jsonStr := `{
  "$schema": "https://undertreetech.github.io/adk-go/orchestration/v1",
  "version": "1",
  "metadata": {
    "name": "OrderProcessingPipeline",
    "description": "订单处理：分类 → 并行[支付+风控] → 合并"
  },
  "registries": {
    "models": [
      {
        "ref": "deepseek-v4",
        "provider": "openai",
        "config": {
          "modelName": "deepseek-v4-pro",
          "apiKeyEnv": "OPENAI_API_KEY",
          "baseUrlEnv": "OPENAI_BASE_URL"
        }
      }
    ],
    "callbacks": [
      {
        "ref": "skip_if_no_risk_check",
        "provider": "conditional_skip",
        "config": {
          "conditionKey": "needs_risk_check",
          "outputKey": "risk_result",
          "defaultValue": "{\"status\":\"auto_approved\"}"
        }
      }
    ]
  },
  "agent": {
    "type": "sequential",
    "name": "OrderProcessingPipeline",
    "children": [
      {
        "type": "llm",
        "name": "ClassifyOrder",
        "model": {"ref": "deepseek-v4"},
        "instruction": "分析订单...",
        "outputKey": "order_classification"
      },
      {
        "type": "parallel",
        "name": "ParallelProcessing",
        "children": [
          {
            "type": "llm",
            "name": "PaymentProcess",
            "model": {"ref": "deepseek-v4"},
            "instruction": "处理支付...\n{order_classification}",
            "outputKey": "payment_result"
          },
          {
            "type": "llm",
            "name": "RiskCheck",
            "model": {"ref": "deepseek-v4"},
            "instruction": "风控审查...\n{order_classification}",
            "outputKey": "risk_result",
            "callbacks": {
              "beforeAgent": [{"ref": "skip_if_no_risk_check"}]
            }
          }
        ]
      },
      {
        "type": "llm",
        "name": "MergeAndComplete",
        "model": {"ref": "deepseek-v4"},
        "instruction": "汇总：\n{order_classification}\n{payment_result}\n{risk_result}",
        "outputKey": "final_report"
      }
    ]
  }
}`

	var schema OrchestrationSchema
	if err := json.Unmarshal([]byte(jsonStr), &schema); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Verify top-level
	if schema.Version != "1" {
		t.Errorf("Version = %q, want %q", schema.Version, "1")
	}
	if schema.Metadata.Name != "OrderProcessingPipeline" {
		t.Errorf("Metadata.Name = %q, want %q", schema.Metadata.Name, "OrderProcessingPipeline")
	}

	// Verify registries
	if len(schema.Registries.Models) != 1 || schema.Registries.Models[0].Ref != "deepseek-v4" {
		t.Errorf("Models[0].Ref = %q, want %q", schema.Registries.Models[0].Ref, "deepseek-v4")
	}
	if len(schema.Registries.Callbacks) != 1 {
		t.Errorf("len(Callbacks) = %d, want 1", len(schema.Registries.Callbacks))
	}

	// Verify root agent
	if schema.Agent.Type != AgentTypeSequential {
		t.Errorf("Agent.Type = %q, want %q", schema.Agent.Type, AgentTypeSequential)
	}
	if len(schema.Agent.Children) != 3 {
		t.Fatalf("len(Agent.Children) = %d, want 3", len(schema.Agent.Children))
	}

	// Verify ClassifyOrder
	classify := schema.Agent.Children[0]
	if classify.Name != "ClassifyOrder" || classify.Type != AgentTypeLLM {
		t.Errorf("Children[0] = {Name: %q, Type: %q}, want {ClassifyOrder, llm}", classify.Name, classify.Type)
	}
	if classify.OutputKey != "order_classification" {
		t.Errorf("ClassifyOrder.OutputKey = %q, want %q", classify.OutputKey, "order_classification")
	}

	// Verify ParallelProcessing
	parallel := schema.Agent.Children[1]
	if parallel.Type != AgentTypeParallel {
		t.Errorf("Children[1].Type = %q, want %q", parallel.Type, AgentTypeParallel)
	}
	if len(parallel.Children) != 2 {
		t.Fatalf("len(ParallelProcessing.Children) = %d, want 2", len(parallel.Children))
	}

	// Verify RiskCheck has beforeAgent callback
	riskCheck := parallel.Children[1]
	if riskCheck.Name != "RiskCheck" {
		t.Errorf("ParallelProcessing.Children[1].Name = %q, want %q", riskCheck.Name, "RiskCheck")
	}
	if len(riskCheck.Callbacks.BeforeAgent) != 1 {
		t.Fatalf("len(RiskCheck.Callbacks.BeforeAgent) = %d, want 1", len(riskCheck.Callbacks.BeforeAgent))
	}
	if riskCheck.Callbacks.BeforeAgent[0].Ref != "skip_if_no_risk_check" {
		t.Errorf("RiskCheck.Callbacks.BeforeAgent[0].Ref = %q, want %q",
			riskCheck.Callbacks.BeforeAgent[0].Ref, "skip_if_no_risk_check")
	}

	// Verify MergeAndComplete
	merge := schema.Agent.Children[2]
	if merge.Name != "MergeAndComplete" {
		t.Errorf("Children[2].Name = %q, want %q", merge.Name, "MergeAndComplete")
	}
}
