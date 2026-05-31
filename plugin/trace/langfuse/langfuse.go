// Package langfuse wires ADK Go telemetry to a Langfuse instance via OTLP/HTTP.
//
// It is fully self-contained — it has zero imports from any host application.
// Any project using the ADK can import this package as a library, call
// Setup to configure the exporter and get back the plugin config and a
// shutdown function.
//
// It leverages the shared plugin/trace base package for span enrichment and
// provides a Langfuse-specific AttributeMapper with additional context helpers
// for user IDs, tags, metadata, environment, and release.
//
// The plugin is safe for multi-agent flows: single agents, sequential
// delegation (transfer_to_agent), SequentialAgent, LoopAgent and ParallelAgent
// are all supported. Parallel branches are isolated via the ADK Branch()
// identifier so that concurrent sub-agents never mix their spans or LLM
// payloads.
package langfuse

import (
	"context"
	"encoding/base64"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"

	"github.com/UnderTreeTech/adk-go/plugin/trace"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
)

// defaultHost is the Langfuse Cloud US endpoint used when Config.Host is empty.
const defaultHost = "https://cloud.langfuse.com"

// defaultServiceName is used as the OTel service name when Config.ServiceName
// is not provided.
const defaultServiceName = "langfuse-adk"

// Setup initialises the full Langfuse integration: an OTLP/HTTP trace exporter
// pointed at the Langfuse ingestion endpoint, an enriching span exporter that
// injects LLM request/response payloads, and an ADK plugin that captures them.
//
// It returns a runner.PluginConfig ready to pass to the ADK launcher/runner
// and a shutdown function that flushes pending spans. The caller must defer
// the shutdown function.
//
// Usage:
//
//	pluginCfg, shutdown, err := langfuse.Setup(&langfuse.Config{
//	    PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
//	    SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
//	    Host:      "https://cloud.langfuse.com",
//	})
//	if err != nil { log.Fatal(err) }
//	defer shutdown(context.Background())
//
//	runnr, _ := runner.New(runner.Config{
//	    Agent:        myAgent,
//	    PluginConfig: pluginCfg,
//	})
func Setup(cfg *Config) (runner.PluginConfig, func(context.Context) error, error) {
	auth := base64.StdEncoding.EncodeToString(
		[]byte(cfg.PublicKey + ":" + cfg.SecretKey),
	)

	host := cfg.Host
	if host == "" {
		host = defaultHost
	}

	exporterOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(fmt.Sprintf("%s/api/public/otel/v1/traces", host)),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization": "Basic " + auth,
		}),
	}
	if cfg.Insecure {
		exporterOpts = append(exporterOpts, otlptracehttp.WithInsecure())
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = defaultServiceName
	}

	return trace.Setup(trace.SetupConfig{
		PluginName:   "langfuse-enrichment",
		ExporterOpts: exporterOpts,
		ServiceName:  serviceName,
		Environment:  cfg.Environment,
		Mapper:       &langfuseMapper{},
		ErrorPrefix:  "langfuse",
	})
}

// TraceIDFromContext extracts the Langfuse trace ID from the context. Because
// the Langfuse plugin exports spans via OTLP, Langfuse uses the OpenTelemetry
// TraceID directly as its trace identifier. This is the same ID visible in the
// Langfuse UI trace detail page URL.
//
// Returns "" if no active span exists in the context or the span has no valid
// trace ID.
//func TraceIDFromContext(ctx context.Context) string {
//	return trace.TraceIDFromContext(ctx)
//}

// ---------------------------------------------------------------------------
// langfuseMapper implements trace.AttributeMapper for Langfuse.
// ---------------------------------------------------------------------------

type langfuseMapper struct{}

func (m *langfuseMapper) AgentInputKey() string { return "langfuse.trace.input" }
func (m *langfuseMapper) AgentOutputKeys() []string {
	return []string{"langfuse.trace.output", "langfuse.observation.output"}
}
func (m *langfuseMapper) LLMRequestKey() string   { return "gcp.vertex.agent.llm_request" }
func (m *langfuseMapper) LLMResponseKey() string  { return "gcp.vertex.agent.llm_response" }
func (m *langfuseMapper) ModelKey() string        { return "gen_ai.request.model" }
func (m *langfuseMapper) InputTokensKey() string  { return "gen_ai.usage.input_tokens" }
func (m *langfuseMapper) OutputTokensKey() string { return "gen_ai.usage.output_tokens" }
func (m *langfuseMapper) TotalTokensKey() string  { return "" } // Langfuse does not track total tokens separately

func (m *langfuseMapper) BeforeAgentAttrs(ctx agent.CallbackContext) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	// User ID: prefer context-injected value, fall back to ADK native
	if userID := trace.UserIDFromContext(ctx); userID != "" {
		attrs = append(attrs, attribute.String("langfuse.user.id", userID))
	} else if userID := ctx.UserID(); userID != "" {
		attrs = append(attrs, attribute.String("langfuse.user.id", userID))
	}

	if sessionID := ctx.SessionID(); sessionID != "" {
		attrs = append(attrs, attribute.String("langfuse.session.id", sessionID))
	}
	if tags := trace.TagsFromContext(ctx); len(tags) > 0 {
		attrs = append(attrs, attribute.StringSlice("langfuse.trace.tags", tags))
	}
	for k, v := range trace.TraceMetadataFromContext(ctx) {
		attrs = append(attrs, attribute.String("langfuse.trace.metadata."+k, v))
	}
	if env := trace.EnvironmentFromContext(ctx); env != "" {
		attrs = append(attrs, attribute.String("langfuse.environment", env))
	}
	if rel := trace.ReleaseFromContext(ctx); rel != "" {
		attrs = append(attrs, attribute.String("langfuse.release", rel))
	}
	if name := trace.TraceNameFromContext(ctx); name != "" {
		attrs = append(attrs, attribute.String("langfuse.trace.name", name))
	}

	// Also set observation.input alongside trace.input (handled by base enricher via AgentInputKey)
	if uc := ctx.UserContent(); uc != nil {
		text := trace.ContentToText(uc)
		if text != "" {
			attrs = append(attrs, attribute.String("langfuse.observation.input", `"`+text+`"`))
		}
	}

	return attrs
}
