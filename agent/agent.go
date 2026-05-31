// Package agent provides a high-level factory for creating ADK agents with
// optional ContextGuard, Langfuse and Jaeger plugin integration. It wraps the
// underlying google.golang.org/adk/agent/llmagent package and the fork's
// plugin packages into a single NewAgent call.
//
// Design principles:
//   - Langfuse is initialized once externally and passed in as a PluginConfig;
//     NewAgent merely includes it in the merged output.
//   - ContextGuard is initialized per-agent inside NewAgent, configured via
//     simple scalar fields in ContextGuardConfig.
//   - Neither plugin's internal Config struct is directly exposed to callers.
//
// Usage:
//
//	// 1. Initialize Langfuse once at application startup.
//	langfuseCfg, langfuseShutdown, _ := langfuse.Setup(&langfuse.Config{
//	    PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
//	    SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
//	})
//	defer langfuseShutdown(ctx)
//
//	// 2. Create agents — Langfuse PluginConfig is shared, ContextGuard is per-agent.
//	result, err := agent.NewAgent(agent.Config{
//	    LLMAgent:             llmagentCfg,
//	    EnableLangfuse:       true,
//	    LangfusePluginConfig: &langfuseCfg,
//	    ContextGuard: &agent.ContextGuardConfig{
//	        Strategy: agent.StrategySlidingWindow,
//	        MaxTurns: 30,
//	    },
//	})
//
//	runnr, _ := runner.New(runner.Config{
//	    Agent:        result.Agent,
//	    PluginConfig: result.PluginConfig,
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
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool"

	"github.com/UnderTreeTech/adk-go/plugin/contextguard"
)

// Strategy constants for ContextGuard configuration.
const (
	// StrategySlidingWindow compacts when the number of content entries exceeds
	// MaxTurns, regardless of token count. This is the default strategy.
	StrategySlidingWindow = contextguard.StrategySlidingWindow

	// StrategyThreshold compacts when estimated token usage approaches the
	// model's context window limit.
	StrategyThreshold = contextguard.StrategyThreshold
)

// Config is the top-level configuration for NewAgent.
type Config struct {
	// LLMAgentConfig is the upstream google/adk-go llmagent.Config that defines the
	// agent's name, model, instruction, tools, sub-agents, callbacks, etc.
	// only for google adk out of box llmagent.
	LLMAgentConfig llmagent.Config

	// Config that defines the agent's name, description, subAgents, callbacks, etc.
	// only for google adk loopagent, parallelagent, sequentialagent.
	Config adkagent.Config

	// If MaxIterations == 0, then LoopAgent runs indefinitely or until any
	// sub-agent escalates.
	MaxIterations uint

	// LangfusePluginConfig is the pre-initialized Langfuse runner.PluginConfig
	// obtained from langfuse.Setup(). It is shared across all agents — there
	// is no need to call langfuse.Setup() per agent. This field is only used
	// when EnableLangfuse is true.
	LangfusePluginConfig *runner.PluginConfig

	// JaegerPluginConfig is the pre-initialized Jaeger runner.PluginConfig
	// obtained from jaeger.Setup(). It is shared across all agents — there
	// is no need to call jaeger.Setup() per agent. When non-nil, the Jaeger
	// enrichment plugin is included in the merged PluginConfig. This is
	// mutually exclusive with LangfusePluginConfig — only one tracing
	// backend should be active at a time.
	JaegerPluginConfig *runner.PluginConfig

	// ContextGuard configures the context-window management plugin. When
	// non-nil, a new ContextGuard instance is created for this agent to ensure
	// per-agent isolation of compaction state and strategy. When nil, no
	// context guard is applied.
	ContextGuard *ContextGuardConfig
}

// ContextGuardConfig defines how the ContextGuard plugin behaves for this agent.
// It does not expose the underlying contextguard package types directly.
type ContextGuardConfig struct {
	// Strategy selects the compaction strategy. Supported values:
	//   - StrategySlidingWindow (default): compacts when turn count exceeds MaxTurns.
	//   - StrategyThreshold: compacts when token usage approaches the context window.
	// When empty, defaults to StrategySlidingWindow.
	Strategy string

	// MaxTurns is the maximum number of content entries before compaction fires.
	// Only used when Strategy is StrategySlidingWindow. Defaults to 20 when <= 0.
	MaxTurns int

	// MaxTokens overrides the model's context window size (in tokens).
	// Only used when Strategy is StrategyThreshold. When <= 0, the value is
	// looked up from the ModelRegistry.
	MaxTokens int

	// MaxCompactionAttempts sets how many summarization retries are allowed
	// when a single compaction pass still exceeds the threshold. Defaults to 3
	// when <= 0. Applies to both strategies.
	MaxCompactionAttempts int

	// Registry provides model metadata (context window sizes, default max
	// tokens). When nil, a CrushRegistry is created automatically using
	// catwalk's embedded model database.
	Registry contextguard.ModelRegistry
}

// buildContextGuardPlugins creates a new ContextGuard instance for the agent,
// translating the user-facing ContextGuardConfig into the underlying
// contextguard package calls.
func buildContextGuardPlugins(agentName string, llm model.LLM, cfg *ContextGuardConfig) ([]*plugin.Plugin, error) {
	registry := cfg.Registry
	if registry == nil {
		registry = contextguard.NewCrushRegistry()
	}

	guard := contextguard.New(registry)

	// Build per-agent options based on the simplified config.
	var opts []contextguard.AgentOption

	strategy := cfg.Strategy
	if strategy == "" {
		strategy = StrategySlidingWindow
	}

	switch strategy {
	case StrategySlidingWindow:
		maxTurns := cfg.MaxTurns
		if maxTurns <= 0 {
			maxTurns = 20
		}
		opts = append(opts, contextguard.WithSlidingWindow(maxTurns))
	case StrategyThreshold:
		if cfg.MaxTokens > 0 {
			opts = append(opts, contextguard.WithMaxTokens(cfg.MaxTokens))
		}
	default:
		return nil, fmt.Errorf("agent: unsupported context guard strategy: %q", strategy)
	}

	if cfg.MaxCompactionAttempts > 0 {
		opts = append(opts, contextguard.WithMaxCompactionAttempts(cfg.MaxCompactionAttempts))
	}

	guard.Add(agentName, llm, opts...)

	guardCfg := guard.PluginConfig()
	return guardCfg.Plugins, nil
}

// BundledAgent bundles everything returned by NewAgent.
type BundledAgent struct {
	// Agent is the created ADK agent, ready to pass to runner.Config.Agent.
	Agent adkagent.Agent

	// PluginConfig is the combined plugin config for ContextGuard and/or
	// Langfuse, ready to pass to runner.Config.PluginConfig.
	PluginConfig runner.PluginConfig
}

// buildPlugins build default agent plugins.
func buildPlugins(cfg Config) ([]*plugin.Plugin, error) {
	var allPlugins []*plugin.Plugin

	// 1. Langfuse plugin — include if enabled and config provided.
	if cfg.LangfusePluginConfig != nil {
		allPlugins = append(allPlugins, cfg.LangfusePluginConfig.Plugins...)
	}

	// 2. Jaeger plugin — include if enabled and config provided.
	if cfg.JaegerPluginConfig != nil {
		allPlugins = append(allPlugins, cfg.JaegerPluginConfig.Plugins...)
	}

	// 3. Noop trace plugin — when no trace backend is configured, initialise
	// a noop TracerProvider so that TraceIDFromContext always returns a valid ID.
	if cfg.LangfusePluginConfig == nil && cfg.JaegerPluginConfig == nil {
		noopCfg, _ := trace.SetupNoop()
		allPlugins = append(allPlugins, noopCfg.Plugins...)
	}

	// 4. ContextGuard plugin — create per-agent instance.
	if cfg.ContextGuard != nil {
		guardPlugins, err := buildContextGuardPlugins(cfg.LLMAgentConfig.Name, cfg.LLMAgentConfig.Model, cfg.ContextGuard)
		if err != nil {
			return nil, err
		}
		allPlugins = append(allPlugins, guardPlugins...)
	}

	return allPlugins, nil
}

// NewLLMAgent creates an ADK agent from the given Config, optionally including
// ContextGuard and Langfuse plugins in the returned PluginConfig.
//
// Key design choices:
//   - Langfuse is initialized ONCE externally (via langfuse.Setup) and the
//     resulting runner.PluginConfig is passed in. NewAgent merely includes
//     or excludes it based on EnableLangfuse.
//   - ContextGuard is initialized PER-AGENT inside NewAgent, ensuring each
//     agent has its own compaction state and strategy configuration. Different
//     users' sessions are isolated by the ADK session state mechanism (keys
//     are scoped by agent name), but having a per-agent ContextGuard instance
//     ensures clean separation of strategy objects and model registry lookups.
//   - ContextGuard defaults to the sliding_window strategy with 20 turns.
func NewLLMAgent(cfg Config) (*BundledAgent, error) {
	// 0. Prepend default log callbacks so they always fire first.
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

	// 1. Create the upstream LLM agent.
	ag, err := llmagent.New(cfg.LLMAgentConfig)
	if err != nil {
		return nil, fmt.Errorf("agent: create llm agent: %w", err)
	}

	// 2. Build default plugins.
	allPlugins, err := buildPlugins(cfg)
	if err != nil {
		log.Errorf("build default plugins fail", log.String("error", err.Error()))
		return nil, err
	}

	return &BundledAgent{
		Agent: ag,
		PluginConfig: runner.PluginConfig{
			Plugins: allPlugins,
		},
	}, nil
}

// NewLoopAgent creates a LoopAgent.
//
// LoopAgent repeatedly runs its sub-agents in sequence for a specified number
// of iterations or until a termination condition is met.
//
// Use the LoopAgent when your workflow involves repetition or iterative
// refinement, such as like revising code.
func NewLoopAgent(cfg Config) (*BundledAgent, error) {
	// 1. Create the upstream LLM agent.
	a, err := loopagent.New(loopagent.Config{
		AgentConfig:   cfg.Config,
		MaxIterations: cfg.MaxIterations,
	})
	if err != nil {
		log.Errorf("create loop agent fail", log.String("error", err.Error()))
		return nil, err
	}

	// 2. Build default plugins.
	allPlugins, err := buildPlugins(cfg)
	if err != nil {
		log.Errorf("build default plugins fail", log.String("error", err.Error()))
		return nil, err
	}

	return &BundledAgent{
		Agent: a,
		PluginConfig: runner.PluginConfig{
			Plugins: allPlugins,
		},
	}, nil
}

// NewParallelAgent creates a ParallelAgent.
//
// Parallel agent runs its sub-agents in parallel in isolated manner.
//
// This approach is beneficial for scenarios requiring multiple perspectives or
// attempts on a single task, such as:
// - Running different algorithms simultaneously.
// - Generating multiple responses for review by a subsequent evaluation agent.
func NewParallelAgent(cfg Config) (*BundledAgent, error) {
	// 1. Create the upstream LLM agent.
	a, err := parallelagent.New(parallelagent.Config{
		AgentConfig: cfg.Config,
	})
	if err != nil {
		log.Errorf("create parallel agent fail", log.String("error", err.Error()))
		return nil, err
	}

	// 2. Build default plugins.
	allPlugins, err := buildPlugins(cfg)
	if err != nil {
		log.Errorf("build default plugins fail", log.String("error", err.Error()))
		return nil, err
	}

	return &BundledAgent{
		Agent: a,
		PluginConfig: runner.PluginConfig{
			Plugins: allPlugins,
		},
	}, nil
}

// NewSequentialAgent creates a SequentialAgent.
//
// SequentialAgent executes its sub-agents once, in the order they are listed.
//
// Use the SequentialAgent when you want the execution to occur in a fixed,
// strict order.
func NewSequentialAgent(cfg Config) (*BundledAgent, error) {
	// 1. Create the upstream LLM agent.
	a, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: cfg.Config,
	})
	if err != nil {
		log.Errorf("create sequential agent fail", log.String("error", err.Error()))
		return nil, err
	}

	// 2. Build default plugins.
	allPlugins, err := buildPlugins(cfg)
	if err != nil {
		log.Errorf("build default plugins fail", log.String("error", err.Error()))
		return nil, err
	}

	return &BundledAgent{
		Agent: a,
		PluginConfig: runner.PluginConfig{
			Plugins: allPlugins,
		},
	}, nil
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
		log.String("jaeger_trace_id", trace.TraceIDFromContext(ctx)),
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
