package flow

import (
	"encoding/json"
	"testing"
)

func TestBlockTypeValues(t *testing.T) {
	types := ValidBlockTypes()
	if len(types) != 3 {
		t.Fatalf("ValidBlockTypes() returned %d types, want 3", len(types))
	}
	expected := map[BlockType]bool{
		BlockTypeStart: true,
		BlockTypeAgent: true,
		BlockTypeEnd:   true,
	}
	for _, typ := range types {
		if !expected[typ] {
			t.Errorf("unexpected block type: %q", typ)
		}
	}
}

func TestFlowSchemaRoundTrip(t *testing.T) {
	original := FlowSchema{
		Schema: FlowSchemaURI,
		Version: FlowSchemaVersion,
		Metadata: FlowMetadata{
			Name:        "GovServicePreReview",
			Description: "政务服务一次办好智能预审",
			Labels:      map[string]string{"env": "production"},
		},
		Blocks: []Block{
			{
				ID:        "entry",
				Name:      "政务服务入口",
				Type:      BlockTypeStart,
				OutputKey: "user_input",
			},
			{
				ID:          "material_receive",
				Name:        "材料接收智能体",
				Type:        BlockTypeAgent,
				OutputKey:   "material_result",
				InputKeys:   []string{"user_input"},
				Description: "接收并标准化处理市民上传的办事材料",
			},
			{
				ID:        "completeness_review",
				Name:      "完整性审核智能体",
				Type:      BlockTypeAgent,
				OutputKey: "completeness_result",
				InputKeys: []string{"material_result"},
			},
			{
				ID:        "auth_verify",
				Name:      "真实性核验智能体",
				Type:      BlockTypeAgent,
				OutputKey: "auth_result",
				InputKeys: []string{"material_result", "completeness_result"},
			},
			{
				ID:        "smart_guide",
				Name:      "智能引导提示智能体",
				Type:      BlockTypeAgent,
				OutputKey: "guide_result",
				InputKeys: []string{"completeness_result", "auth_result"},
			},
			{
				ID:   "output",
				Name: "最终输出",
				Type: BlockTypeEnd,
			},
		},
		Edges: []Edge{
			{SourceID: "entry", TargetID: "material_receive"},
			{SourceID: "material_receive", TargetID: "completeness_review"},
			{SourceID: "material_receive", TargetID: "auth_verify"},
			{SourceID: "completeness_review", TargetID: "auth_verify"},
			{SourceID: "completeness_review", TargetID: "smart_guide"},
			{SourceID: "auth_verify", TargetID: "smart_guide"},
			{SourceID: "smart_guide", TargetID: "output"},
		},
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	// Unmarshal back
	var decoded FlowSchema
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
	if len(decoded.Blocks) != len(original.Blocks) {
		t.Fatalf("len(Blocks) = %d, want %d", len(decoded.Blocks), len(original.Blocks))
	}
	if len(decoded.Edges) != len(original.Edges) {
		t.Fatalf("len(Edges) = %d, want %d", len(decoded.Edges), len(original.Edges))
	}

	// Verify specific blocks
	entryBlock := decoded.Blocks[0]
	if entryBlock.ID != "entry" {
		t.Errorf("Blocks[0].ID = %q, want %q", entryBlock.ID, "entry")
	}
	if entryBlock.Type != BlockTypeStart {
		t.Errorf("Blocks[0].Type = %q, want %q", entryBlock.Type, BlockTypeStart)
	}
	if entryBlock.OutputKey != "user_input" {
		t.Errorf("Blocks[0].OutputKey = %q, want %q", entryBlock.OutputKey, "user_input")
	}

	// Verify agent block with inputKeys
	materialBlock := decoded.Blocks[1]
	if materialBlock.ID != "material_receive" {
		t.Errorf("Blocks[1].ID = %q, want %q", materialBlock.ID, "material_receive")
	}
	if len(materialBlock.InputKeys) != 1 || materialBlock.InputKeys[0] != "user_input" {
		t.Errorf("Blocks[1].InputKeys = %v, want [user_input]", materialBlock.InputKeys)
	}

	// Verify auth_verify has 2 input keys
	authBlock := decoded.Blocks[3]
	if len(authBlock.InputKeys) != 2 {
		t.Errorf("Blocks[3].InputKeys length = %d, want 2", len(authBlock.InputKeys))
	}

	// Verify edges
	firstEdge := decoded.Edges[0]
	if firstEdge.SourceID != "entry" || firstEdge.TargetID != "material_receive" {
		t.Errorf("Edges[0] = {SourceID: %q, TargetID: %q}, want {entry, material_receive}",
			firstEdge.SourceID, firstEdge.TargetID)
	}
}

func TestFlowSchemaParseFromJSON(t *testing.T) {
	jsonStr := `{
  "version": "2",
  "metadata": {
    "name": "OrderProcessingPipeline",
    "description": "订单处理：分类 → 并行[支付+风控] → 合并"
  },
  "blocks": [
    {"id": "entry", "name": "入口", "type": "start", "outputKey": "user_input"},
    {"id": "classify", "name": "ClassifyOrder", "type": "agent", "outputKey": "order_classification", "inputKeys": ["user_input"]},
    {"id": "payment", "name": "PaymentProcess", "type": "agent", "outputKey": "payment_result", "inputKeys": ["order_classification"]},
    {"id": "risk", "name": "RiskCheck", "type": "agent", "outputKey": "risk_result", "inputKeys": ["order_classification"]},
    {"id": "merge", "name": "MergeAndComplete", "type": "agent", "outputKey": "final_report", "inputKeys": ["order_classification", "payment_result", "risk_result"]},
    {"id": "output", "name": "输出", "type": "end"}
  ],
  "edges": [
    {"sourceId": "entry", "targetId": "classify"},
    {"sourceId": "classify", "targetId": "payment"},
    {"sourceId": "classify", "targetId": "risk"},
    {"sourceId": "payment", "targetId": "merge"},
    {"sourceId": "risk", "targetId": "merge"},
    {"sourceId": "merge", "targetId": "output"}
  ]
}`

	var schema FlowSchema
	if err := json.Unmarshal([]byte(jsonStr), &schema); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if schema.Version != "2" {
		t.Errorf("Version = %q, want %q", schema.Version, "2")
	}
	if schema.Metadata.Name != "OrderProcessingPipeline" {
		t.Errorf("Metadata.Name = %q, want %q", schema.Metadata.Name, "OrderProcessingPipeline")
	}
	if len(schema.Blocks) != 6 {
		t.Errorf("len(Blocks) = %d, want 6", len(schema.Blocks))
	}
	if len(schema.Edges) != 6 {
		t.Errorf("len(Edges) = %d, want 6", len(schema.Edges))
	}

	// Verify diamond pattern: classify → {payment, risk} → merge
	classifyBlock := schema.Blocks[1]
	if classifyBlock.ID != "classify" {
		t.Errorf("Blocks[1].ID = %q, want %q", classifyBlock.ID, "classify")
	}
}
