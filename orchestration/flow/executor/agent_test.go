package executor

import (
	"context"
	"fmt"
	"iter"
	"testing"

	"github.com/UnderTreeTech/adk-go/orchestration/flow"
	"github.com/UnderTreeTech/adk-go/orchestration/flow/provider"
	adkagent "google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// mockLLM is a minimal mock model.LLM for creating real agents.
type mockLLM struct {
	name string
}

func (m *mockLLM) Name() string { return m.name }
func (m *mockLLM) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(&adkmodel.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "mock response"}}}}, nil)
	}
}

// newTestLLMAgent creates a real LLM agent with a mock model for testing.
func newTestLLMAgent(name string) adkagent.Agent {
	ag, err := adkagent.New(adkagent.Config{
		Name: name,
		Run: func(_ adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		},
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create test agent: %v", err))
	}
	return ag
}

func TestBuildLinearChain(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "LinearChain"},
		Blocks: []flow.Block{
			{ID: "a", Name: "AgentA", Type: flow.BlockTypeAgent, OutputKey: "out_a"},
			{ID: "b", Name: "AgentB", Type: flow.BlockTypeAgent, OutputKey: "out_b"},
			{ID: "c", Name: "AgentC", Type: flow.BlockTypeAgent, OutputKey: "out_c"},
		},
		Edges: []flow.Edge{
			{SourceID: "a", TargetID: "b"},
			{SourceID: "b", TargetID: "c"},
		},
	}

	p := provider.NewMapAgentProvider()
	p.Register("a", newTestLLMAgent("AgentA"))
	p.Register("b", newTestLLMAgent("AgentB"))
	p.Register("c", newTestLLMAgent("AgentC"))

	ag, err := Build(schema, BuildConfig{Name: "LinearPipeline", Provider: p})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if ag.Name() != "LinearPipeline" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "LinearPipeline")
	}
	// Should be a sequential agent with 3 sub-agents
	if len(ag.SubAgents()) != 3 {
		t.Errorf("len(SubAgents) = %d, want 3", len(ag.SubAgents()))
	}
}

func TestBuildDiamond(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "DiamondPipeline"},
		Blocks: []flow.Block{
			{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
			{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
			{ID: "risk", Name: "Risk", Type: flow.BlockTypeAgent, OutputKey: "risk"},
			{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
		},
		Edges: []flow.Edge{
			{SourceID: "classify", TargetID: "payment"},
			{SourceID: "classify", TargetID: "risk"},
			{SourceID: "payment", TargetID: "merge"},
			{SourceID: "risk", TargetID: "merge"},
		},
	}

	p := provider.NewMapAgentProvider()
	p.Register("classify", newTestLLMAgent("Classify"))
	p.Register("payment", newTestLLMAgent("Payment"))
	p.Register("risk", newTestLLMAgent("Risk"))
	p.Register("merge", newTestLLMAgent("Merge"))

	ag, err := Build(schema, BuildConfig{Name: "DiamondPipeline", Provider: p})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if ag.Name() != "DiamondPipeline" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "DiamondPipeline")
	}
	// Should be: SequentialAgent[Classify, ParallelAgent[Payment, Risk], Merge]
	if len(ag.SubAgents()) != 3 {
		t.Fatalf("len(SubAgents) = %d, want 3", len(ag.SubAgents()))
	}
	if ag.SubAgents()[0].Name() != "Classify" {
		t.Errorf("SubAgents[0].Name() = %q, want %q", ag.SubAgents()[0].Name(), "Classify")
	}
	// Middle should be parallel
	parallel := ag.SubAgents()[1]
	if len(parallel.SubAgents()) != 2 {
		t.Errorf("ParallelAgent should have 2 sub-agents, got %d", len(parallel.SubAgents()))
	}
	if ag.SubAgents()[2].Name() != "Merge" {
		t.Errorf("SubAgents[2].Name() = %q, want %q", ag.SubAgents()[2].Name(), "Merge")
	}
}

func TestBuildGovServiceFlow(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "GovServicePreReview"},
		Blocks: []flow.Block{
			{ID: "entry", Name: "入口", Type: flow.BlockTypeStart, OutputKey: "user_input"},
			{ID: "material", Name: "材料接收", Type: flow.BlockTypeAgent, OutputKey: "material_result"},
			{ID: "completeness", Name: "完整性审核", Type: flow.BlockTypeAgent, OutputKey: "completeness_result"},
			{ID: "auth", Name: "真实性核验", Type: flow.BlockTypeAgent, OutputKey: "auth_result"},
			{ID: "guide", Name: "智能引导", Type: flow.BlockTypeAgent, OutputKey: "guide_result"},
			{ID: "output", Name: "输出", Type: flow.BlockTypeEnd},
		},
		Edges: []flow.Edge{
			{SourceID: "entry", TargetID: "material"},
			{SourceID: "material", TargetID: "completeness"},
			{SourceID: "material", TargetID: "auth"},
			{SourceID: "completeness", TargetID: "auth"},
			{SourceID: "completeness", TargetID: "guide"},
			{SourceID: "auth", TargetID: "guide"},
			{SourceID: "guide", TargetID: "output"},
		},
	}

	p := provider.NewMapAgentProvider()
	p.Register("material", newTestLLMAgent("材料接收"))
	p.Register("completeness", newTestLLMAgent("完整性审核"))
	p.Register("auth", newTestLLMAgent("真实性核验"))
	p.Register("guide", newTestLLMAgent("智能引导"))

	ag, err := Build(schema, BuildConfig{Name: "GovServicePipeline", Provider: p})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if ag.Name() != "GovServicePipeline" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "GovServicePipeline")
	}
	// Should have 6 sub-agents (one per level, all sequential)
	if len(ag.SubAgents()) != 6 {
		t.Errorf("len(SubAgents) = %d, want 6", len(ag.SubAgents()))
	}
}

func TestBuildMissingProvider(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "Test"},
		Blocks: []flow.Block{
			{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
		},
		Edges: []flow.Edge{},
	}

	_, err := Build(schema, BuildConfig{Name: "Test", Provider: nil})
	if err == nil {
		t.Fatal("Build should fail with nil provider")
	}
}

func TestBuildMissingAgentInProvider(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "Test"},
		Blocks: []flow.Block{
			{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
			{ID: "b", Name: "B", Type: flow.BlockTypeAgent},
		},
		Edges: []flow.Edge{
			{SourceID: "a", TargetID: "b"},
		},
	}

	p := provider.NewMapAgentProvider()
	p.Register("a", newTestLLMAgent("A"))
	// Note: "b" is NOT registered — should fail

	_, err := Build(schema, BuildConfig{Name: "Test", Provider: p})
	if err == nil {
		t.Fatal("Build should fail when agent not in provider")
	}
}

func TestBuildSingleAgent(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "Single"},
		Blocks: []flow.Block{
			{ID: "a", Name: "AgentA", Type: flow.BlockTypeAgent, OutputKey: "out_a"},
		},
		Edges: []flow.Edge{},
	}

	p := provider.NewMapAgentProvider()
	p.Register("a", newTestLLMAgent("AgentA"))

	ag, err := Build(schema, BuildConfig{Name: "SinglePipeline", Provider: p})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Single agent should be returned directly, not wrapped in SequentialAgent
	if ag.Name() != "AgentA" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "AgentA")
	}
}

func TestBuildDiamondWithConditionEdge(t *testing.T) {
	schema := &flow.FlowSchema{
		Version:  "2",
		Metadata: flow.FlowMetadata{Name: "ConditionalDiamond"},
		Blocks: []flow.Block{
			{ID: "start", Name: "Start", Type: flow.BlockTypeStart, OutputKey: "user_input"},
			{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls",
				InputKeys: []string{"user_input"}},
			{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
			{ID: "risk_check", Name: "RiskCheck", Type: flow.BlockTypeAgent, OutputKey: "risk",
				InputKeys: []string{"cls"},
				SkipOutput: `{"status":"auto_approved"}`},
			{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final",
				InputKeys: []string{"pay", "risk"}},
			{ID: "end", Name: "End", Type: flow.BlockTypeEnd},
		},
		Edges: []flow.Edge{
			{SourceID: "start", TargetID: "classify"},
			{SourceID: "classify", TargetID: "payment"},
			{SourceID: "classify", TargetID: "risk_check",
				Condition: &flow.EdgeCondition{StateKey: "needs_risk_check"}},
			{SourceID: "payment", TargetID: "merge"},
			{SourceID: "risk_check", TargetID: "merge"},
			{SourceID: "merge", TargetID: "end"},
		},
	}

	p := provider.NewMapAgentProvider()
	p.Register("classify", newTestLLMAgent("Classify"))
	p.Register("payment", newTestLLMAgent("Payment"))
	p.Register("risk_check", newTestLLMAgent("RiskCheck"))
	p.Register("merge", newTestLLMAgent("Merge"))

	root, err := Build(schema, BuildConfig{
		Name:     "ConditionalDiamondPipeline",
		Provider: p,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// When schema has conditional edges, Build returns a FlowDAGAgent
	if root.Name() != "ConditionalDiamondPipeline" {
		t.Errorf("root.Name() = %q, want %q", root.Name(), "ConditionalDiamondPipeline")
	}
}

func TestBuildNoConditionEdgeReturnsStatic(t *testing.T) {
	// Schema without conditional edges should go through buildStatic path
	schema := &flow.FlowSchema{
		Version:  "2",
		Metadata: flow.FlowMetadata{Name: "SimpleLinear"},
		Blocks: []flow.Block{
			{ID: "start", Name: "Start", Type: flow.BlockTypeStart},
			{ID: "a", Name: "A", Type: flow.BlockTypeAgent, OutputKey: "out_a"},
			{ID: "end", Name: "End", Type: flow.BlockTypeEnd},
		},
		Edges: []flow.Edge{
			{SourceID: "start", TargetID: "a"},
			{SourceID: "a", TargetID: "end"},
		},
	}

	p := provider.NewMapAgentProvider()
	p.Register("a", newTestLLMAgent("A"))

	root, err := Build(schema, BuildConfig{Provider: p})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if root.Name() == "" {
		t.Error("root agent should have a name")
	}
}
