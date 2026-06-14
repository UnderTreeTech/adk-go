// Package orchestration defines the JSON schema, parser, and dynamic builder
// for constructing adk-go agent trees from declarative configurations.
//
// The schema is a tree-based format where each node represents an agent
// (LLMAgent, SequentialAgent, ParallelAgent, or LoopAgent). Workflow agents
// contain children, forming a nested tree that directly maps to the adk-go
// agent construction API.
//
// Registries (services, models, tools, callbacks) decouple references from
// concrete implementations. The builder resolves references via registry
// lookups and recursively constructs the agent tree.
package orchestration

// SchemaVersion is the current supported schema version string.
const SchemaVersion = "1"

// SchemaURI is the JSON schema URI for validation.
const SchemaURI = "https://undertreetech.github.io/adk-go/orchestration/v1"

// OrchestrationSchema is the top-level JSON document that defines a multi-agent
// orchestration configuration.
type OrchestrationSchema struct {
	// Schema is the JSON schema URI (optional, for tooling).
	Schema string `json:"$schema,omitempty"`

	// Version must be "1" (the only supported version currently).
	Version string `json:"version"`

	// Metadata contains name, description, and optional labels.
	Metadata SchemaMetadata `json:"metadata"`

	// Registries declare named references for services, models, tools, and
	// callbacks that agents can reference by ref.
	Registries Registries `json:"registries"`

	// Agent is the root agent node of the orchestration tree.
	Agent AgentNode `json:"agent"`
}

// SchemaMetadata contains metadata about the orchestration configuration.
type SchemaMetadata struct {
	// Name is the human-readable name of the orchestration (required).
	Name string `json:"name"`

	// Description is an optional longer description.
	Description string `json:"description,omitempty"`

	// Labels are optional key-value pairs for categorization.
	Labels map[string]string `json:"labels,omitempty"`
}

// Registries declares named references that agents can reference. Each
// registry type (services, models, tools, callbacks) is independent.
//
// Build order: services → models → tools → callbacks → agent tree.
// Tools that need runtime infrastructure (e.g., artifact service) reference
// services via config keys like "serviceRef".
type Registries struct {
	// Services declare infrastructure services (artifact storage, session
	// backend, memory backend, etc.) that tools may depend on.
	Services []ServiceRef `json:"services,omitempty"`

	// Models declare LLM model instances that LLM agents reference.
	Models []ModelRef `json:"models,omitempty"`

	// Tools declare tool instances that LLM agents can use.
	Tools []ToolRef `json:"tools,omitempty"`

	// Callbacks declare callback functions (beforeAgent, afterAgent, etc.)
	// that agents can attach.
	Callbacks []CallbackRef `json:"callbacks,omitempty"`
}

// ServiceRef declares a named infrastructure service that tools may depend on.
// The provider is resolved from the global service provider registry.
type ServiceRef struct {
	// Ref is the unique reference name (e.g., "disk_artifact").
	Ref string `json:"ref"`

	// Provider identifies the service provider (e.g., "disk_artifact",
	// "s3_artifact").
	Provider string `json:"provider"`

	// Config is provider-specific configuration (e.g., rootDir for disk,
	// bucket/region for S3).
	Config map[string]any `json:"config,omitempty"`
}

// ModelRef declares a named LLM model instance that LLM agents can reference.
type ModelRef struct {
	// Ref is the unique reference name (e.g., "deepseek-v4").
	Ref string `json:"ref"`

	// Provider identifies the model provider (e.g., "openai", "anthropic").
	Provider string `json:"provider"`

	// Config is the provider-specific model configuration.
	Config ModelProviderConfig `json:"config"`
}

// ModelProviderConfig contains configuration for constructing a model.LLM
// instance. Fields are provider-specific; not all fields apply to every
// provider.
type ModelProviderConfig struct {
	// ModelName is the model identifier (e.g., "deepseek-v4-pro", "gpt-4o").
	ModelName string `json:"modelName,omitempty"`

	// APIKeyEnv is the environment variable name holding the API key.
	// The builder reads os.Getenv(APIKeyEnv) at construction time.
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`

	// BaseURLEnv is the environment variable name holding the API base URL.
	// The builder reads os.Getenv(BaseURLEnv) at construction time.
	BaseURLEnv string `json:"baseUrlEnv,omitempty"`

	// ExtraBody is optional additional request body parameters (OpenAI only).
	ExtraBody map[string]any `json:"extraBody,omitempty"`

	// MaxOutputTokens limits the maximum output tokens (Anthropic only).
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`

	// ThinkingBudgetTokens sets the thinking token budget (Anthropic only).
	ThinkingBudgetTokens int `json:"thinkingBudgetTokens,omitempty"`
}

// ToolRef declares a named tool instance that LLM agents can use.
// Tools that depend on infrastructure services reference them via config
// keys (e.g., "serviceRef": "disk_artifact").
type ToolRef struct {
	// Ref is the unique reference name (e.g., "generate_file").
	Ref string `json:"ref"`

	// Provider identifies the tool provider (e.g., "filegentool",
	// "memory_toolset").
	Provider string `json:"provider"`

	// Config is provider-specific configuration. For tools that need
	// infrastructure services, the config should include a "serviceRef" key
	// referencing a ServiceRef.Ref in registries.services.
	Config map[string]any `json:"config,omitempty"`
}

// CallbackRef declares a named callback function that agents can attach.
type CallbackRef struct {
	// Ref is the unique reference name (e.g., "skip_if_no_risk_check").
	Ref string `json:"ref"`

	// Provider identifies the callback provider (e.g., "conditional_skip").
	Provider string `json:"provider"`

	// Config is provider-specific configuration (e.g., conditionKey,
	// outputKey, defaultValue for conditional_skip).
	Config map[string]any `json:"config,omitempty"`
}

// AgentType enumerates the supported agent node types.
type AgentType string

const (
	// AgentTypeLLM represents a LLM-powered agent.
	AgentTypeLLM AgentType = "llm"

	// AgentTypeSequential represents a sequential pipeline agent.
	AgentTypeSequential AgentType = "sequential"

	// AgentTypeParallel represents a parallel execution agent.
	AgentTypeParallel AgentType = "parallel"

	// AgentTypeLoop represents a loop/iterative agent.
	AgentTypeLoop AgentType = "loop"
)

// ValidAgentTypes returns all valid agent type values.
func ValidAgentTypes() []AgentType {
	return []AgentType{AgentTypeLLM, AgentTypeSequential, AgentTypeParallel, AgentTypeLoop}
}

// IsWorkflow returns true if the agent type is a workflow agent
// (sequential, parallel, or loop) that can contain children.
func (t AgentType) IsWorkflow() bool {
	return t == AgentTypeSequential || t == AgentTypeParallel || t == AgentTypeLoop
}

// AgentNode is the union type for all agent nodes in the orchestration tree.
// The Type field discriminates which fields are meaningful:
//   - type == "llm": Model, Instruction, OutputKey, Tools, Callbacks are used
//   - type == "sequential"/"parallel"/"loop": Children is used
//   - type == "loop": MaxIterations is additionally used
type AgentNode struct {
	// Type is the agent type discriminator (required).
	Type AgentType `json:"type"`

	// Name is the unique agent name within the tree (required).
	// Must not be "user" (reserved by ADK).
	Name string `json:"name"`

	// Description is an optional agent description.
	Description string `json:"description,omitempty"`

	// ---- LLMAgent-specific fields (type == "llm") ----

	// Model references a model in registries.models (required for LLM agents).
	Model *ModelReference `json:"model,omitempty"`

	// Instruction is the LLM agent's instruction template.
	// Supports {state_key} placeholder substitution.
	Instruction string `json:"instruction,omitempty"`

	// GlobalInstruction is an optional global instruction appended to all
	// LLM calls in this agent.
	GlobalInstruction string `json:"globalInstruction,omitempty"`

	// OutputKey saves the agent's output to session state under this key.
	// Downstream agents can reference it via {OutputKey} templating.
	OutputKey string `json:"outputKey,omitempty"`

	// Tools references tools in registries.tools that this agent can call.
	Tools []ToolReference `json:"tools,omitempty"`

	// IncludeContents controls whether conversation history is included.
	// "none" means no history; "default" (or empty) means include history.
	IncludeContents string `json:"includeContents,omitempty"`

	// DisallowTransferToParent prevents the agent from transferring
	// conversation control back to its parent agent.
	DisallowTransferToParent bool `json:"disallowTransferToParent,omitempty"`

	// DisallowTransferToPeers prevents the agent from transferring
	// conversation control to sibling agents.
	DisallowTransferToPeers bool `json:"disallowTransferToPeers,omitempty"`

	// ---- Callbacks (all agent types) ----

	// Callbacks references callbacks in registries.callbacks.
	Callbacks AgentCallbacks `json:"callbacks,omitempty"`

	// ---- Workflow agent fields (type == "sequential"/"parallel"/"loop") ----

	// Children are the sub-agents of a workflow agent.
	// Required for sequential, parallel, and loop agents.
	Children []AgentNode `json:"children,omitempty"`

	// ---- LoopAgent-specific fields (type == "loop") ----

	// MaxIterations is the maximum number of loop iterations.
	// 0 means loop indefinitely until a sub-agent escalates or
	// EndInvocation() is called.
	MaxIterations uint `json:"maxIterations,omitempty"`

	// ---- adk-go wrapper level ----

	// DisableDefaultCallbacks disables the default logging callbacks
	// that agent.NewLLMAgent automatically prepends.
	DisableDefaultCallbacks bool `json:"disableDefaultCallbacks,omitempty"`
}

// ModelReference references a model in registries.models by ref.
type ModelReference struct {
	Ref string `json:"ref"`
}

// ToolReference references a tool in registries.tools by ref.
type ToolReference struct {
	Ref string `json:"ref"`
}

// AgentCallbacks references callbacks in registries.callbacks.
type AgentCallbacks struct {
	// BeforeAgent references callbacks that run before the agent executes.
	// If any callback returns non-nil Content, the agent is skipped.
	BeforeAgent []CallbackReference `json:"beforeAgent,omitempty"`

	// AfterAgent references callbacks that run after the agent completes.
	AfterAgent []CallbackReference `json:"afterAgent,omitempty"`
}

// CallbackReference references a callback in registries.callbacks.
// Config can override or supplement the registry-level callback config.
type CallbackReference struct {
	// Ref matches a CallbackRef.Ref in registries.callbacks.
	Ref string `json:"ref"`

	// Config is per-attachment config that overrides/supplements the
	// registry-level config. Keys here override keys in CallbackRef.Config.
	Config map[string]any `json:"config,omitempty"`
}
