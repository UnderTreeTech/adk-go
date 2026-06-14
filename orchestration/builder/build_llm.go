package builder

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/agent"
	"github.com/UnderTreeTech/adk-go/orchestration"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// buildLLMAgent constructs an LLMAgent from the schema node.
func (b *Builder) buildLLMAgent(node orchestration.AgentNode) (adkagent.Agent, error) {
	// 1. Resolve model from registry
	var llm model.LLM
	if node.Model != nil && node.Model.Ref != "" {
		resolvedLLM, err := b.cfg.ModelRegistry.Get(node.Model.Ref)
		if err != nil {
			return nil, fmt.Errorf("agent %q: resolve model %q: %w", node.Name, node.Model.Ref, err)
		}
		llm = resolvedLLM
	}

	// 2. Resolve tools from registry
	var tools []tool.Tool
	for _, toolRef := range node.Tools {
		t, err := b.cfg.ToolRegistry.Get(toolRef.Ref)
		if err != nil {
			return nil, fmt.Errorf("agent %q: resolve tool %q: %w", node.Name, toolRef.Ref, err)
		}
		tools = append(tools, t)
	}

	// 3. Resolve callbacks from registry
	beforeCallbacks, err := b.resolveBeforeAgentCallbacks(node.Callbacks.BeforeAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve beforeAgent callbacks: %w", node.Name, err)
	}
	afterCallbacks, err := b.resolveAfterAgentCallbacks(node.Callbacks.AfterAgent)
	if err != nil {
		return nil, fmt.Errorf("agent %q: resolve afterAgent callbacks: %w", node.Name, err)
	}

	// 4. Build LLMAgent using the existing agent.NewLLMAgent factory
	cfg := agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:        node.Name,
			Description: node.Description,
			Model:       llm,
			Instruction: node.Instruction,

			GlobalInstruction:         node.GlobalInstruction,
			OutputKey:                 node.OutputKey,
			Tools:                     tools,
			BeforeAgentCallbacks:      beforeCallbacks,
			AfterAgentCallbacks:       afterCallbacks,
			DisallowTransferToParent:  node.DisallowTransferToParent,
			DisallowTransferToPeers:   node.DisallowTransferToPeers,
		},
		DisableDefaultCallbacks: node.DisableDefaultCallbacks,
	}

	// Handle IncludeContents
	if node.IncludeContents == "none" {
		cfg.LLMAgentConfig.IncludeContents = llmagent.IncludeContentsNone
	}

	ag, err := agent.NewLLMAgent(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent %q: create LLM agent: %w", node.Name, err)
	}
	return ag, nil
}

// resolveBeforeAgentCallbacks resolves a list of callback references to
// BeforeAgentCallback functions.
func (b *Builder) resolveBeforeAgentCallbacks(refs []orchestration.CallbackReference) ([]adkagent.BeforeAgentCallback, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	callbacks := make([]adkagent.BeforeAgentCallback, 0, len(refs))
	for _, ref := range refs {
		cb, err := b.cfg.CallbackRegistry.GetBeforeAgent(ref.Ref)
		if err != nil {
			return nil, fmt.Errorf("resolve beforeAgent callback %q: %w", ref.Ref, err)
		}
		callbacks = append(callbacks, cb)
	}
	return callbacks, nil
}

// resolveAfterAgentCallbacks resolves a list of callback references to
// AfterAgentCallback functions.
func (b *Builder) resolveAfterAgentCallbacks(refs []orchestration.CallbackReference) ([]adkagent.AfterAgentCallback, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	callbacks := make([]adkagent.AfterAgentCallback, 0, len(refs))
	for _, ref := range refs {
		cb, err := b.cfg.CallbackRegistry.GetAfterAgent(ref.Ref)
		if err != nil {
			return nil, fmt.Errorf("resolve afterAgent callback %q: %w", ref.Ref, err)
		}
		callbacks = append(callbacks, cb)
	}
	return callbacks, nil
}
