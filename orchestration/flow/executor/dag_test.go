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

