// Package trace provides shared infrastructure for ADK Go trace-enrichment
// plugins. Concrete backends (Jaeger, Langfuse, etc.) build on top of this
// package by supplying an AttributeMapper that controls which span attributes
// are emitted.
package trace

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// MarshalLLMRequest converts an ADK LLMRequest into a JSON-friendly map
// containing the system instruction and the full message history (text,
// tool calls, and tool responses).
func MarshalLLMRequest(req *model.LLMRequest) map[string]any {
	msgs := make([]map[string]any, 0, len(req.Contents))
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			switch {
			case p.Text != "":
				msgs = append(msgs, map[string]any{"role": c.Role, "content": p.Text})
			case p.FunctionCall != nil:
				args, _ := json.Marshal(p.FunctionCall.Args)
				msgs = append(msgs, map[string]any{
					"role":      c.Role,
					"tool_call": map[string]any{"name": p.FunctionCall.Name, "args": string(args)},
				})
			case p.FunctionResponse != nil:
				r, _ := json.Marshal(p.FunctionResponse.Response)
				msgs = append(msgs, map[string]any{
					"role":          "tool",
					"tool_response": map[string]any{"name": p.FunctionResponse.Name, "result": string(r)},
				})
			}
		}
	}
	result := map[string]any{"messages": msgs}
	if req.Config != nil && req.Config.SystemInstruction != nil {
		result["system"] = ContentToText(req.Config.SystemInstruction)
	}
	return result
}

// ContentToText flattens a genai.Content into a single human-readable string.
// Text parts are joined with newlines; function calls and responses are
// rendered as bracketed annotations (e.g. "[tool_call: name(args)]").
func ContentToText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var parts []string
	for _, p := range c.Parts {
		switch {
		case p.Text != "":
			parts = append(parts, p.Text)
		case p.FunctionCall != nil:
			args, _ := json.Marshal(p.FunctionCall.Args)
			parts = append(parts, fmt.Sprintf("[tool_call: %s(%s)]", p.FunctionCall.Name, string(args)))
		case p.FunctionResponse != nil:
			resp, _ := json.Marshal(p.FunctionResponse.Response)
			parts = append(parts, fmt.Sprintf("[tool_response: %s → %s]", p.FunctionResponse.Name, string(resp)))
		}
	}
	return strings.Join(parts, "\n")
}

// HasFunctionCalls reports whether c contains at least one FunctionCall part.
// It is used to distinguish intermediate tool-call responses from final text
// answers so that only the latter are propagated as trace output.
func HasFunctionCalls(c *genai.Content) bool {
	if c == nil {
		return false
	}
	for _, p := range c.Parts {
		if p.FunctionCall != nil {
			return true
		}
	}
	return false
}
