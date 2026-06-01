// Package main demonstrates how to solve the "parallel branch + conditional
// merge" problem in adk-go.
//
// Problem:
//
//	         ┌── BranchA ──┐
//	START ──┤              ├── MergeNode ── END
//	         └── BranchB ──┘
//
// When one of the parallel branches is conditional (may or may not execute),
// the merge node still expects results from both branches. If the conditional
// branch doesn't run, the merge node would be stuck or produce incorrect output.
//
// Solution: "Sink the condition into the branch"
//
// Instead of skipping the branch entirely, we let both branches always execute
// under ParallelAgent. Each branch uses a BeforeAgentCallback to check a
// condition in session state. If the condition is not met, the callback returns
// a "skipped" marker and writes a default value to state, so the merge node
// always has data from both branches.
//
// Concrete scenario: Order Processing System
//   - ClassifyOrder: determines order risk level based on order amount
//   - PaymentProcess (BranchA): always executes, processes the payment
//   - RiskCheck (BranchB): only executes for high-value orders (amount >= 10000)
//   - MergeAndComplete: merges results from both branches, completes order
//
// Flow diagram:
//
//	Sequential[
//	  ClassifyOrder,          ← sets state: order_amount, needs_risk_check
//	  Parallel[
//	    PaymentProcess,       ← always runs, sets state: payment_result
//	    RiskCheck,            ← conditional: skips if needs_risk_check == false
//	  ],                        sets state: risk_result ("approved"/"skipped")
//	  MergeAndComplete,       ← reads payment_result + risk_result from state
//	]
//
// Usage:
//
//	go run ./examples/parallel-conditional --run "处理订单 #12345，金额 15000 元"
//	go run ./examples/parallel-conditional --run "处理订单 #67890，金额 500 元"
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

	"github.com/UnderTreeTech/adk-go/agent"
	genaiopenai "github.com/UnderTreeTech/adk-go/model/openai"
	"github.com/UnderTreeTech/adk-go/plugin/trace/langfuse"
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	adkAgent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

func main() {
	ctx := context.Background()
	defer log.New(nil).Sync()

	// -----------------------------------------------------------------------
	// 1. Setup trace (Langfuse)
	// -----------------------------------------------------------------------
	langfuseCfg, langfuseShutdown, _ := langfuse.Setup(&langfuse.Config{
		Host:      getEnvOrDefault("LANGFUSE_HOST", ""),
		PublicKey: getEnvOrDefault("LANGFUSE_PUBLIC_KEY", ""),
		SecretKey: getEnvOrDefault("LANGFUSE_SECRET_KEY", ""),
	})
	defer langfuseShutdown(ctx)

	// -----------------------------------------------------------------------
	// 2. Setup LLM model
	// -----------------------------------------------------------------------
	llmModel := genaiopenai.New(&genaiopenai.Config{
		APIKey:    getEnvOrDefault("OPENAI_API_KEY", ""),
		BaseURL:   getEnvOrDefault("OPENAI_BASE_URL", ""),
		ModelName: getEnvOrDefault("MODEL_NAME", ""),
	})

	// -----------------------------------------------------------------------
	// 3. Create agents
	// -----------------------------------------------------------------------

	// Step 1: ClassifyOrder — 分析订单，将分类结果写入 state
	classifyOrder, _ := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:  "ClassifyOrder",
			Model: llmModel,
			Instruction: `你是一个订单分类 Agent。分析用户输入的订单信息，提取以下内容：
1. 订单号
2. 订单金额

然后判断是否需要风控审查：
- 如果金额 >= 10000 元，需要风控审查
- 如果金额 < 10000 元，不需要风控审查

你的输出必须严格按照以下 JSON 格式（不要输出其他任何内容）：
{"order_id": "订单号", "amount": 金额数字, "needs_risk_check": true或false}

例如：
用户输入: "处理订单 #12345，金额 15000 元"
你的输出: {"order_id": "#12345", "amount": 15000, "needs_risk_check": true}

用户输入: "处理订单 #67890，金额 500 元"
你的输出: {"order_id": "#67890", "amount": 500, "needs_risk_check": false}`,
			OutputKey: "order_classification",
		},
	})

	// Step 2a: PaymentProcess — 支付处理（总是执行）
	paymentProcess, _ := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:  "PaymentProcess",
			Model: llmModel,
			Instruction: `你是一个支付处理 Agent。根据订单分类信息处理支付。

订单分类信息：
{order_classification}

请模拟支付处理过程：
1. 验证支付信息
2. 冻结金额
3. 确认支付

输出支付处理结果，格式：
{"status": "success", "message": "支付处理完成", "transaction_id": "TXN-随机8位数字"}`,
			OutputKey: "payment_result",
		},
	})

	// Step 2b: RiskCheck — 风控审查（条件执行：仅大额订单）
	//
	// ★ 核心解决方案：使用 BeforeAgentCallback 实现条件跳过 ★
	//
	// 当 state 中 needs_risk_check == false 时，BeforeAgentCallback 直接返回
	// "skipped" 内容并将 risk_result 设为默认值，这样：
	// 1. RiskCheck agent 的 LLM 调用不会执行（节省成本和时间）
	// 2. state 中依然有 risk_result 数据（默认值 "auto_approved"）
	// 3. 下游的 MergeAndComplete 可以正常读取 risk_result
	riskCheck, _ := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:  "RiskCheck",
			Model: llmModel,
			Instruction: `你是一个风控审查 Agent。对大额订单进行风险评估。

订单分类信息：
{order_classification}

请执行以下风控检查：
1. 验证订单金额是否在合理范围
2. 检查用户信用记录
3. 进行反欺诈分析

输出风控审查结果，格式：
{"status": "approved", "risk_level": "low/medium/high", "message": "风控审查通过/不通过，原因..."}`,
			OutputKey: "risk_result",
			BeforeAgentCallbacks: []adkAgent.BeforeAgentCallback{
				conditionalSkipCallback("needs_risk_check", "risk_result",
					`{"status": "auto_approved", "risk_level": "none", "message": "小额订单，自动通过风控"}`),
			},
		},
	})

	// Step 2: Parallel execution of PaymentProcess + RiskCheck
	parallelBranch, _ := agent.NewParallelAgent(agent.Config{
		Config: adkAgent.Config{
			Name:        "ParallelProcessing",
			Description: "并行处理支付和风控审查",
			SubAgents:   []adkAgent.Agent{paymentProcess, riskCheck},
		},
	})

	// Step 3: MergeAndComplete — 汇聚节点，合并两个分支结果
	mergeAndComplete, _ := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:  "MergeAndComplete",
			Model: llmModel,
			Instruction: `你是一个订单完成 Agent。汇总支付处理和风控审查的结果，生成最终的订单处理报告。

订单分类信息：
{order_classification}

支付处理结果：
{payment_result}

风控审查结果：
{risk_result}

请生成一份清晰的订单处理报告，包含：
1. 📋 订单基本信息
2. 💳 支付处理结果
3. 🔒 风控审查结果（如果是小额订单自动通过，请注明）
4. ✅ 最终结论：订单是否处理成功

请用友好的格式输出报告。`,
			OutputKey: "final_report",
		},
	})

	// -----------------------------------------------------------------------
	// 4. Assemble pipeline: Classify → Parallel[Payment, RiskCheck] → Merge
	// -----------------------------------------------------------------------
	pipeline, _ := agent.NewSequentialAgent(agent.Config{
		Config: adkAgent.Config{
			Name:        "OrderProcessingPipeline",
			Description: "订单处理流水线：分类 → 并行[支付 + 风控] → 合并完成",
			SubAgents:   []adkAgent.Agent{classifyOrder, parallelBranch, mergeAndComplete},
		},
	})

	// -----------------------------------------------------------------------
	// 5. Launch
	// -----------------------------------------------------------------------
	config := &launcher.Config{
		AgentLoader: adkAgent.NewSingleLoader(pipeline),
		PluginConfig: runner.PluginConfig{
			Plugins: langfuseCfg.Plugins,
		},
	}

	l := full.NewLauncher()
	if err := l.Execute(ctx, config, os.Args[1:]); err != nil {
		panic(fmt.Sprintf("Run failed: %v", err))
	}
}

// ============================================================================
// conditionalSkipCallback — 条件跳过的核心实现
// ============================================================================

// conditionalSkipCallback 创建一个 BeforeAgentCallback，当 session state 中
// conditionKey 的值为 false（或包含 "false"）时，跳过当前 agent 的执行，
// 并将 defaultValue 写入 outputKey。
//
// 这是解决"并行分支汇聚 + 条件分支"问题的核心模式：
//
//  1. ParallelAgent 总是启动所有分支
//  2. 每个条件分支通过 BeforeAgentCallback 检查条件
//  3. 条件不满足时，callback 返回非 nil 内容 → agent 运行被跳过
//  4. 同时向 state 写入默认值 → 下游汇聚节点有数据可读
//
// 这样就避免了"并行分支中条件分支不执行，汇聚节点缺少数据"的问题。
func conditionalSkipCallback(conditionKey, outputKey, defaultValue string) func(adkAgent.CallbackContext) (*genai.Content, error) {
	return func(ctx adkAgent.CallbackContext) (*genai.Content, error) {
		// 从 state 中读取条件标志
		val, err := ctx.ReadonlyState().Get(conditionKey)
		if err != nil {
			// state 中没有该 key，默认不跳过
			log.Info(ctx, fmt.Sprintf("⚠️ [%s] condition key %q not found in state, proceeding normally", ctx.AgentName(), conditionKey))
			return nil, nil
		}

		// 判断是否需要跳过
		shouldSkip := false
		switch v := val.(type) {
		case bool:
			shouldSkip = !v
		case string:
			shouldSkip = (v == "false" || v == "no" || v == "0")
		default:
			log.Info(ctx, fmt.Sprintf("⚠️ [%s] condition key %q has unexpected type %T, proceeding normally", ctx.AgentName(), conditionKey, val))
			return nil, nil
		}

		if !shouldSkip {
			// 条件满足，正常执行 agent
			log.Info(ctx, fmt.Sprintf("✅ [%s] condition %q is true, executing agent", ctx.AgentName(), conditionKey))
			return nil, nil
		}

		// ★ 条件不满足，跳过 agent 并写入默认值 ★
		log.Info(ctx, fmt.Sprintf("⏭️ [%s] condition %q is false, SKIPPING agent. Writing default value to state[%q]",
			ctx.AgentName(), conditionKey, outputKey))

		// 将默认值写入 state，确保下游汇聚节点有数据
		if err := ctx.State().Set(outputKey, defaultValue); err != nil {
			return nil, fmt.Errorf("failed to set default value for %q: %w", outputKey, err)
		}

		// 返回非 nil Content → 触发 adk 框架跳过 agent 的实际运行
		return genai.NewContentFromText(fmt.Sprintf(
			"[SKIPPED] Agent %q was skipped because condition %q is false. Default value written to state[%q].",
			ctx.AgentName(), conditionKey, outputKey,
		), genai.RoleModel), nil
	}
}

// ============================================================================
// Helpers
// ============================================================================

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
