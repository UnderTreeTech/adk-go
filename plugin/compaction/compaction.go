// Package compaction implements an ADK plugin that prevents conversations
// from exceeding the LLM's context window. Before every model call it
// delegates to a configurable Strategy that decides whether and how to
// compact the conversation history.
//
// Two strategies are provided out of the box:
//
//   - ThresholdStrategy: estimates token count and summarizes when the
//     remaining capacity drops below a safety buffer (two-tier: fixed 20k
//     for large windows, 20% for small ones). This is a reactive guard.
//
//   - SlidingWindowStrategy: compacts when the number of Content entries
//     exceeds a configured maximum, regardless of token count. This is a
//     preventive, periodic compaction based on turn count.
//
// Both strategies use the agent's own LLM for summarization and share the
// same structured system prompt, state keys, and helper functions.
//
// Usage:
//
//	guard := contextguard.New(registry)
//	guard.Add("assistant", llmModel)
//	guard.Add("researcher", llmResearcher, contextguard.WithSlidingWindow(30))
//
//	runnr, _ := runner.New(runner.Config{
//	    Agent:        myAgent,
//	    PluginConfig: guard.PluginConfig(),
//	})
package compaction

import (
	"github.com/UnderTreeTech/waterdrop/pkg/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

const (
	// StrategyThreshold selects the token-threshold strategy: summarization
	// fires when estimated token usage approaches the model's context window.
	StrategyThreshold = "threshold"

	// StrategySlidingWindow selects the sliding-window strategy: summarization
	// fires when the number of Content entries exceeds a configured limit.
	StrategySlidingWindow = "sliding_window"
)

const (
	stateKeyPrefixSummary              = "__context_guard_summary_"
	stateKeyPrefixSummarizedAt         = "__context_guard_summarized_at_"
	stateKeyPrefixContentsAtCompaction = "__context_guard_contents_at_compaction_"
	stateKeyPrefixRealTokens           = "__context_guard_real_tokens_"
	stateKeyPrefixLastHeuristic        = "__context_guard_last_heuristic_"

	largeContextWindowThreshold = 200_000
	largeContextWindowBuffer    = 20_000
	smallContextWindowRatio     = 0.20

	// defaultMaxCompactionAttempts defines the maximum number of compaction
	// passes performed in a single turn. If the first summary still exceeds
	// the threshold, the compacted content is summarized again until it fits
	// or the limit is reached, preventing infinite loops.
	defaultMaxCompactionAttempts = 3

	// defaultHeuristicCorrectionFactor is applied to the len/4 heuristic
	// when no real token data is available for calibration. The value 2.5
	// accounts for the typical 2-3x underestimation of len/4 on structured
	// content (JSON tool schemas, markdown, non-ASCII, overhead from tool
	// definitions and system prompts that tokenize denser than plain text).
	// This ensures compaction fires early enough even without provider
	// token counts.
	defaultHeuristicCorrectionFactor = 2.5

	// maxCorrectionFactor caps the calibration ratio to prevent a single
	// turn with unusual content (e.g. JSON-heavy tool schemas) from
	// producing a disproportionate correction that persists across turns.
	maxCorrectionFactor = 5.0
)

const defaultMaxTurns = 20

// Strategy defines how a compaction algorithm decides whether and how to
// compact conversation history before an LLM call.
type Strategy interface {
	Name() string
	Compact(ctx agent.CallbackContext, req *model.LLMRequest) error
}

// AgentOption configures per-agent behavior when calling Add.
type AgentOption func(*agentConfig)

type agentConfig struct {
	strategy              string
	maxTurns              int
	maxTokens             int
	maxCompactionAttempts int
}

// WithSlidingWindow selects the sliding-window strategy with the given
// maximum number of Content entries before compaction.
func WithSlidingWindow(maxTurns int) AgentOption {
	return func(c *agentConfig) {
		c.strategy = StrategySlidingWindow
		c.maxTurns = maxTurns
	}
}

// WithMaxTokens sets a manual context window size override (in tokens).
// Only used by the threshold strategy. When set, the ModelRegistry is
// bypassed for this agent.
func WithMaxTokens(maxTokens int) AgentOption {
	return func(c *agentConfig) {
		c.maxTokens = maxTokens
	}
}

// WithMaxCompactionAttempts sets the maximum number of summarization retries
// when a single compaction pass still exceeds the threshold. Applies to both
// strategies. Defaults to 3 when not set or when n <= 0.
func WithMaxCompactionAttempts(n int) AgentOption {
	return func(c *agentConfig) {
		c.maxCompactionAttempts = n
	}
}

// ContextGuard accumulates per-agent strategies and produces a single
// runner.PluginConfig. Use New to create one, Add to register agents,
// and PluginConfig to get the final configuration.
type ContextGuard struct {
	registry   ModelRegistry
	strategies map[string]Strategy
}

// New creates a ContextGuard backed by the given ModelRegistry.
func New(registry ModelRegistry) *ContextGuard {
	return &ContextGuard{
		registry:   registry,
		strategies: make(map[string]Strategy),
	}
}

// Add registers an agent with its LLM for summarization. Without options,
// the threshold strategy is used with limits from the ModelRegistry.
func (g *ContextGuard) Add(agentID string, llm model.LLM, opts ...AgentOption) {
	cfg := &agentConfig{
		strategy: StrategyThreshold,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	maxCompactionAttempts := cfg.maxCompactionAttempts
	if maxCompactionAttempts <= 0 {
		maxCompactionAttempts = defaultMaxCompactionAttempts
	}

	switch cfg.strategy {
	case StrategySlidingWindow:
		maxTurns := cfg.maxTurns
		if maxTurns <= 0 {
			maxTurns = defaultMaxTurns
		}
		g.strategies[agentID] = newSlidingWindowStrategy(g.registry, llm, maxTurns, maxCompactionAttempts)
	default:
		g.strategies[agentID] = newThresholdStrategy(g.registry, llm, cfg.maxTokens, maxCompactionAttempts)
	}

	log.Infof("ContextGuard: strategy configured",
		log.String("agent", agentID),
		log.String("strategy", g.strategies[agentID].Name()),
	)
}

// PluginConfig returns a runner.PluginConfig ready to pass to the ADK
// launcher or runner.
func (g *ContextGuard) PluginConfig() runner.PluginConfig {
	guard := &contextGuard{strategies: g.strategies}

	p, _ := plugin.New(plugin.Config{
		Name:                "context_guard",
		BeforeModelCallback: llmagent.BeforeModelCallback(guard.beforeModel),
		AfterModelCallback:  llmagent.AfterModelCallback(guard.afterModel),
	})

	return runner.PluginConfig{
		Plugins: []*plugin.Plugin{p},
	}
}

// contextGuard is the internal state of the plugin, holding per-agent
// strategies keyed by agent ID.
type contextGuard struct {
	strategies map[string]Strategy
}

// beforeModel is the BeforeModelCallback invoked by ADK before every LLM
// call. It looks up the agent's strategy and delegates compaction to it.
// After compaction (or pass-through), it persists the heuristic token
// estimate of the final request so that the next call can compute a
// calibration factor between real and heuristic counts.
func (g *contextGuard) beforeModel(ctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil || len(req.Contents) == 0 {
		return nil, nil
	}

	strategy, ok := g.strategies[ctx.AgentName()]
	if !ok {
		return nil, nil
	}

	if err := strategy.Compact(ctx, req); err != nil {
		log.Warn(ctx, "ContextGuard: compaction failed, passing through",
			log.String("agent", ctx.AgentName()),
			log.String("strategy", strategy.Name()),
			log.String("error", err.Error()),
		)
	}

	persistLastHeuristic(ctx, estimateTokens(req))

	return nil, nil
}

// afterModel is the AfterModelCallback invoked by ADK after every LLM
// response, including streaming partials. Only the final (non-partial)
// response carries UsageMetadata, so partials are skipped.
//
// Only PromptTokenCount is stored: it reflects the full conversation size
// that the LLM received, which is the value to compare against the context
// window threshold. CandidatesTokenCount (the model's output) will become
// part of the next turn's prompt automatically.
func (g *contextGuard) afterModel(ctx agent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
	if resp == nil || resp.Partial {
		return nil, nil
	}
	if resp.UsageMetadata == nil {
		return nil, nil
	}
	if _, ok := g.strategies[ctx.AgentName()]; !ok {
		return nil, nil
	}
	promptTokens := int(resp.UsageMetadata.PromptTokenCount)
	if promptTokens > 0 {
		persistRealTokens(ctx, promptTokens)
	}
	return nil, nil
}

// ModelRegistry provides model metadata needed by the ContextGuard plugin.
// Implementations can fetch data from a remote source, a local config, or
// a static map — the plugin only depends on this interface.
type ModelRegistry interface {
	// ContextWindow returns the maximum context window size (in tokens) for
	// the given model ID. If the model is unknown, a reasonable default
	// should be returned (e.g. 128000).
	ContextWindow(modelID string) int

	// DefaultMaxTokens returns the default maximum output tokens for the
	// given model ID. If the model is unknown, a reasonable default should
	// be returned (e.g. 4096).
	DefaultMaxTokens(modelID string) int
}

const (
	crushDefaultCtxWindow = 128000
	crushDefaultMaxTokens = 4096
)

// CrushRegistry implements ModelRegistry using catwalk's embedded model
// database. All model metadata (context windows, max tokens, costs) is
// compiled into the binary — no network calls, no background goroutines.
//
// Usage:
//
//	registry := contextguard.NewCrushRegistry()
//	guard := contextguard.New(registry)
type CrushRegistry struct {
	models map[string]catwalk.Model
}

// NewCrushRegistry creates a registry pre-loaded with every model from
// catwalk's embedded provider database.
func NewCrushRegistry() *CrushRegistry {
	models := make(map[string]catwalk.Model)
	for _, provider := range embedded.GetAll() {
		for _, m := range provider.Models {
			models[m.ID] = m
		}
	}

	log.Infof("CrushRegistry: loaded models from catwalk", log.Int("count", len(models)))

	return &CrushRegistry{models: models}
}

// ContextWindow returns the context window size (in tokens) for the given
// model ID. Returns 128000 if the model is not found.
func (r *CrushRegistry) ContextWindow(modelID string) int {
	if m, ok := r.models[modelID]; ok && m.ContextWindow > 0 {
		return int(m.ContextWindow)
	}
	return crushDefaultCtxWindow
}

// DefaultMaxTokens returns the default max output tokens for the given
// model ID. Returns 4096 if the model is not found.
func (r *CrushRegistry) DefaultMaxTokens(modelID string) int {
	if m, ok := r.models[modelID]; ok && m.DefaultMaxTokens > 0 {
		return int(m.DefaultMaxTokens)
	}
	return crushDefaultMaxTokens
}
