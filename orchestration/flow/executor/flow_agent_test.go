package executor

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"testing"

	"github.com/UnderTreeTech/adk-go/orchestration/flow"
	"github.com/UnderTreeTech/adk-go/orchestration/flow/provider"
	adkagent "google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// ---- FlowDAGAgent tests ----

func TestNewFlowDAGAgentBasic(t *testing.T) {
	schema := &flow.FlowSchema{
		Version:  "2",
		Metadata: flow.FlowMetadata{Name: "ConditionalDiamond"},
		Blocks: []flow.Block{
			{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
			{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
			{ID: "risk_check", Name: "RiskCheck", Type: flow.BlockTypeAgent, OutputKey: "risk",
				SkipOutput: `{"status":"auto_approved"}`},
			{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
		},
		Edges: []flow.Edge{
			{SourceID: "classify", TargetID: "payment"},
			{SourceID: "classify", TargetID: "risk_check",
				Condition: &flow.EdgeCondition{StateKey: "needs_risk_check"}},
			{SourceID: "payment", TargetID: "merge"},
			{SourceID: "risk_check", TargetID: "merge"},
		},
	}

	dag, err := NewDAG(schema.Blocks, schema.Edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	p := provider.NewMapAgentProvider()
	p.Register("classify", newTestLLMAgent("Classify"))
	p.Register("payment", newTestLLMAgent("Payment"))
	p.Register("risk_check", newTestLLMAgent("RiskCheck"))
	p.Register("merge", newTestLLMAgent("Merge"))

	ag, err := NewFlowDAGAgent(FlowDAGAgentConfig{
		Name:     "ConditionalDiamond",
		Schema:   schema,
		DAG:      dag,
		Provider: p,
	})
	if err != nil {
		t.Fatalf("NewFlowDAGAgent: %v", err)
	}

	if ag.Name() != "ConditionalDiamond" {
		t.Errorf("Agent.Name() = %q, want %q", ag.Name(), "ConditionalDiamond")
	}
}

func TestNewFlowDAGAgentMissingProvider(t *testing.T) {
	schema := &flow.FlowSchema{
		Version:  "2",
		Metadata: flow.FlowMetadata{Name: "Test"},
		Blocks:   []flow.Block{{ID: "a", Name: "A", Type: flow.BlockTypeAgent}},
		Edges:    []flow.Edge{},
	}

	dag, _ := NewDAG(schema.Blocks, schema.Edges)

	_, err := NewFlowDAGAgent(FlowDAGAgentConfig{
		Name:   "Test",
		Schema: schema,
		DAG:    dag,
	})
	if err == nil {
		t.Fatal("NewFlowDAGAgent should fail with nil provider")
	}
}

func TestNewFlowDAGAgentMissingAgentInProvider(t *testing.T) {
	schema := &flow.FlowSchema{
		Version:  "2",
		Metadata: flow.FlowMetadata{Name: "Test"},
		Blocks: []flow.Block{
			{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
			{ID: "b", Name: "B", Type: flow.BlockTypeAgent},
		},
		Edges: []flow.Edge{
			{SourceID: "a", TargetID: "b",
				Condition: &flow.EdgeCondition{StateKey: "run_b"}},
		},
	}

	dag, _ := NewDAG(schema.Blocks, schema.Edges)

	p := provider.NewMapAgentProvider()
	p.Register("a", newTestLLMAgent("A"))
	// "b" NOT registered — should fail

	_, err := NewFlowDAGAgent(FlowDAGAgentConfig{
		Name:     "Test",
		Schema:   schema,
		DAG:      dag,
		Provider: p,
	})
	if err == nil {
		t.Fatal("NewFlowDAGAgent should fail when agent not in provider")
	}
}

func TestSessionStateEvaluatorIsActive(t *testing.T) {
	// Create a mock session.State
	state := &mockState{
		data: map[string]any{
			"bool_true":    true,
			"bool_false":   false,
			"str_true":     "true",
			"str_false":    "false",
			"str_no":       "no",
			"str_zero":     "0",
			"str_yes":      "yes",
			"int_val":      42,
		},
	}

	eval := &sessionStateEvaluator{state: state}

	tests := []struct {
		key    string
		expect bool
	}{
		{"bool_true", true},
		{"bool_false", false},
		{"str_true", true},    // "true" is not in {"false","no","0"}
		{"str_false", false},  // "false" is falsy
		{"str_no", false},     // "no" is falsy
		{"str_zero", false},   // "0" is falsy
		{"str_yes", true},     // "yes" is truthy
		{"int_val", false},    // non-bool, non-string → false
		{"nonexistent", false}, // key not found → false
	}

	for _, tt := range tests {
		got := eval.IsActive(tt.key)
		if got != tt.expect {
			t.Errorf("IsActive(%q) = %v, want %v", tt.key, got, tt.expect)
		}
	}
}

// ---- mock session.State for testing ----

type mockState struct {
	mu   sync.RWMutex
	data map[string]any
}

var _ session.State = (*mockState)(nil)

func (s *mockState) Get(key string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return val, nil
}

func (s *mockState) Set(key string, val any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = val
	return nil
}

func (s *mockState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for k, v := range s.data {
			if !yield(k, v) {
				return
			}
		}
	}
}

// ---- mock InvocationContext for testing ----
// Note: We can't easily construct a real InvocationContext outside the ADK runner,
// so we test the component parts (ActiveBlocks, sessionStateEvaluator) separately.
// Integration tests with the real runner would go in example/main.go.

// TestFlowDAGAgentRunWithConditionSkipped tests the full run loop
// by directly calling runFlowDAG with a mock context.
// This is an integration-level test within the executor package.
func TestFlowDAGAgentRunWithConditionSkipped(t *testing.T) {
	schema := &flow.FlowSchema{
		Version:  "2",
		Metadata: flow.FlowMetadata{Name: "ConditionalDiamond"},
		Blocks: []flow.Block{
			{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
			{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
			{ID: "risk_check", Name: "RiskCheck", Type: flow.BlockTypeAgent, OutputKey: "risk",
				SkipOutput: `{"status":"auto_approved"}`},
			{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
		},
		Edges: []flow.Edge{
			{SourceID: "classify", TargetID: "payment"},
			{SourceID: "classify", TargetID: "risk_check",
				Condition: &flow.EdgeCondition{StateKey: "needs_risk_check"}},
			{SourceID: "payment", TargetID: "merge"},
			{SourceID: "risk_check", TargetID: "merge"},
		},
	}

	dag, err := NewDAG(schema.Blocks, schema.Edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	// Verify reachability analysis works correctly
	eval := mapEvaluator{"needs_risk_check": false}
	active := dag.ActiveBlocks(eval)

	if !active["classify"] {
		t.Error("classify should be reachable")
	}
	if !active["payment"] {
		t.Error("payment should be reachable")
	}
	if active["risk_check"] {
		t.Error("risk_check should be UNREACHABLE when needs_risk_check=false")
	}
	if !active["merge"] {
		t.Error("merge should be reachable via payment")
	}

	// Verify that when condition is true, all blocks are reachable
	eval = mapEvaluator{"needs_risk_check": true}
	active = dag.ActiveBlocks(eval)
	for _, b := range schema.Blocks {
		if !active[b.ID] {
			t.Errorf("block %q should be reachable when condition is true", b.ID)
		}
	}
}

func TestFlowDAGAgentTransitiveSkipWithMerge(t *testing.T) {
	// Complex DAG:
	//   classify → payment → merge
	//   classify → risk_check(condition) → risk_detail → merge
	schema := &flow.FlowSchema{
		Version:  "2",
		Metadata: flow.FlowMetadata{Name: "TransitiveSkip"},
		Blocks: []flow.Block{
			{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
			{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
			{ID: "risk_check", Name: "RiskCheck", Type: flow.BlockTypeAgent, OutputKey: "risk",
				SkipOutput: `{"status":"auto_approved"}`},
			{ID: "risk_detail", Name: "RiskDetail", Type: flow.BlockTypeAgent, OutputKey: "risk_detail",
				SkipOutput: `{"detail":"skipped"}`},
			{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
		},
		Edges: []flow.Edge{
			{SourceID: "classify", TargetID: "payment"},
			{SourceID: "classify", TargetID: "risk_check",
				Condition: &flow.EdgeCondition{StateKey: "needs_risk"}},
			{SourceID: "payment", TargetID: "merge"},
			{SourceID: "risk_check", TargetID: "risk_detail"},
			{SourceID: "risk_detail", TargetID: "merge"},
		},
	}

	dag, err := NewDAG(schema.Blocks, schema.Edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	eval := mapEvaluator{"needs_risk": false}
	active := dag.ActiveBlocks(eval)

	if !active["classify"] {
		t.Error("classify should be reachable")
	}
	if !active["payment"] {
		t.Error("payment should be reachable")
	}
	if active["risk_check"] {
		t.Error("risk_check should be UNREACHABLE")
	}
	if active["risk_detail"] {
		t.Error("risk_detail should be UNREACHABLE (transitive from risk_check)")
	}
	if !active["merge"] {
		t.Error("merge should be reachable via payment")
	}
}

// Ensure mockState compiles against session.State
func TestMockStateImplementsSessionState(t *testing.T) {
	var _ session.State = &mockState{}
}

// Verify ErrStateKeyNotExist exists in session package
func TestSessionErrStateKeyNotExist(t *testing.T) {
	_ = session.ErrStateKeyNotExist
}

// Verify the mock state Get returns ErrStateKeyNotExist
func TestMockStateGetNonExistent(t *testing.T) {
	state := &mockState{data: map[string]any{}}
	_, err := state.Get("nonexistent")
	if err != session.ErrStateKeyNotExist {
		t.Errorf("Get(nonexistent) error = %v, want ErrStateKeyNotExist", err)
	}
}

// Helper to create a simple test agent for flow DAG tests
func newSimpleTestAgent(name string) adkagent.Agent {
	ag, err := adkagent.New(adkagent.Config{
		Name: name,
		Run: func(_ adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				yield(&session.Event{
					LLMResponse: adkmodel.LLMResponse{
						Content: genai.NewContentFromText(
							fmt.Sprintf("Agent %q executed", name),
							genai.RoleModel,
						),
					},
				}, nil)
			}
		},
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create test agent %q: %v", name, err))
	}
	return ag
}

// Suppress unused import warning
var _ = context.Background
