// Package flow defines the v2 graph-based (DAG) orchestration schema.
//
// Unlike the v1 tree-based schema (orchestration.OrchestrationSchema) which
// uses nested sequential/parallel/loop trees with inline registries, the v2
// flow schema uses a flat list of blocks and edges to define a directed
// acyclic graph (DAG) of agent relationships.
//
// Key design principles:
//   - Orchestration ONLY handles agent relationships (data flow, branching,
//     merging), NOT agent execution details (model, tools, MCP, skills, knowledge).
//   - Agent execution config is provided externally via AgentProvider.
//   - Each Block declares only identity and input/output metadata.
//   - Edges define the data flow between blocks.
package flow

// FlowSchemaVersion is the current supported v2 schema version string.
const FlowSchemaVersion = "2"

// FlowSchemaURI is the JSON schema URI for v2 validation.
const FlowSchemaURI = "https://undertreetech.github.io/adk-go/orchestration/v2"

// FlowSchema is the top-level JSON document that defines a graph-based (DAG)
// multi-agent orchestration configuration.
//
// Unlike the v1 tree-based OrchestrationSchema where workflow agents contain
// nested children, the v2 schema uses flat blocks + edges to define arbitrary
// DAG topologies with explicit data flow, branching, and merging.
type FlowSchema struct {
	// Schema is the JSON schema URI (optional, for tooling).
	Schema string `json:"$schema,omitempty"`

	// Version must be "2" (the only supported v2 version currently).
	Version string `json:"version"`

	// Metadata contains name, description, and optional labels.
	Metadata FlowMetadata `json:"metadata"`

	// Blocks is the flat list of agent nodes in the DAG.
	// Each block declares only identity and input/output metadata;
	// execution details (model, tools, MCP, skills, knowledge) are
	// provided externally via AgentProvider.
	Blocks []Block `json:"blocks"`

	// Edges defines the data flow between blocks.
	// Each edge declares that the output of sourceId flows to targetId.
	Edges []Edge `json:"edges"`
}

// FlowMetadata contains metadata about the flow orchestration configuration.
type FlowMetadata struct {
	// Name is the human-readable name of the orchestration (required).
	Name string `json:"name"`

	// Description is an optional longer description.
	Description string `json:"description,omitempty"`

	// Labels are optional key-value pairs for categorization.
	Labels map[string]string `json:"labels,omitempty"`
}

// BlockType enumerates the supported block (agent node) types.
type BlockType string

const (
	// BlockTypeStart represents an entry point block that receives user input.
	// A start block does not require an AgentProvider entry; it acts as
	// a passthrough that makes the user's input available to downstream blocks.
	BlockTypeStart BlockType = "start"

	// BlockTypeAgent represents a business agent block that processes data.
	// An AgentProvider entry is required for this block type.
	BlockTypeAgent BlockType = "agent"

	// BlockTypeEnd represents a terminal block that outputs the final result.
	// Like start blocks, end blocks do not require an AgentProvider entry.
	BlockTypeEnd BlockType = "end"
)

// ValidBlockTypes returns all valid block type values.
func ValidBlockTypes() []BlockType {
	return []BlockType{BlockTypeStart, BlockTypeAgent, BlockTypeEnd}
}

// Block represents a single agent node in the DAG. Each block declares
// only its identity and input/output metadata — NOT its execution details
// (model, tools, MCP, skills, knowledge). Those are provided externally
// via AgentProvider.
type Block struct {
	// ID is the unique identifier for this block within the flow (required).
	// Used as the key for AgentProvider lookups and edge references.
	ID string `json:"id"`

	// Name is the human-readable agent name (required for agent blocks).
	// Must not be "user" (reserved by ADK).
	Name string `json:"name"`

	// Type is the block type discriminator (required).
	Type BlockType `json:"type"`

	// Description is an optional block description.
	Description string `json:"description,omitempty"`

	// OutputKey saves the block's output to session state under this key.
	// Downstream blocks can reference it via {OutputKey} templating in
	// their agent's instruction. Required for agent blocks.
	OutputKey string `json:"outputKey,omitempty"`

	// InputKeys lists the session state keys that this block depends on
	// from upstream blocks. Used for validation to ensure data flow
	// completeness. Optional; if omitted, validation is skipped for
	// this block's inputs.
	InputKeys []string `json:"inputKeys,omitempty"`
}

// Edge defines a directed data flow between two blocks in the DAG.
// The output of the source block becomes available as input to the
// target block.
type Edge struct {
	// SourceID is the block ID that produces output data.
	SourceID string `json:"sourceId"`

	// TargetID is the block ID that receives the output data.
	TargetID string `json:"targetId"`
}
