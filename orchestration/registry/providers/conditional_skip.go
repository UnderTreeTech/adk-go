package providers

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/genai"
)

func init() {
	registry.RegisterCallbackProvider("conditional_skip", conditionalSkipProvider)
}

// conditionalSkipProvider creates a BeforeAgentCallback that conditionally
// skips an agent based on a boolean value in session state.
//
// This implements the "sink the condition into the branch" pattern from
// examples/agents/parallel-conditional/main.go:
//
// When state[conditionKey] is false (or "false"/"no"/"0"), the callback:
//  1. Writes defaultValue to state[outputKey] so downstream agents have data
//  2. Returns non-nil Content to signal the framework to skip the agent
//
// Config keys:
//   - conditionKey (string, required): state key to check
//   - outputKey (string, required): state key to write default value to
//   - defaultValue (string, required): value to write when skipping
func conditionalSkipProvider(config map[string]any, svcReg registry.ServiceRegistry) (
	adkagent.BeforeAgentCallback,
	adkagent.AfterAgentCallback,
	error,
) {
	conditionKey, _ := config["conditionKey"].(string)
	outputKey, _ := config["outputKey"].(string)
	defaultValue, _ := config["defaultValue"].(string)

	if conditionKey == "" {
		return nil, nil, fmt.Errorf("conditional_skip: config[\"conditionKey\"] is required")
	}
	if outputKey == "" {
		return nil, nil, fmt.Errorf("conditional_skip: config[\"outputKey\"] is required")
	}
	if defaultValue == "" {
		return nil, nil, fmt.Errorf("conditional_skip: config[\"defaultValue\"] is required")
	}

	cb := makeConditionalSkipCallback(conditionKey, outputKey, defaultValue)
	return cb, nil, nil
}

// makeConditionalSkipCallback creates the BeforeAgentCallback that implements
// conditional skipping. This is the same logic as conditionalSkipCallback in
// examples/agents/parallel-conditional/main.go.
func makeConditionalSkipCallback(conditionKey, outputKey, defaultValue string) adkagent.BeforeAgentCallback {
	return func(ctx adkagent.CallbackContext) (*genai.Content, error) {
		// Read the condition flag from state
		val, err := ctx.ReadonlyState().Get(conditionKey)
		if err != nil {
			// Condition key not found in state, proceed normally
			return nil, nil
		}

		// Determine whether to skip
		shouldSkip := false
		switch v := val.(type) {
		case bool:
			shouldSkip = !v
		case string:
			shouldSkip = (v == "false" || v == "no" || v == "0")
		default:
			// Unexpected type, proceed normally
			return nil, nil
		}

		if !shouldSkip {
			// Condition is true, execute the agent normally
			return nil, nil
		}

		// ★ Condition is false, skip the agent and write default value ★
		if err := ctx.State().Set(outputKey, defaultValue); err != nil {
			return nil, fmt.Errorf("conditional_skip: failed to set default value for %q: %w", outputKey, err)
		}

		// Return non-nil Content → triggers ADK framework to skip agent execution
		return genai.NewContentFromText(
			fmt.Sprintf("[SKIPPED] Agent %q was skipped because condition %q is false. Default value written to state[%q].",
				ctx.AgentName(), conditionKey, outputKey),
			genai.RoleModel,
		), nil
	}
}
