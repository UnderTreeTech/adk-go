package trace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.36.0"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	adktelemetry "google.golang.org/adk/telemetry"
)

// SetupConfig holds the common configuration for setting up a trace-enrichment
// plugin backed by an OTLP/HTTP exporter.
type SetupConfig struct {
	// PluginName is the name used when registering the ADK plugin.
	PluginName string

	// ExporterOpts are the OTLP/HTTP exporter options (endpoint, headers,
	// insecure, etc.) — already configured by the caller.
	ExporterOpts []otlptracehttp.Option

	// ServiceName is the OTel service.name resource attribute.
	ServiceName string

	// Environment is an optional deployment environment tag.
	Environment string

	// Mapper is the backend-specific attribute mapper.
	Mapper AttributeMapper

	// ErrorPrefix is prepended to error messages (e.g. "jaeger", "langfuse").
	ErrorPrefix string
}

// Setup initialises a full trace-enrichment integration: an OTLP/HTTP
// exporter, an enriching span exporter, and an ADK plugin.
//
// It returns a runner.PluginConfig and a shutdown function. The caller must
// defer the shutdown function.
func Setup(cfg SetupConfig) (runner.PluginConfig, func(context.Context) error, error) {
	exporter, err := otlptracehttp.New(context.Background(), cfg.ExporterOpts...)
	if err != nil {
		return runner.PluginConfig{}, nil, fmt.Errorf("%s: create OTLP exporter: %w", cfg.ErrorPrefix, err)
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String(cfg.ServiceName),
	}
	if cfg.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(cfg.Environment))
	}
	res, err := resource.New(context.Background(), resource.WithAttributes(attrs...))
	if err != nil {
		return runner.PluginConfig{}, nil, fmt.Errorf("%s: create OTel resource: %w", cfg.ErrorPrefix, err)
	}

	enricher := NewSpanEnricher(cfg.Mapper)
	wrapped := &EnrichingExporter{Inner: exporter, Enricher: enricher}

	providers, err := adktelemetry.New(context.Background(),
		adktelemetry.WithSpanProcessors(sdktrace.NewBatchSpanProcessor(wrapped)),
		adktelemetry.WithResource(res),
	)
	if err != nil {
		return runner.PluginConfig{}, nil, fmt.Errorf("%s: create ADK telemetry providers: %w", cfg.ErrorPrefix, err)
	}
	providers.SetGlobalOtelProviders()

	plug, _ := plugin.New(plugin.Config{
		Name:                cfg.PluginName,
		BeforeAgentCallback: agent.BeforeAgentCallback(enricher.BeforeAgent),
		AfterAgentCallback:  agent.AfterAgentCallback(enricher.AfterAgent),
		BeforeModelCallback: llmagent.BeforeModelCallback(enricher.BeforeModel),
		AfterModelCallback:  llmagent.AfterModelCallback(enricher.AfterModel),
	})

	pluginCfg := runner.PluginConfig{Plugins: []*plugin.Plugin{plug}}
	return pluginCfg, providers.Shutdown, nil
}
