// Package main demonstrates an end-to-end v2 flow orchestration:
// loading a DAG-based JSON schema, parsing it, registering pre-built
// agents with the provider, building the agent tree, and launching it
// with the ADK runner.
//
// Key difference from v1: the JSON schema only defines agent relationships
// (data flow, branching, merging). Agent execution details (model, tools,
// MCP, skills, knowledge) are provided by the caller via AgentProvider.
//
// Usage:
//
//	go run ./orchestration/flow/example
//	go run ./orchestration/flow/example gov_service.json
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/UnderTreeTech/adk-go/orchestration/flow/executor"
	flowparser "github.com/UnderTreeTech/adk-go/orchestration/flow/parser"
	"github.com/UnderTreeTech/adk-go/orchestration/flow/provider"

	"github.com/UnderTreeTech/adk-go/plugin/trace/jaeger"
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	adkAgent "google.golang.org/adk/agent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/runner"
)

func main() {
	ctx := context.Background()
	defer log.New(nil).Sync()

	// -----------------------------------------------------------------------
	// 1. Setup trace (Jaeger)
	// -----------------------------------------------------------------------
	jaegerCfg, jaegerShutdown, err := jaeger.Setup(&jaeger.Config{
		Endpoint:    getEnvOrDefault("JAEGER_ENDPOINT", "http://localhost:4318/v1/traces"),
		ServiceName: getEnvOrDefault("JAEGER_SERVICE_NAME", "flow-orchestration-example"),
		Environment: getEnvOrDefault("JAEGER_ENVIRONMENT", "development"),
		Insecure:    true,
	})
	if err != nil {
		fmt.Printf("setup jaeger failed: %v\n", err)
		return
	}
	defer jaegerShutdown(ctx)

	// -----------------------------------------------------------------------
	// 2. Load and parse the v2 flow schema
	// -----------------------------------------------------------------------
	schemaPath := "gov_service.json"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		schemaPath = os.Args[1]
	}

	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		fmt.Printf("failed to read schema file %q: %v\n", schemaPath, err)
		return
	}

	schema, err := flowparser.Parse(schemaData)
	if err != nil {
		fmt.Printf("failed to parse schema: %v\n", err)
		return
	}

	fmt.Printf("✅ Schema parsed: %s (version %s, %d blocks, %d edges)\n",
		schema.Metadata.Name, schema.Version, len(schema.Blocks), len(schema.Edges))

	// -----------------------------------------------------------------------
	// 3. Register pre-built agents with the provider
	//
	// IMPORTANT: This is where the caller (business layer) provides
	// fully-configured agents with their model, tools, MCP, skills,
	// knowledge, etc. The flow orchestration only handles the
	// arrangement (data flow, branching, merging).
	// -----------------------------------------------------------------------
	p := provider.NewMapAgentProvider()

	for _, block := range schema.Blocks {
		// Skip start/end blocks — they don't need real agents
		if block.Type == "start" || block.Type == "end" {
			continue
		}

		// Create a placeholder agent for each agent-type block.
		// In production, the caller would create fully-configured LLM agents
		// with their model, tools, MCP, skills, knowledge, etc.
		ag, err := createPlaceholderAgent(block.Name, block.OutputKey, block.InputKeys)
		if err != nil {
			fmt.Printf("failed to create agent for block %q: %v\n", block.ID, err)
			return
		}
		if err := p.Register(block.ID, ag); err != nil {
			fmt.Printf("failed to register agent for block %q: %v\n", block.ID, err)
			return
		}
		fmt.Printf("  📦 Registered agent: %s (id=%s, outputKey=%s)\n", block.Name, block.ID, block.OutputKey)
	}

	// -----------------------------------------------------------------------
	// 4. Build the agent tree from the DAG
	// -----------------------------------------------------------------------
	pipeline, err := executor.Build(schema, executor.BuildConfig{
		Name:     schema.Metadata.Name,
		Provider: p,
	})
	if err != nil {
		fmt.Printf("failed to build agent tree: %v\n", err)
		return
	}

	fmt.Printf("✅ Agent tree built: root=%q, subAgents=%d\n", pipeline.Name(), len(pipeline.SubAgents()))

	// -----------------------------------------------------------------------
	// 5. Launch
	// -----------------------------------------------------------------------
	config := &launcher.Config{
		AgentLoader: adkAgent.NewSingleLoader(pipeline),
		PluginConfig: runner.PluginConfig{
			Plugins: jaegerCfg.Plugins,
		},
	}

	l := full.NewLauncher()
	launcherArgs := filterLauncherArgs(os.Args[1:])
	if err := l.Execute(ctx, config, launcherArgs); err != nil {
		panic(fmt.Sprintf("Run failed: %v", err))
	}
}

// createPlaceholderAgent creates a minimal adkagent.Agent for demonstration.
// In production, the caller would create fully-configured LLM agents with
// their model, tools, MCP, skills, knowledge, etc.
func createPlaceholderAgent(name, outputKey string, inputKeys []string) (adkAgent.Agent, error) {
	// Build instruction that references upstream outputs via template substitution
	var instructionParts []string
	instructionParts = append(instructionParts, fmt.Sprintf("You are the %s agent.", name))
	if len(inputKeys) > 0 {
		instructionParts = append(instructionParts, "\n\nInputs from upstream agents:")
		for _, key := range inputKeys {
			instructionParts = append(instructionParts, fmt.Sprintf("\n- {%s}", key))
		}
	}
	instructionParts = append(instructionParts, "\n\nProcess the inputs and produce your result.")

	// In a real application, you would create the agent like this:
	//
	//   model := openai.New(openai.Config{
	//       Model:   "deepseek-v4-pro",
	//       APIKey:  os.Getenv("OPENAI_API_KEY"),
	//       BaseURL: os.Getenv("OPENAI_BASE_URL"),
	//   })
	//   return agent.NewLLMAgent(agent.Config{
	//       LLMAgentConfig: llmagent.Config{
	//           Name:        name,
	//           Model:       model,
	//           Instruction: strings.Join(instructionParts, ""),
	//           OutputKey:   outputKey,
	//           Tools:       []tool.Tool{mcpTool1, skillTool1, ...},
	//       },
	//   })

	// For this example, create a minimal pass-through agent
	ag, err := adkAgent.New(adkAgent.Config{
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("create placeholder agent: %w", err)
	}

	return ag, nil
}

// filterLauncherArgs removes the schema file path argument and keeps only
// flags recognized by the console launcher (e.g. -streaming_mode).
func filterLauncherArgs(args []string) []string {
	var filtered []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
