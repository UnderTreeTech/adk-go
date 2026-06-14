package executor

import (
	"fmt"
	"testing"

	"github.com/UnderTreeTech/adk-go/orchestration/flow"
)

func TestDAGLinearChain(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent, OutputKey: "out_a"},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent, OutputKey: "out_b"},
		{ID: "c", Name: "C", Type: flow.BlockTypeAgent, OutputKey: "out_c"},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "b"},
		{SourceID: "b", TargetID: "c"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	levels := dag.Levels()
	if len(levels) != 3 {
		t.Fatalf("len(Levels) = %d, want 3", len(levels))
	}
	if levels[0][0].ID != "a" {
		t.Errorf("Level 0 = %v, want [a]", levels[0])
	}
	if levels[1][0].ID != "b" {
		t.Errorf("Level 1 = %v, want [b]", levels[1])
	}
	if levels[2][0].ID != "c" {
		t.Errorf("Level 2 = %v, want [c]", levels[2])
	}
}

func TestDAGDiamond(t *testing.T) {
	// Diamond: classify → {payment, risk} → merge
	blocks := []flow.Block{
		{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
		{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
		{ID: "risk", Name: "Risk", Type: flow.BlockTypeAgent, OutputKey: "risk"},
		{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
	}
	edges := []flow.Edge{
		{SourceID: "classify", TargetID: "payment"},
		{SourceID: "classify", TargetID: "risk"},
		{SourceID: "payment", TargetID: "merge"},
		{SourceID: "risk", TargetID: "merge"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	levels := dag.Levels()
	if len(levels) != 3 {
		t.Fatalf("len(Levels) = %d, want 3", len(levels))
	}
	// Level 0: classify
	if len(levels[0]) != 1 || levels[0][0].ID != "classify" {
		t.Errorf("Level 0 = %v, want [classify]", levelIDs(levels[0]))
	}
	// Level 1: payment + risk (parallel)
	if len(levels[1]) != 2 {
		t.Fatalf("Level 1 len = %d, want 2", len(levels[1]))
	}
	level1IDs := levelIDs(levels[1])
	if level1IDs[0] != "payment" || level1IDs[1] != "risk" {
		t.Errorf("Level 1 = %v, want [payment, risk]", level1IDs)
	}
	// Level 2: merge
	if len(levels[2]) != 1 || levels[2][0].ID != "merge" {
		t.Errorf("Level 2 = %v, want [merge]", levelIDs(levels[2]))
	}
}

func TestDAGGovServiceFlow(t *testing.T) {
	// 政务服务 example: entry → material → {completeness, auth} → smart_guide → output
	// But completeness also feeds into auth, so auth has 2 upstream
	blocks := []flow.Block{
		{ID: "entry", Name: "入口", Type: flow.BlockTypeStart, OutputKey: "user_input"},
		{ID: "material", Name: "材料接收", Type: flow.BlockTypeAgent, OutputKey: "material_result"},
		{ID: "completeness", Name: "完整性审核", Type: flow.BlockTypeAgent, OutputKey: "completeness_result"},
		{ID: "auth", Name: "真实性核验", Type: flow.BlockTypeAgent, OutputKey: "auth_result"},
		{ID: "guide", Name: "智能引导", Type: flow.BlockTypeAgent, OutputKey: "guide_result"},
		{ID: "output", Name: "输出", Type: flow.BlockTypeEnd},
	}
	edges := []flow.Edge{
		{SourceID: "entry", TargetID: "material"},
		{SourceID: "material", TargetID: "completeness"},
		{SourceID: "material", TargetID: "auth"},
		{SourceID: "completeness", TargetID: "auth"},
		{SourceID: "completeness", TargetID: "guide"},
		{SourceID: "auth", TargetID: "guide"},
		{SourceID: "guide", TargetID: "output"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	levels := dag.Levels()
	// Expected levels:
	// 0: entry
	// 1: material
	// 2: completeness (auth needs completeness, so auth is level 3)
	// 3: auth
	// 4: guide
	// 5: output
	if len(levels) != 6 {
		t.Fatalf("len(Levels) = %d, want 6; levels: %v", len(levels), formatLevels(levels))
	}

	expectedLevels := [][]string{
		{"entry"},
		{"material"},
		{"completeness"},
		{"auth"},
		{"guide"},
		{"output"},
	}
	for i, expected := range expectedLevels {
		got := levelIDs(levels[i])
		if !equalStrings(got, expected) {
			t.Errorf("Level %d = %v, want %v", i, got, expected)
		}
	}
}

func TestDAGCycleDetection(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent},
		{ID: "c", Name: "C", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "b"},
		{SourceID: "b", TargetID: "c"},
		{SourceID: "c", TargetID: "a"}, // cycle!
	}

	_, err := NewDAG(blocks, edges)
	if err == nil {
		t.Fatal("NewDAG should fail for cyclic graph")
	}
}

func TestDAGSelfLoop(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "a"}, // self-loop
	}

	_, err := NewDAG(blocks, edges)
	if err == nil {
		t.Fatal("NewDAG should fail for self-loop")
	}
}

func TestDAGInvalidEdgeRef(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "nonexistent"},
	}

	_, err := NewDAG(blocks, edges)
	if err == nil {
		t.Fatal("NewDAG should fail for invalid edge reference")
	}
}

func TestDAGDuplicateBlockID(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
		{ID: "a", Name: "A2", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{}

	_, err := NewDAG(blocks, edges)
	if err == nil {
		t.Fatal("NewDAG should fail for duplicate block ID")
	}
}

func TestDAGTopologicalSort(t *testing.T) {
	blocks := []flow.Block{
		{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
		{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
		{ID: "risk", Name: "Risk", Type: flow.BlockTypeAgent, OutputKey: "risk"},
		{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
	}
	edges := []flow.Edge{
		{SourceID: "classify", TargetID: "payment"},
		{SourceID: "classify", TargetID: "risk"},
		{SourceID: "payment", TargetID: "merge"},
		{SourceID: "risk", TargetID: "merge"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	sortOrder := dag.TopologicalSort()
	// Verify topological ordering: classify must come before payment/risk,
	// and payment/risk must come before merge
	pos := make(map[string]int)
	for i, id := range sortOrder {
		pos[id] = i
	}
	if pos["classify"] >= pos["payment"] {
		t.Error("classify should come before payment in topological sort")
	}
	if pos["classify"] >= pos["risk"] {
		t.Error("classify should come before risk in topological sort")
	}
	if pos["payment"] >= pos["merge"] {
		t.Error("payment should come before merge in topological sort")
	}
	if pos["risk"] >= pos["merge"] {
		t.Error("risk should come before merge in topological sort")
	}
}

func TestDAGUpstreamDownstream(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent},
		{ID: "c", Name: "C", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "b"},
		{SourceID: "a", TargetID: "c"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	// Downstream of a
	ds := dag.Downstream("a")
	if !equalStrings(ds, []string{"b", "c"}) {
		t.Errorf("Downstream(a) = %v, want [b, c]", ds)
	}

	// Upstream of b
	us := dag.Upstream("b")
	if !equalStrings(us, []string{"a"}) {
		t.Errorf("Upstream(b) = %v, want [a]", us)
	}

	// InDegree
	if dag.InDegree("a") != 0 {
		t.Errorf("InDegree(a) = %d, want 0", dag.InDegree("a"))
	}
	if dag.InDegree("b") != 1 {
		t.Errorf("InDegree(b) = %d, want 1", dag.InDegree("b"))
	}

	// Non-existent block
	if dag.InDegree("nonexistent") != -1 {
		t.Errorf("InDegree(nonexistent) = %d, want -1", dag.InDegree("nonexistent"))
	}
	if dag.Downstream("nonexistent") != nil {
		t.Errorf("Downstream(nonexistent) should be nil")
	}
}

func TestDAGLevelOf(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "b"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	if dag.LevelOf("a") != 0 {
		t.Errorf("LevelOf(a) = %d, want 0", dag.LevelOf("a"))
	}
	if dag.LevelOf("b") != 1 {
		t.Errorf("LevelOf(b) = %d, want 1", dag.LevelOf("b"))
	}
	if dag.LevelOf("nonexistent") != -1 {
		t.Errorf("LevelOf(nonexistent) = %d, want -1", dag.LevelOf("nonexistent"))
	}
}

func TestDAGDisconnectedGraph(t *testing.T) {
	// Multiple disconnected components
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{} // no edges

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	levels := dag.Levels()
	if len(levels) != 1 {
		t.Fatalf("len(Levels) = %d, want 1 for disconnected graph", len(levels))
	}
	if len(levels[0]) != 2 {
		t.Errorf("Level 0 should have 2 blocks, got %d", len(levels[0]))
	}
}

// ---- ConditionEvaluator tests ----

// mapEvaluator is a test ConditionEvaluator backed by a map.
type mapEvaluator map[string]bool

func (e mapEvaluator) IsActive(stateKey string) bool {
	return e[stateKey]
}

func TestDAGActiveBlocksNoCondition(t *testing.T) {
	// Diamond without conditions: all blocks reachable
	blocks := []flow.Block{
		{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
		{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
		{ID: "risk", Name: "Risk", Type: flow.BlockTypeAgent, OutputKey: "risk"},
		{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
	}
	edges := []flow.Edge{
		{SourceID: "classify", TargetID: "payment"},
		{SourceID: "classify", TargetID: "risk"},
		{SourceID: "payment", TargetID: "merge"},
		{SourceID: "risk", TargetID: "merge"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	// nil evaluator → all reachable
	active := dag.ActiveBlocks(nil)
	for _, b := range blocks {
		if !active[b.ID] {
			t.Errorf("block %q should be reachable with nil evaluator", b.ID)
		}
	}

	// AlwaysActiveEvaluator → all reachable
	active = dag.ActiveBlocks(&AlwaysActiveEvaluator{})
	for _, b := range blocks {
		if !active[b.ID] {
			t.Errorf("block %q should be reachable with AlwaysActiveEvaluator", b.ID)
		}
	}
}

func TestDAGActiveBlocksDiamondWithCondition(t *testing.T) {
	// Diamond: classify → {payment, risk_check(condition)} → merge
	// When needs_risk_check = false, risk_check is unreachable
	blocks := []flow.Block{
		{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
		{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
		{ID: "risk_check", Name: "RiskCheck", Type: flow.BlockTypeAgent, OutputKey: "risk"},
		{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
	}
	edges := []flow.Edge{
		{SourceID: "classify", TargetID: "payment"},
		{SourceID: "classify", TargetID: "risk_check", Condition: &flow.EdgeCondition{StateKey: "needs_risk_check"}},
		{SourceID: "payment", TargetID: "merge"},
		{SourceID: "risk_check", TargetID: "merge"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	// Condition FALSE → risk_check unreachable
	eval := mapEvaluator{"needs_risk_check": false}
	active := dag.ActiveBlocks(eval)

	if !active["classify"] {
		t.Error("classify should be reachable")
	}
	if !active["payment"] {
		t.Error("payment should be reachable")
	}
	if active["risk_check"] {
		t.Error("risk_check should be UNREACHABLE when condition is false")
	}
	if !active["merge"] {
		t.Error("merge should still be reachable via payment")
	}

	// Condition TRUE → all reachable
	eval = mapEvaluator{"needs_risk_check": true}
	active = dag.ActiveBlocks(eval)

	for _, b := range blocks {
		if !active[b.ID] {
			t.Errorf("block %q should be reachable when condition is true", b.ID)
		}
	}
}

func TestDAGActiveBlocksTransitiveSkip(t *testing.T) {
	// Chain: A → B(condition) → C → D
	// When condition is false, B is unreachable, and C and D should also be unreachable
	// (transitive pruning)
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent, OutputKey: "out_a"},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent, OutputKey: "out_b"},
		{ID: "c", Name: "C", Type: flow.BlockTypeAgent, OutputKey: "out_c"},
		{ID: "d", Name: "D", Type: flow.BlockTypeAgent, OutputKey: "out_d"},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "b", Condition: &flow.EdgeCondition{StateKey: "run_b"}},
		{SourceID: "b", TargetID: "c"},
		{SourceID: "c", TargetID: "d"},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	// Condition FALSE → B, C, D all unreachable
	eval := mapEvaluator{"run_b": false}
	active := dag.ActiveBlocks(eval)

	if !active["a"] {
		t.Error("a should be reachable (in-degree 0)")
	}
	if active["b"] {
		t.Error("b should be UNREACHABLE when condition is false")
	}
	if active["c"] {
		t.Error("c should be UNREACHABLE (transitive from b)")
	}
	if active["d"] {
		t.Error("d should be UNREACHABLE (transitive from b)")
	}

	// Condition TRUE → all reachable
	eval = mapEvaluator{"run_b": true}
	active = dag.ActiveBlocks(eval)
	for _, b := range blocks {
		if !active[b.ID] {
			t.Errorf("block %q should be reachable when condition is true", b.ID)
		}
	}
}

func TestDAGActiveBlocksPartialMerge(t *testing.T) {
	// Merge node with multiple incoming edges, some inactive:
	//   classify → payment → merge
	//   classify → risk_check(condition) → merge
	// When condition is false, risk_check is unreachable but merge is still reachable via payment
	blocks := []flow.Block{
		{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
		{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
		{ID: "risk_check", Name: "RiskCheck", Type: flow.BlockTypeAgent, OutputKey: "risk"},
		{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
	}
	edges := []flow.Edge{
		{SourceID: "classify", TargetID: "payment"},
		{SourceID: "classify", TargetID: "risk_check", Condition: &flow.EdgeCondition{StateKey: "needs_risk"}},
		{SourceID: "payment", TargetID: "merge"},
		{SourceID: "risk_check", TargetID: "merge"},
	}

	dag, err := NewDAG(blocks, edges)
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
		t.Error("risk_check should be UNREACHABLE when condition is false")
	}
	if !active["merge"] {
		t.Error("merge should be reachable via payment (even though risk_check is unreachable)")
	}
}

func TestDAGActiveBlocksTransitiveWithMerge(t *testing.T) {
	// Complex case: condition branch has downstream that feeds into merge
	//   classify → payment → merge
	//   classify → risk_check(condition) → risk_detail → merge
	// When condition is false: risk_check and risk_detail unreachable, merge reachable via payment
	blocks := []flow.Block{
		{ID: "classify", Name: "Classify", Type: flow.BlockTypeAgent, OutputKey: "cls"},
		{ID: "payment", Name: "Payment", Type: flow.BlockTypeAgent, OutputKey: "pay"},
		{ID: "risk_check", Name: "RiskCheck", Type: flow.BlockTypeAgent, OutputKey: "risk"},
		{ID: "risk_detail", Name: "RiskDetail", Type: flow.BlockTypeAgent, OutputKey: "risk_detail"},
		{ID: "merge", Name: "Merge", Type: flow.BlockTypeAgent, OutputKey: "final"},
	}
	edges := []flow.Edge{
		{SourceID: "classify", TargetID: "payment"},
		{SourceID: "classify", TargetID: "risk_check", Condition: &flow.EdgeCondition{StateKey: "needs_risk"}},
		{SourceID: "payment", TargetID: "merge"},
		{SourceID: "risk_check", TargetID: "risk_detail"},
		{SourceID: "risk_detail", TargetID: "merge"},
	}

	dag, err := NewDAG(blocks, edges)
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
		t.Error("risk_detail should be UNREACHABLE (transitive)")
	}
	if !active["merge"] {
		t.Error("merge should be reachable via payment")
	}
}

func TestDAGActiveInDegreeWithCondition(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent, OutputKey: "out_a"},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent, OutputKey: "out_b"},
		{ID: "c", Name: "C", Type: flow.BlockTypeAgent, OutputKey: "out_c"},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "c"},
		{SourceID: "b", TargetID: "c", Condition: &flow.EdgeCondition{StateKey: "run_b"}},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	// Full in-degree
	if dag.ActiveInDegree("c", nil) != 2 {
		t.Errorf("ActiveInDegree(c, nil) = %d, want 2", dag.ActiveInDegree("c", nil))
	}

	// With condition false
	eval := mapEvaluator{"run_b": false}
	if dag.ActiveInDegree("c", eval) != 1 {
		t.Errorf("ActiveInDegree(c, false) = %d, want 1", dag.ActiveInDegree("c", eval))
	}

	// With condition true
	eval = mapEvaluator{"run_b": true}
	if dag.ActiveInDegree("c", eval) != 2 {
		t.Errorf("ActiveInDegree(c, true) = %d, want 2", dag.ActiveInDegree("c", eval))
	}
}

func TestDAGFindEdge(t *testing.T) {
	blocks := []flow.Block{
		{ID: "a", Name: "A", Type: flow.BlockTypeAgent},
		{ID: "b", Name: "B", Type: flow.BlockTypeAgent},
	}
	edges := []flow.Edge{
		{SourceID: "a", TargetID: "b", Condition: &flow.EdgeCondition{StateKey: "cond"}},
	}

	dag, err := NewDAG(blocks, edges)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	edge := dag.findEdge("a", "b")
	if edge == nil {
		t.Fatal("findEdge(a,b) should not be nil")
	}
	if edge.Condition == nil || edge.Condition.StateKey != "cond" {
		t.Error("findEdge should return edge with condition")
	}

	if dag.findEdge("b", "a") != nil {
		t.Error("findEdge(b,a) should be nil (no such edge)")
	}
}

// ---- Helpers ----

func levelIDs(blocks []flow.Block) []string {
	ids := make([]string, len(blocks))
	for i, b := range blocks {
		ids[i] = b.ID
	}
	return ids
}

func formatLevels(levels [][]flow.Block) string {
	var result string
	for i, level := range levels {
		result += fmt.Sprintf("  Level %d: %v\n", i, levelIDs(level))
	}
	return result
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

