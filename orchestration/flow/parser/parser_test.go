package parser

import (
	"testing"

	"github.com/UnderTreeTech/adk-go/orchestration/flow"
)

func TestParseValidGovService(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {
    "name": "GovServicePreReview",
    "description": "政务服务一次办好智能预审"
  },
  "blocks": [
    {"id": "entry", "name": "入口", "type": "start", "outputKey": "user_input"},
    {"id": "material", "name": "材料接收", "type": "agent", "outputKey": "material_result"},
    {"id": "completeness", "name": "完整性审核", "type": "agent", "outputKey": "completeness_result"},
    {"id": "auth", "name": "真实性核验", "type": "agent", "outputKey": "auth_result"},
    {"id": "guide", "name": "智能引导", "type": "agent", "outputKey": "guide_result"},
    {"id": "output", "name": "输出", "type": "end"}
  ],
  "edges": [
    {"sourceId": "entry", "targetId": "material"},
    {"sourceId": "material", "targetId": "completeness"},
    {"sourceId": "material", "targetId": "auth"},
    {"sourceId": "completeness", "targetId": "auth"},
    {"sourceId": "completeness", "targetId": "guide"},
    {"sourceId": "auth", "targetId": "guide"},
    {"sourceId": "guide", "targetId": "output"}
  ]
}`

	schema, err := Parse([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if schema.Version != "2" {
		t.Errorf("Version = %q, want %q", schema.Version, "2")
	}
	if schema.Metadata.Name != "GovServicePreReview" {
		t.Errorf("Metadata.Name = %q, want %q", schema.Metadata.Name, "GovServicePreReview")
	}
	if len(schema.Blocks) != 6 {
		t.Errorf("len(Blocks) = %d, want 6", len(schema.Blocks))
	}
	if len(schema.Edges) != 7 {
		t.Errorf("len(Edges) = %d, want 7", len(schema.Edges))
	}
}

func TestParseValidDiamond(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "OrderPipeline"},
  "blocks": [
    {"id": "classify", "name": "Classify", "type": "agent", "outputKey": "cls"},
    {"id": "payment", "name": "Payment", "type": "agent", "outputKey": "pay"},
    {"id": "risk", "name": "Risk", "type": "agent", "outputKey": "risk"},
    {"id": "merge", "name": "Merge", "type": "agent", "outputKey": "final"}
  ],
  "edges": [
    {"sourceId": "classify", "targetId": "payment"},
    {"sourceId": "classify", "targetId": "risk"},
    {"sourceId": "payment", "targetId": "merge"},
    {"sourceId": "risk", "targetId": "merge"}
  ]
}`

	schema, err := Parse([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(schema.Blocks) != 4 {
		t.Errorf("len(Blocks) = %d, want 4", len(schema.Blocks))
	}
}

func TestParseInvalidVersion(t *testing.T) {
	jsonStr := `{
  "version": "1",
  "metadata": {"name": "Test"},
  "blocks": [{"id": "a", "name": "A", "type": "agent"}],
  "edges": []
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
  "version": "2",
  "metadata": {},
  "blocks": [{"id": "a", "name": "A", "type": "agent"}],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for missing metadata.name")
	}
}

func TestParseEmptyBlocks(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for empty blocks")
	}
}

func TestParseDuplicateBlockID(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [
    {"id": "a", "name": "A", "type": "agent"},
    {"id": "a", "name": "B", "type": "agent"}
  ],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for duplicate block ID")
	}
	if !containsStr(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
}

func TestParseReservedBlockName(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [{"id": "a", "name": "user", "type": "agent"}],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for reserved name 'user'")
	}
}

func TestParseInvalidBlockType(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [{"id": "a", "name": "A", "type": "invalid_type"}],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for invalid block type")
	}
}

func TestParseInvalidEdgeRef(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [{"id": "a", "name": "A", "type": "agent"}],
  "edges": [{"sourceId": "a", "targetId": "nonexistent"}]
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for invalid edge reference")
	}
}

func TestParseSelfLoop(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [{"id": "a", "name": "A", "type": "agent"}],
  "edges": [{"sourceId": "a", "targetId": "a"}]
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for self-loop")
	}
}

func TestParseCycleDetection(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [
    {"id": "a", "name": "A", "type": "agent"},
    {"id": "b", "name": "B", "type": "agent"},
    {"id": "c", "name": "C", "type": "agent"}
  ],
  "edges": [
    {"sourceId": "a", "targetId": "b"},
    {"sourceId": "b", "targetId": "c"},
    {"sourceId": "c", "targetId": "a"}
  ]
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for cyclic graph")
	}
	if !containsStr(err.Error(), "cycle") {
		t.Errorf("error should mention 'cycle', got: %v", err)
	}
}

func TestParseDuplicateEdge(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [
    {"id": "a", "name": "A", "type": "agent"},
    {"id": "b", "name": "B", "type": "agent"}
  ],
  "edges": [
    {"sourceId": "a", "targetId": "b"},
    {"sourceId": "a", "targetId": "b"}
  ]
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for duplicate edge")
	}
}

func TestParseDuplicateOutputKey(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [
    {"id": "a", "name": "A", "type": "agent", "outputKey": "result"},
    {"id": "b", "name": "B", "type": "agent", "outputKey": "result"}
  ],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for duplicate outputKey")
	}
}

func TestNormalizeDefaultsDescription(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "Test"},
		Blocks: []flow.Block{
			{ID: "a", Name: "MyAgent", Type: flow.BlockTypeAgent},
		},
	}

	if err := Normalize(schema); err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}
	if schema.Blocks[0].Description != "MyAgent" {
		t.Errorf("Description = %q, want %q (should default to Name)", schema.Blocks[0].Description, "MyAgent")
	}
}

func TestNormalizeTrimsWhitespace(t *testing.T) {
	schema := &flow.FlowSchema{
		Version: "2",
		Metadata: flow.FlowMetadata{Name: "Test"},
		Blocks: []flow.Block{
			{ID: "  a  ", Name: "  MyAgent  ", Type: flow.BlockTypeAgent, OutputKey: "  out  "},
		},
	}

	if err := Normalize(schema); err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}
	if schema.Blocks[0].ID != "a" {
		t.Errorf("ID = %q, want %q", schema.Blocks[0].ID, "a")
	}
	if schema.Blocks[0].Name != "MyAgent" {
		t.Errorf("Name = %q, want %q", schema.Blocks[0].Name, "MyAgent")
	}
	if schema.Blocks[0].OutputKey != "out" {
		t.Errorf("OutputKey = %q, want %q", schema.Blocks[0].OutputKey, "out")
	}
}

func TestParseValidConditionalEdge(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "ConditionalPipeline"},
  "blocks": [
    {"id": "classify", "name": "Classify", "type": "agent", "outputKey": "cls"},
    {"id": "payment", "name": "Payment", "type": "agent", "outputKey": "pay"},
    {"id": "risk", "name": "RiskCheck", "type": "agent", "outputKey": "risk",
     "skipOutput": "{\"status\":\"auto_approved\"}"},
    {"id": "merge", "name": "Merge", "type": "agent", "outputKey": "final"}
  ],
  "edges": [
    {"sourceId": "classify", "targetId": "payment"},
    {"sourceId": "classify", "targetId": "risk", "condition": {"stateKey": "needs_risk_check"}},
    {"sourceId": "payment", "targetId": "merge"},
    {"sourceId": "risk", "targetId": "merge"}
  ]
}`

	schema, err := Parse([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(schema.Edges) != 4 {
		t.Errorf("len(Edges) = %d, want 4", len(schema.Edges))
	}

	// Verify the conditional edge
	condEdge := schema.Edges[1]
	if condEdge.Condition == nil {
		t.Fatal("Edges[1] should have a condition")
	}
	if condEdge.Condition.StateKey != "needs_risk_check" {
		t.Errorf("Condition.StateKey = %q, want %q", condEdge.Condition.StateKey, "needs_risk_check")
	}

	// Verify SkipOutput
	riskBlock := schema.Blocks[2]
	if riskBlock.SkipOutput != `{"status":"auto_approved"}` {
		t.Errorf("RiskBlock.SkipOutput = %q, want auto_approved JSON", riskBlock.SkipOutput)
	}
}

func TestParseEdgeConditionMissingStateKey(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [
    {"id": "a", "name": "A", "type": "agent", "outputKey": "out_a"},
    {"id": "b", "name": "B", "type": "agent", "outputKey": "out_b"}
  ],
  "edges": [
    {"sourceId": "a", "targetId": "b", "condition": {}}
  ]
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for EdgeCondition with empty stateKey")
	}
	if !containsStr(err.Error(), "stateKey") {
		t.Errorf("error should mention 'stateKey', got: %v", err)
	}
}

func TestParseSkipOutputWithoutOutputKey(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [
    {"id": "a", "name": "A", "type": "agent", "skipOutput": "{\"status\":\"skipped\"}"}
  ],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err == nil {
		t.Fatal("Parse() should fail for block with skipOutput but no outputKey")
	}
	if !containsStr(err.Error(), "skipOutput") {
		t.Errorf("error should mention 'skipOutput', got: %v", err)
	}
}

func TestParseSkipOutputWithOutputKey(t *testing.T) {
	// skipOutput with outputKey should be valid
	jsonStr := `{
  "version": "2",
  "metadata": {"name": "Test"},
  "blocks": [
    {"id": "a", "name": "A", "type": "agent", "outputKey": "result", "skipOutput": "{\"status\":\"skipped\"}"},
    {"id": "b", "name": "B", "type": "agent", "outputKey": "other"}
  ],
  "edges": []
}`

	_, err := Parse([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Parse() should succeed for skipOutput with outputKey, got: %v", err)
	}
}

// helper
func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
