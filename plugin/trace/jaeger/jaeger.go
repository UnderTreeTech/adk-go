// Package jaeger wires ADK Go telemetry to a Jaeger instance via OTLP.
//
// Jaeger v1.35+ natively supports OTLP ingestion on port 4317 (gRPC) and
// 4318 (HTTP), so this package uses standard OpenTelemetry OTLP exporters
// without any Jaeger-specific client library.
//
// It leverages the shared plugin/trace base package for span enrichment and
// provides a Jaeger-specific AttributeMapper. An ADK plugin captures LLM
// request/response payloads and injects them as span attributes so that
// Jaeger displays full agent execution traces with model I/O details.
//
// The plugin is safe for multi-agent flows: single agents, sequential
// delegation (transfer_to_agent), SequentialAgent, LoopAgent and ParallelAgent
// are all supported.
package jaeger

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"

	"github.com/UnderTreeTech/adk-go/plugin/trace"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
)

// defaultEndpoint is the default Jaeger OTLP HTTP endpoint used when
// Config.Endpoint is empty.
const defaultEndpoint = "http://localhost:4318/v1/traces"

// defaultServiceName is used as the OTel service name when Config.ServiceName
// is not provided.
const defaultServiceName = "adk-agent"

// Setup initialises the full Jaeger integration: an OTLP exporter pointed at
// the Jaeger collector's OTLP endpoint, an enriching span exporter that
// injects LLM request/response payloads, and an ADK plugin that captures them.
//
// It returns a runner.PluginConfig ready to pass to the ADK launcher/runner
// and a shutdown function that flushes pending spans. The caller must defer
// the shutdown function.
//
// Usage:
//
//	pluginCfg, shutdown, err := jaeger.Setup(&jaeger.Config{
//	    Endpoint:    "http://localhost:4318/v1/traces",
//	    ServiceName: "my-agent",
//	    Insecure:    true,
//	})
//	if err != nil { log.Fatal(err) }
//	defer shutdown(context.Background())
//
//	runnr, _ := runner.New(runner.Config{
//	    Agent:        myAgent,
//	    PluginConfig: pluginCfg,
//	})
func Setup(cfg *Config) (runner.PluginConfig, func(context.Context) error, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	exporterOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint),
	}
	if cfg.Insecure {
		exporterOpts = append(exporterOpts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		exporterOpts = append(exporterOpts, otlptracehttp.WithHeaders(cfg.Headers))
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = defaultServiceName
	}

	return trace.Setup(trace.SetupConfig{
		PluginName:   "jaeger-enrichment",
		ExporterOpts: exporterOpts,
		ServiceName:  serviceName,
		Environment:  cfg.Environment,
		Mapper:       &jaegerMapper{},
		ErrorPrefix:  "jaeger",
	})
}

// TraceIDFromContext extracts the current trace ID from the context. This is
// the same ID visible in the Jaeger UI trace detail page.
//
// Returns "" if no active span exists in the context or the span has no valid
// trace ID.
func TraceIDFromContext(ctx context.Context) string {
	return trace.TraceIDFromContext(ctx)
}

// ---------------------------------------------------------------------------
// jaegerMapper implements trace.AttributeMapper for Jaeger.
// ---------------------------------------------------------------------------

type jaegerMapper struct{}

func (m *jaegerMapper) AgentInputKey() string     { return "adk.agent.input" }
func (m *jaegerMapper) AgentOutputKeys() []string { return []string{"adk.agent.output"} }
func (m *jaegerMapper) LLMRequestKey() string     { return "gen_ai.request.body" }
func (m *jaegerMapper) LLMResponseKey() string    { return "gen_ai.response.body" }
func (m *jaegerMapper) ModelKey() string           { return "gen_ai.request.model" }
func (m *jaegerMapper) InputTokensKey() string     { return "gen_ai.usage.input_tokens" }
func (m *jaegerMapper) OutputTokensKey() string    { return "gen_ai.usage.output_tokens" }
func (m *jaegerMapper) TotalTokensKey() string     { return "gen_ai.usage.total_tokens" }

func (m *jaegerMapper) BeforeAgentAttrs(ctx agent.CallbackContext) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if userID := ctx.UserID(); userID != "" {
		attrs = append(attrs, attribute.String("adk.user.id", userID))
	}
	if sessionID := ctx.SessionID(); sessionID != "" {
		attrs = append(attrs, attribute.String("adk.session.id", sessionID))
	}
	return attrs
}
