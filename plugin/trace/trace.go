package trace

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// ---------------------------------------------------------------------------
// AttributeMapper
// ---------------------------------------------------------------------------

// AttributeMapper defines how a concrete backend (Jaeger, Langfuse, …)
// maps logical trace concepts to span attribute keys. Each backend provides
// its own implementation so that the shared SpanEnricher and
// EnrichingExporter can remain backend-agnostic.
type AttributeMapper interface {
	// AgentInputKey returns the attribute key for the agent's input content.
	AgentInputKey() string

	// AgentOutputKeys returns the attribute key(s) for the agent's output.
	// Multiple keys are supported (e.g. Langfuse sets both trace.output and
	// observation.output).
	AgentOutputKeys() []string

	// LLMRequestKey returns the attribute key for the serialised LLM request.
	LLMRequestKey() string

	// LLMResponseKey returns the attribute key for the LLM response text.
	LLMResponseKey() string

	// ModelKey returns the attribute key for the model identifier.
	ModelKey() string

	// InputTokensKey returns the attribute key for input token count.
	InputTokensKey() string

	// OutputTokensKey returns the attribute key for output token count.
	OutputTokensKey() string

	// TotalTokensKey returns the attribute key for total token count.
	// Return "" if the backend does not track total tokens separately.
	TotalTokensKey() string

	// BeforeAgentAttrs is called during beforeAgent to let the backend inject
	// additional backend-specific attributes (e.g. Langfuse user ID, tags,
	// metadata). The base enricher already handles the common attributes
	// (user ID, session ID, input content) via the standard keys above.
	//
	// ctx is the ADK callback context which also carries the Go context.
	// Return nil to add no extra attributes.
	BeforeAgentAttrs(ctx agent.CallbackContext) []attribute.KeyValue
}

// ---------------------------------------------------------------------------
// LLMCall
// ---------------------------------------------------------------------------

// LLMCall captures the serialised request and response text for a single
// generate_content LLM invocation.
type LLMCall struct {
	Request      string
	Response     string
	Model        string
	InputTokens  int32
	OutputTokens int32
	TotalTokens  int32
}

// ---------------------------------------------------------------------------
// SpanEnricher
// ---------------------------------------------------------------------------

// SpanEnricher is the core state holder for trace-enrichment ADK plugins. It
// tracks in-flight agent spans and pending LLM calls so that the
// EnrichingExporter can attach request/response payloads to the correct
// generate_content spans at export time.
//
// Keys are built from invocationID + branch (via BranchKey) so that
// ParallelAgent sub-agents running concurrently under the same invocation
// never collide.
type SpanEnricher struct {
	mu         sync.Mutex
	agentSpans map[string][]oteltrace.Span // branchKey → stack of invoke_agent spans
	pending    map[string][]LLMCall        // invoke_agent spanID → FIFO queue of LLM calls
	mapper     AttributeMapper
}

// BranchKey builds the map key that isolates parallel branches. In
// sequential flows Branch() returns "" and the key equals the invocationID.
func BranchKey(ctx agent.CallbackContext) string {
	if b := ctx.Branch(); b != "" {
		return ctx.InvocationID() + ":" + b
	}
	return ctx.InvocationID()
}

// NewSpanEnricher creates a fresh SpanEnricher with the given mapper.
func NewSpanEnricher(mapper AttributeMapper) *SpanEnricher {
	return &SpanEnricher{
		agentSpans: make(map[string][]oteltrace.Span),
		pending:    make(map[string][]LLMCall),
		mapper:     mapper,
	}
}

// BeforeAgent is the BeforeAgentCallback. It pushes the current
// invoke_agent span onto a per-branch stack and decorates it with common
// and backend-specific attributes.
func (e *SpanEnricher) BeforeAgent(ctx agent.CallbackContext) (*genai.Content, error) {
	span := oteltrace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return nil, nil
	}
	key := BranchKey(ctx)
	e.mu.Lock()
	e.agentSpans[key] = append(e.agentSpans[key], span)
	e.mu.Unlock()

	// Let the backend inject its own attributes (user ID, tags, metadata, etc.)
	if extra := e.mapper.BeforeAgentAttrs(ctx); len(extra) > 0 {
		span.SetAttributes(extra...)
	}

	// Common: agent input
	if uc := ctx.UserContent(); uc != nil {
		if s, err := json.Marshal(ContentToText(uc)); err == nil {
			span.SetAttributes(
				attribute.String(e.mapper.AgentInputKey(), string(s)),
			)
		}
	}
	return nil, nil
}

// AfterAgent is the AfterAgentCallback. It pops the invoke_agent span from
// the per-branch stack, cleaning up when the last span is removed.
func (e *SpanEnricher) AfterAgent(ctx agent.CallbackContext) (*genai.Content, error) {
	key := BranchKey(ctx)
	e.mu.Lock()
	stack := e.agentSpans[key]
	if len(stack) > 1 {
		e.agentSpans[key] = stack[:len(stack)-1]
	} else {
		delete(e.agentSpans, key)
	}
	e.mu.Unlock()
	return nil, nil
}

// BeforeModel is the BeforeModelCallback. It serialises the full LLM prompt
// and enqueues it as a pending LLMCall keyed by the invoke_agent span ID.
func (e *SpanEnricher) BeforeModel(ctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	span := oteltrace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return nil, nil
	}
	spanID := span.SpanContext().SpanID().String()
	reqJSON, _ := json.Marshal(MarshalLLMRequest(req))

	e.mu.Lock()
	e.pending[spanID] = append(e.pending[spanID], LLMCall{
		Request: string(reqJSON),
		Model:   req.Model,
	})
	e.mu.Unlock()
	return nil, nil
}

// AfterModel is the AfterModelCallback. It captures the model's response
// text (or the error message on failure), attaches it to the pending
// LLMCall, and — when the response is a non-partial final text answer (no
// function calls) — propagates the output to the ancestor invoke_agent
// spans in the same branch.
func (e *SpanEnricher) AfterModel(ctx agent.CallbackContext, resp *model.LLMResponse, llmErr error) (*model.LLMResponse, error) {
	span := oteltrace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return nil, nil
	}
	spanID := span.SpanContext().SpanID().String()

	var text string
	if llmErr != nil {
		text = llmErr.Error()
	} else if resp != nil && resp.Content != nil {
		text = ContentToText(resp.Content)
	}

	e.mu.Lock()
	queue := e.pending[spanID]
	if len(queue) > 0 {
		queue[len(queue)-1].Response = text
		if resp != nil && resp.UsageMetadata != nil {
			queue[len(queue)-1].InputTokens = resp.UsageMetadata.PromptTokenCount
			queue[len(queue)-1].OutputTokens = resp.UsageMetadata.CandidatesTokenCount
			queue[len(queue)-1].TotalTokens = resp.UsageMetadata.TotalTokenCount
		}
	}
	e.mu.Unlock()

	// Propagate final output to ancestor agent spans
	if resp != nil && !resp.Partial && text != "" && !HasFunctionCalls(resp.Content) {
		key := BranchKey(ctx)
		e.mu.Lock()
		stack := e.agentSpans[key]
		e.mu.Unlock()
		outputJSON, _ := json.Marshal(text)
		outputKeys := e.mapper.AgentOutputKeys()
		for _, s := range stack {
			if s != nil && s.IsRecording() {
				for _, k := range outputKeys {
					s.SetAttributes(attribute.String(k, string(outputJSON)))
				}
			}
		}
	}
	return nil, nil
}

// PopCall dequeues the oldest pending LLMCall for the given span ID. It
// returns false when no calls are pending.
func (e *SpanEnricher) PopCall(spanID string) (LLMCall, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	queue := e.pending[spanID]
	if len(queue) == 0 {
		return LLMCall{}, false
	}
	call := queue[0]
	if len(queue) == 1 {
		delete(e.pending, spanID)
	} else {
		e.pending[spanID] = queue[1:]
	}
	return call, true
}

// ---------------------------------------------------------------------------
// EnrichingExporter
// ---------------------------------------------------------------------------

// EnrichingExporter wraps a real OTLP SpanExporter and injects LLM
// request/response attributes into generate_content spans just before they
// are exported.
type EnrichingExporter struct {
	Inner    sdktrace.SpanExporter
	Enricher *SpanEnricher
}

// ExportSpans enriches generate_content spans with the pending LLM
// request/response payloads and delegates to the inner exporter.
func (ex *EnrichingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	enriched := make([]sdktrace.ReadOnlySpan, len(spans))
	mapper := ex.Enricher.mapper
	for i, s := range spans {
		var extra []attribute.KeyValue

		if strings.HasPrefix(s.Name(), "generate_content") {
			parentID := s.Parent().SpanID().String()
			if call, ok := ex.Enricher.PopCall(parentID); ok {
				if call.Request != "" {
					extra = append(extra, attribute.String(mapper.LLMRequestKey(), call.Request))
				}
				if call.Response != "" {
					extra = append(extra, attribute.String(mapper.LLMResponseKey(), call.Response))
				}
				if call.Model != "" {
					extra = append(extra, attribute.String(mapper.ModelKey(), call.Model))
				}
				if call.InputTokens > 0 {
					extra = append(extra, attribute.Int64(mapper.InputTokensKey(), int64(call.InputTokens)))
				}
				if call.OutputTokens > 0 {
					extra = append(extra, attribute.Int64(mapper.OutputTokensKey(), int64(call.OutputTokens)))
				}
				if totalKey := mapper.TotalTokensKey(); totalKey != "" && call.TotalTokens > 0 {
					extra = append(extra, attribute.Int64(totalKey, int64(call.TotalTokens)))
				}
			}
		}

		if len(extra) > 0 {
			enriched[i] = &EnrichedSpan{ReadOnlySpan: s, Extra: extra}
		} else {
			enriched[i] = s
		}
	}
	return ex.Inner.ExportSpans(ctx, enriched)
}

// Shutdown delegates to the inner exporter, flushing any buffered spans.
func (ex *EnrichingExporter) Shutdown(ctx context.Context) error {
	return ex.Inner.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// EnrichedSpan
// ---------------------------------------------------------------------------

// EnrichedSpan wraps an sdktrace.ReadOnlySpan and appends extra attributes
// without modifying the original. Every method of the ReadOnlySpan interface
// is explicitly forwarded so that the wrapper satisfies the full contract.
type EnrichedSpan struct {
	sdktrace.ReadOnlySpan
	Extra []attribute.KeyValue
}

// Attributes returns the original span attributes plus the extra ones
// injected by the EnrichingExporter.
func (s *EnrichedSpan) Attributes() []attribute.KeyValue {
	return append(s.ReadOnlySpan.Attributes(), s.Extra...)
}

func (s *EnrichedSpan) Name() string                       { return s.ReadOnlySpan.Name() }
func (s *EnrichedSpan) SpanContext() oteltrace.SpanContext { return s.ReadOnlySpan.SpanContext() }
func (s *EnrichedSpan) Parent() oteltrace.SpanContext      { return s.ReadOnlySpan.Parent() }
func (s *EnrichedSpan) SpanKind() oteltrace.SpanKind       { return s.ReadOnlySpan.SpanKind() }
func (s *EnrichedSpan) StartTime() time.Time               { return s.ReadOnlySpan.StartTime() }
func (s *EnrichedSpan) EndTime() time.Time                 { return s.ReadOnlySpan.EndTime() }
func (s *EnrichedSpan) Events() []sdktrace.Event           { return s.ReadOnlySpan.Events() }
func (s *EnrichedSpan) Links() []sdktrace.Link             { return s.ReadOnlySpan.Links() }
func (s *EnrichedSpan) Status() sdktrace.Status            { return s.ReadOnlySpan.Status() }
func (s *EnrichedSpan) Resource() *resource.Resource       { return s.ReadOnlySpan.Resource() }
func (s *EnrichedSpan) DroppedAttributes() int             { return s.ReadOnlySpan.DroppedAttributes() }
func (s *EnrichedSpan) DroppedEvents() int                 { return s.ReadOnlySpan.DroppedEvents() }
func (s *EnrichedSpan) DroppedLinks() int                  { return s.ReadOnlySpan.DroppedLinks() }
func (s *EnrichedSpan) ChildSpanCount() int                { return s.ReadOnlySpan.ChildSpanCount() }
func (s *EnrichedSpan) InstrumentationScope() instrumentation.Scope {
	return s.ReadOnlySpan.InstrumentationScope()
}
func (s *EnrichedSpan) InstrumentationLibrary() instrumentation.Scope {
	return s.ReadOnlySpan.InstrumentationLibrary()
}
