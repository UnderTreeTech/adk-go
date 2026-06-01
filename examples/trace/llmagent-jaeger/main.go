package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/UnderTreeTech/adk-go/agent"
	ac "github.com/UnderTreeTech/adk-go/artifact"
	"github.com/UnderTreeTech/adk-go/artifact/s3"
	genaiopenai "github.com/UnderTreeTech/adk-go/model/openai"
	"github.com/UnderTreeTech/adk-go/plugin/trace/jaeger"
	"github.com/UnderTreeTech/adk-go/prompt"
	"github.com/UnderTreeTech/adk-go/tools/appendfiletool"
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	adkAgent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// This example demonstrates using Jaeger as the tracing backend instead of
// Langfuse. It uses google.golang.org/adk/telemetry under the hood with an
// OTLP exporter pointed at the Jaeger collector.
//
// Prerequisites:
//   - Jaeger running with OTLP support (v1.35+)
//   - Default OTLP HTTP endpoint: http://localhost:4318/v1/traces
//
// Quick start with Jaeger all-in-one:
//
//	docker run -d --name jaeger \
//	  -e COLLECTOR_OTLP_ENABLED=true \
//	  -p 16686:16686 \
//	  -p 4317:4317 \
//	  -p 4318:4318 \
//	  jaegertracing/all-in-one:latest
//
// Then open http://localhost:16686 to view traces.
func main() {
	defer log.New(nil).Sync()
	ctx := context.Background()

	var artifactService artifact.Service
	atConfig := &ac.Config{
		StorageType:      "s3",
		InternalEndpoint: "",
		InternalSchema:   "http",
		ExternalEndpoint: "",
		ExternalSchema:   "http",
		AccessKey:        "",
		SecretKey:        "",
		Region:           "shanghai",
		Bucket:           "artifacts",
		ExpireTime:       time.Second * 604800,
	}
	artifactService, err := s3artifact.NewS3Service(atConfig)
	if err != nil {
		fmt.Printf("new s3 artifact service failed: %v", err)
		return
	}

	// 添加统一的文件生成工具（支持小文件一次性生成和大文件分块生成）
	fileGenTool, err := appendfiletool.New(artifactService, atConfig)
	if err != nil {
		fmt.Println("create file generation tool fail", err)
		return
	}

	// 使用 Jaeger 替代 Langfuse 作为 trace 后端
	jaegerCfg, jaegerShutdown, err := jaeger.Setup(&jaeger.Config{
		Endpoint:    getEnvOrDefault("JAEGER_ENDPOINT", "http://localhost:4318/v1/traces"),
		ServiceName: getEnvOrDefault("JAEGER_SERVICE_NAME", "weather-time-agent"),
		Environment: getEnvOrDefault("JAEGER_ENVIRONMENT", "development"),
		Insecure:    true,
	})
	if err != nil {
		fmt.Printf("setup jaeger failed: %v\n", err)
		return
	}
	defer jaegerShutdown(ctx)

	extras := make(map[string]any)
	extras["thinking"] = map[string]any{
		"type": "disabled",
	}
	llmModel := genaiopenai.New(&genaiopenai.Config{
		APIKey:    getEnvOrDefault("OPENAI_API_KEY", ""),
		BaseURL:   getEnvOrDefault("OPENAI_BASE_URL", ""),
		ModelName: getEnvOrDefault("MODEL_NAME", ""),
		ExtraBody: map[string]any{
			"extra_body": extras,
		},
	})

	getCityWeatherTool, err := GetCityWeatherTool()
	if err != nil {
		fmt.Printf("GetCityWeatherTool failed: %v", err)
		return
	}

	a, err := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:              "weather_time_agent",
			Model:             llmModel,
			Description:       "Agent to answer questions about the time and weather in a city.",
			GlobalInstruction: prompt.GlobalInstruction,
			Instruction:       "Your SOLE purpose is to answer questions about the current time and weather in a specific city. You MUST refuse to answer any questions unrelated to time or weather. Finally write the resutl to markdown file and save it to S3.",
			Tools: []tool.Tool{
				getCityWeatherTool,
				fileGenTool,
			},
		},
	})
	if err != nil {
		panic(fmt.Sprintf("Failed to create agent: %v", err))
	}

	// Assemble runner-level plugins: Jaeger trace
	config := &launcher.Config{
		AgentLoader: adkAgent.NewSingleLoader(a),
		PluginConfig: runner.PluginConfig{
			Plugins: jaegerCfg.Plugins,
		},
	}

	l := full.NewLauncher()
	if err = l.Execute(ctx, config, os.Args[1:]); err != nil {
		panic(fmt.Sprintf("Run failed: %v\n\n%s", err, l.CommandLineSyntax()))
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func GetCityWeather(city string) (map[string]any, error) {
	fixedWeather := map[string]struct {
		condition   string
		temperature int
	}{
		"beijing":   {"Sunny", 25},
		"shanghai":  {"Cloudy", 22},
		"guangzhou": {"Rainy", 28},
		"shenzhen":  {"Partly cloudy", 29},
		"chengdu":   {"Windy", 20},
		"hangzhou":  {"Snowy", -2},
		"wuhan":     {"Humid", 26},
		"chongqing": {"Hazy", 30},
		"xi'an":     {"Cool", 18},
		"nanjing":   {"Hot", 32},
	}
	c := strings.ToLower(strings.TrimSpace(city))
	if info, ok := fixedWeather[c]; ok {
		return map[string]any{"result": fmt.Sprintf("%s, %d°C", info.condition, info.temperature)}, nil
	}
	return nil, fmt.Errorf("weather information not found for %s", c)
}

type GetCityWeatherArgs struct {
	City string `json:"city" jsonschema:"The target city name which must be in English"`
}

func GetCityWeatherTool() (tool.Tool, error) {
	handler := func(ctx tool.Context, args GetCityWeatherArgs) (map[string]any, error) {
		return GetCityWeather(args.City)
	}
	return functiontool.New(
		functiontool.Config{
			Name: "get_city_weather",
			Description: `A tools for querying real-time weather information.
Args:
	city: The target city name.
Returns:
	the weather of the target city.`,
		},
		handler)
}
