// Package agent provides a high-level factory for creating ADK agents with
// default logging callbacks. It wraps the underlying
// google.golang.org/adk/agent/llmagent package and the workflow agent packages
// into simplified NewXxxAgent calls.
//
// Trace and ContextGuard plugins are runner-level concerns and should be
// assembled by the caller when constructing the runner.PluginConfig — they are
// NOT included in the agent factory.
//
// Usage:
//
//	// 1. Create agents.
//	ag, err := agent.NewLLMAgent(agent.Config{
//	    LLMAgentConfig: llmagent.Config{ ... },
//	})
//
//	// 2. Assemble runner-level plugins separately.
//	var plugins []*plugin.Plugin
//	plugins = append(plugins, jaegerCfg.Plugins...)
//	plugins = append(plugins, guardCfg.Plugins...)
//
//	runnr, _ := runner.New(runner.Config{
//	    Agent:        ag,
//	    PluginConfig: runner.PluginConfig{Plugins: plugins},
//	})
package agent

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/plugin/trace"
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// Config is the top-level configuration for creating agents.
type Config struct {
	// LLMAgentConfig is the upstream google/adk-go llmagent.Config that defines the
	// agent's name, model, instruction, tools, sub-agents, callbacks, etc.
	// Only for google adk out of box llmagent.
	LLMAgentConfig llmagent.Config

	// Config that defines the agent's name, description, subAgents, callbacks, etc.
	// Only for google adk loopagent, parallelagent, sequentialagent.
	Config adkagent.Config

	// If MaxIterations == 0, then LoopAgent runs indefinitely or until any
	// sub-agent escalates.
	MaxIterations uint

	// DisableDefaultCallbacks determines whether use default callbacks or not.
	DisableDefaultCallbacks bool
}

// NewLLMAgent creates an ADK LLM agent from the given Config.
//
// Default log callbacks (LogBeforeModelCallback, LogAfterModelCallback,
// LogBeforeToolCallback, LogAfterToolCallback) are automatically prepended
// to the corresponding callback slices so they always fire first.
//
// Runner-level plugins (trace, context guard) should be assembled by the
// caller and passed to runner.Config.PluginConfig separately.
func NewLLMAgent(cfg Config) (adkagent.Agent, error) {
	// Prepend default log callbacks so they always fire first.
	if !cfg.DisableDefaultCallbacks {
		cfg.LLMAgentConfig.BeforeModelCallbacks = append(
			[]llmagent.BeforeModelCallback{LogBeforeModelCallback},
			cfg.LLMAgentConfig.BeforeModelCallbacks...,
		)
		cfg.LLMAgentConfig.AfterModelCallbacks = append(
			[]llmagent.AfterModelCallback{LogAfterModelCallback},
			cfg.LLMAgentConfig.AfterModelCallbacks...,
		)
		cfg.LLMAgentConfig.BeforeToolCallbacks = append(
			[]llmagent.BeforeToolCallback{LogBeforeToolCallback},
			cfg.LLMAgentConfig.BeforeToolCallbacks...,
		)
		cfg.LLMAgentConfig.AfterToolCallbacks = append(
			[]llmagent.AfterToolCallback{LogAfterToolCallback},
			cfg.LLMAgentConfig.AfterToolCallbacks...,
		)
	}

	ag, err := llmagent.New(cfg.LLMAgentConfig)
	if err != nil {
		return nil, fmt.Errorf("agent: create llm agent: %w", err)
	}
	return ag, nil
}

// NewLoopAgent creates a LoopAgent.
//
// LoopAgent repeatedly runs its sub-agents in sequence for a specified number
// of iterations or until a termination condition is met.
//
// Use the LoopAgent when your workflow involves repetition or iterative
// refinement, such as like revising code.
func NewLoopAgent(cfg Config) (adkagent.Agent, error) {
	a, err := loopagent.New(loopagent.Config{
		AgentConfig:   cfg.Config,
		MaxIterations: cfg.MaxIterations,
	})
	if err != nil {
		log.Errorf("create loop agent fail", log.String("error", err.Error()))
		return nil, err
	}
	return a, nil
}

// NewParallelAgent creates a ParallelAgent.
//
// Parallel agent runs its sub-agents in parallel in isolated manner.
//
// This approach is beneficial for scenarios requiring multiple perspectives or
// attempts on a single task, such as:
// - Running different algorithms simultaneously.
// - Generating multiple responses for review by a subsequent evaluation agent.
func NewParallelAgent(cfg Config) (adkagent.Agent, error) {
	a, err := parallelagent.New(parallelagent.Config{
		AgentConfig: cfg.Config,
	})
	if err != nil {
		log.Errorf("create parallel agent fail", log.String("error", err.Error()))
		return nil, err
	}
	return a, nil
}

// NewSequentialAgent creates a SequentialAgent.
//
// SequentialAgent executes its sub-agents once, in the order they are listed.
//
// Use the SequentialAgent when you want the execution to occur in a fixed,
// strict order.
func NewSequentialAgent(cfg Config) (adkagent.Agent, error) {
	a, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: cfg.Config,
	})
	if err != nil {
		log.Errorf("create sequential agent fail", log.String("error", err.Error()))
		return nil, err
	}
	return a, nil
}

// LogBeforeModelCallback 打印模型调用前输入
func LogBeforeModelCallback(ctx adkagent.CallbackContext, llmRequest *model.LLMRequest) (*model.LLMResponse, error) {
	log.Debug(ctx, "model request info",
		log.String("trace_id", trace.TraceIDFromContext(ctx)),
		log.String("invocation_id", ctx.InvocationID()),
		log.Any("system_instruction", llmRequest.Config.SystemInstruction),
		log.Any("content", llmRequest.Contents),
		log.String("model", llmRequest.Model),
	)
	return nil, nil
}

// LogAfterModelCallback 打印模型调用后输入
func LogAfterModelCallback(ctx adkagent.CallbackContext, llmResponse *model.LLMResponse, llmResponseError error) (*model.LLMResponse, error) {
	log.Debug(ctx, "model call reply",
		log.String("trace_id", trace.TraceIDFromContext(ctx)),
		log.String("invocation_id", ctx.InvocationID()),
		log.Any("model_reply", llmResponse),
	)
	return nil, nil
}

// LogAfterToolCallback 打印工具调用结束日志
func LogAfterToolCallback(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
	log.Debug(ctx, "tool call reply info",
		log.String("name", t.Name()),
		log.String("description", t.Description()),
		log.Any("args", args),
		log.Any("result", result),
	)
	if err != nil {
		log.Error(ctx, "❌:tool call fail", log.String("error", err.Error()))
	}
	return result, err
}

// LogBeforeToolCallback 打印工具调用前日志
func LogBeforeToolCallback(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
	log.Debug(ctx, "tool call request info",
		log.String("name", t.Name()),
		log.String("description", t.Description()),
		log.Any("args", args),
	)
	return nil, nil
}
