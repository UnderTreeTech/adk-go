package trace

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

var (
	noopOnce       sync.Once
	noopPluginCfg  runner.PluginConfig
	noopShutdownFn func(context.Context) error
)

// SetupNoop initialises a minimal OTel TracerProvider that records spans
// (ensuring TraceIDFromContext returns valid IDs) but does NOT export them
// to any backend. It also registers an ADK plugin with noop before/after
// agent and model callbacks to maintain span context propagation.
//
// SetupNoop is idempotent — repeated calls return the same PluginConfig
// and shutdown function without reinitialising the provider.
//
// This is automatically called by the agent package when neither Langfuse
// nor Jaeger plugin configs are provided, guaranteeing that trace IDs are
// always available for logging.
func SetupNoop() (runner.PluginConfig, func(context.Context) error) {
	noopOnce.Do(func() {
		// Create a TracerProvider with AlwaysSample but no exporter.
		// Spans are created (so SpanContext / TraceID are populated) but
		// never exported anywhere.
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
		)
		otel.SetTracerProvider(tp)

		// Create a minimal ADK plugin that does nothing but ensures the
		// plugin hook points are wired (some ADK internals rely on plugin
		// presence for span creation).
		plug, _ := plugin.New(plugin.Config{
			Name:                "noop-trace",
			BeforeAgentCallback: agent.BeforeAgentCallback(noopBeforeAgent),
			AfterAgentCallback:  agent.AfterAgentCallback(noopAfterAgent),
			BeforeModelCallback: llmagent.BeforeModelCallback(noopBeforeModel),
			AfterModelCallback:  llmagent.AfterModelCallback(noopAfterModel),
		})

		noopPluginCfg = runner.PluginConfig{Plugins: []*plugin.Plugin{plug}}
		noopShutdownFn = func(ctx context.Context) error {
			return tp.Shutdown(ctx)
		}
	})
	return noopPluginCfg, noopShutdownFn
}

// noopBeforeAgent is a no-op BeforeAgentCallback.
func noopBeforeAgent(_ agent.CallbackContext) (*genai.Content, error) {
	return nil, nil
}

// noopAfterAgent is a no-op AfterAgentCallback.
func noopAfterAgent(_ agent.CallbackContext) (*genai.Content, error) {
	return nil, nil
}

// noopBeforeModel is a no-op BeforeModelCallback.
func noopBeforeModel(_ agent.CallbackContext, _ *model.LLMRequest) (*model.LLMResponse, error) {
	return nil, nil
}

// noopAfterModel is a no-op AfterModelCallback.
func noopAfterModel(_ agent.CallbackContext, _ *model.LLMResponse, _ error) (*model.LLMResponse, error) {
	return nil, nil
}
