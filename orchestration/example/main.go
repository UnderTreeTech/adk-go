// Package main demonstrates an end-to-end orchestration: loading a JSON
// schema, parsing it, building registries, constructing the agent tree,
// and launching it with the ADK runner.
//
// This example reproduces the same flow as examples/agents/parallel-conditional,
// but driven entirely by the JSON schema file pipeline.json.
//
// Usage:
//
//	go run ./orchestration/example
//	go run ./orchestration/example pipeline.json
//
// Environment variables:
//
//	OPENAI_API_KEY   – LLM API key
//	OPENAI_BASE_URL  – LLM base URL
//	MODEL_NAME       – model identifier
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/UnderTreeTech/adk-go/orchestration/builder"
	"github.com/UnderTreeTech/adk-go/orchestration/parser"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	_ "github.com/UnderTreeTech/adk-go/orchestration/registry/providers" // Register built-in providers

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
		ServiceName: getEnvOrDefault("JAEGER_SERVICE_NAME", "orchestration-example"),
		Environment: getEnvOrDefault("JAEGER_ENVIRONMENT", "development"),
		Insecure:    true,
	})
	if err != nil {
		fmt.Printf("setup jaeger failed: %v\n", err)
		return
	}
	defer jaegerShutdown(ctx)

	// -----------------------------------------------------------------------
	// 2. Load and parse the orchestration schema
	// -----------------------------------------------------------------------
	schemaPath := "pipeline.json"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		schemaPath = os.Args[1]
	}

	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		fmt.Printf("failed to read schema file %q: %v\n", schemaPath, err)
		return
	}

	schema, err := parser.Parse(schemaData)
	if err != nil {
		fmt.Printf("failed to parse schema: %v\n", err)
		return
	}

	fmt.Printf("✅ Schema parsed: %s (version %s)\n", schema.Metadata.Name, schema.Version)

	// -----------------------------------------------------------------------
	// 3. Build registries: services → models → tools → callbacks
	// -----------------------------------------------------------------------
	svcReg := registry.NewServiceRegistry()
	if len(schema.Registries.Services) > 0 {
		if err := svcReg.RegisterFromRefs(schema.Registries.Services); err != nil {
			fmt.Printf("failed to register services: %v\n", err)
			return
		}
	}

	modelReg := registry.NewModelRegistry()
	if err := modelReg.RegisterFromRefs(schema.Registries.Models, svcReg); err != nil {
		fmt.Printf("failed to register models: %v\n", err)
		return
	}

	toolReg := registry.NewToolRegistry()
	if len(schema.Registries.Tools) > 0 {
		if err := toolReg.RegisterFromRefs(schema.Registries.Tools, svcReg); err != nil {
			fmt.Printf("failed to register tools: %v\n", err)
			return
		}
	}

	callbackReg := registry.NewCallbackRegistry()
	if len(schema.Registries.Callbacks) > 0 {
		if err := callbackReg.RegisterFromRefs(schema.Registries.Callbacks, svcReg); err != nil {
			fmt.Printf("failed to register callbacks: %v\n", err)
			return
		}
	}

	// -----------------------------------------------------------------------
	// 4. Build the agent tree from the schema
	// -----------------------------------------------------------------------
	b := builder.New(builder.BuilderConfig{
		ModelRegistry:    modelReg,
		ToolRegistry:     toolReg,
		CallbackRegistry: callbackReg,
	})

	pipeline, err := b.Build(schema)
	if err != nil {
		fmt.Printf("failed to build agent tree: %v\n", err)
		return
	}

	fmt.Printf("✅ Agent tree built: root=%q\n", pipeline.Name())

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
	// Filter args: only pass flags recognized by the launcher (skip schema path arg)
	launcherArgs := filterLauncherArgs(os.Args[1:])
	if err := l.Execute(ctx, config, launcherArgs); err != nil {
		panic(fmt.Sprintf("Run failed: %v", err))
	}
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
