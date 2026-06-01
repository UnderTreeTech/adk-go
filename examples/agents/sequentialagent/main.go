package main

import (
	"context"
	"fmt"
	"os"

	"github.com/UnderTreeTech/adk-go/agent"
	genaiopenai "github.com/UnderTreeTech/adk-go/model/openai"
	"github.com/UnderTreeTech/adk-go/plugin/compaction"
	//"github.com/UnderTreeTech/adk-go/plugin/trace/jaeger"
	"github.com/UnderTreeTech/adk-go/plugin/trace/langfuse"
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	adkAgent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
)

func main() {
	ctx := context.Background()

	defer log.New(nil).Sync()

	// 使用 Jaeger 替代 Langfuse 作为 trace 后端
	//jaegerCfg, jaegerShutdown, err := jaeger.Setup(&jaeger.Config{
	//	Endpoint:    getEnvOrDefault("JAEGER_ENDPOINT", "http://localhost:4318/v1/traces"),
	//	ServiceName: getEnvOrDefault("JAEGER_SERVICE_NAME", "weather-time-agent"),
	//	Environment: getEnvOrDefault("JAEGER_ENVIRONMENT", "development"),
	//	Insecure:    true,
	//})
	//if err != nil {
	//	fmt.Printf("setup jaeger failed: %v\n", err)
	//	return
	//}
	//defer jaegerShutdown(ctx)

	langfuseCfg, langfuseShutdown, _ := langfuse.Setup(&langfuse.Config{
		Host:      getEnvOrDefault("LANGFUSE_HOST", ""),
		PublicKey: getEnvOrDefault("LANGFUSE_PUBLIC_KEY", ""),
		SecretKey: getEnvOrDefault("LANGFUSE_SECRET_KEY", ""),
	})
	defer langfuseShutdown(ctx)

	llmModel := genaiopenai.New(&genaiopenai.Config{
		APIKey:    getEnvOrDefault("OPENAI_API_KEY", ""),
		BaseURL:   getEnvOrDefault("OPENAI_BASE_URL", ""),
		ModelName: getEnvOrDefault("MODEL_NAME", ""),
	})

	// 创建一个共享的 ContextGuard 实例，为所有 LLM agent 注册策略。
	// ContextGuard 是按 agent name 匹配的（beforeModel 回调中使用
	// ctx.AgentName() 查找策略），所以必须为每个 LLM agent 注册。
	// 但 Plugin 只能有一个（PluginManager 不允许重名 plugin），所以
	// 不能通过每个 agent 各自创建 ContextGuard 的方式来解决。
	registry := compaction.NewCrushRegistry()
	guard := compaction.New(registry)
	guard.Add("CodeWriter", llmModel, compaction.WithMaxTokens(12800))
	guard.Add("CodeReviewer", llmModel, compaction.WithMaxTokens(12800))
	guard.Add("CodeRefactorer", llmModel, compaction.WithMaxTokens(12800))
	guardCfg := guard.PluginConfig() // 一个 plugin，内含三个 agent 的策略

	// 第一步：代码生成 Agent
	writer, _ := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:  "CodeWriter",
			Model: llmModel,
			Instruction: `你是一个 Go 代码生成器。根据用户需求编写 Go 代码。
只输出完整的 Go 代码块，用 '''go ... ''' 包裹。不要添加其他文字。`,
			OutputKey: "generated_code", // 输出存入 state["generated_code"],
		},
	})

	// 第二步：代码审查 Agent（读取上一步的输出）
	reviewer, _ := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:  "CodeReviewer",
			Model: llmModel,
			Instruction: `你是一个资深 Go 代码审查专家。审查以下代码，指出问题和改进建议。

代码：
'''go
{generated_code}
'''

请列出具体的问题和修改建议。`,
			OutputKey: "review_comments", // 输出存入 state["review_comments"]
		},
	})

	// 第三步：代码重构 Agent（读取前两步的输出）
	refactorer, _ := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:  "CodeRefactorer",
			Model: llmModel,
			Instruction: `你是一个代码重构专家。根据审查意见重构代码。

原始代码：
'''go
{generated_code}
'''

审查意见：
{review_comments}

请输出重构后的完整代码。`,
			OutputKey: "refactored_code",
		},
	})

	// 组装流水线：写 → 审 → 改
	pipeline, _ := agent.NewSequentialAgent(agent.Config{
		Config: adkAgent.Config{
			Name:        "code_pipeline",
			Description: "代码开发流水线：生成 → 审查 → 重构",
			SubAgents:   []adkAgent.Agent{writer, reviewer, refactorer},
		},
	})

	// 在 runner 级别组装所有 plugins：trace + ContextGuard
	var allPlugins []*plugin.Plugin
	allPlugins = append(allPlugins, langfuseCfg.Plugins...) // Langfuse trace
	//allPlugins = append(allPlugins, jaegerCfg.Plugins...)   // or Jaeger trace
	allPlugins = append(allPlugins, guardCfg.Plugins...) // 共享 ContextGuard（一个 plugin，三个 agent 策略）

	config := &launcher.Config{
		AgentLoader: adkAgent.NewSingleLoader(pipeline),
		PluginConfig: runner.PluginConfig{
			Plugins: allPlugins,
		},
	}
	l := full.NewLauncher()
	if err := l.Execute(ctx, config, os.Args[1:]); err != nil {
		panic(fmt.Sprintf("Run failed: %v", err))
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
