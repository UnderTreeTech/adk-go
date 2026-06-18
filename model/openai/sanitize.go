package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	invalidToolCallTag   = "[invalid_tool_call]"
	invalidToolResultTag = "[invalid_tool_result]"
	orphanToolCallTag    = "[orphan_tool_call]"
	orphanToolResultTag  = "[orphan_tool_result]"
)

var errArgumentsNotValidJSON = errors.New("arguments are not valid JSON")

// sanitizeOpenAIMessages sanitizes the message history before sending it to the OpenAI API.
//
// It ensures tool calls and tool responses are properly paired, validates tool call
// arguments against their JSON schemas, and downgrades invalid or orphaned messages
// to user messages instead of silently dropping them.
//
// Message roles in the input can be "system", "user", "assistant", or "tool".
// Only "assistant" messages with tool_calls and "tool" messages are subject to
// sanitization; "system" and "user" messages pass through unchanged.
//
// The function processes messages in rounds: when an assistant message with tool_calls
// is followed by one or more tool messages, they form a round that is sanitized together.
// A standalone tool message (without a preceding assistant tool_call) is treated as
// an orphan and downgraded.
func sanitizeOpenAIMessages(messages []message, tools []tool) []message {
	if len(messages) == 0 {
		return messages
	}

	toolMap := make(map[string]tool, len(tools))
	for _, t := range tools {
		toolMap[t.Function.Name] = t
	}

	out := make([]message, 0, len(messages))
	for i := 0; i < len(messages); {
		msg := messages[i]
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			next := i + 1
			for next < len(messages) && messages[next].Role == "tool" {
				next++
			}
			out = append(out, sanitizeToolRound(msg, messages[i+1:next], toolMap)...)
			i = next
			continue
		}
		if msg.Role == "tool" {
			out = append(out, downgradeOrphanToolResult(msg))
			i++
			continue
		}
		out = append(out, msg)
		i++
	}
	return out
}

type toolCallValidation struct {
	validToolCalls   []toolCall
	invalidToolCalls []invalidToolCall
	validIDs         map[string]struct{}
	invalidIDs       map[string]struct{}
}

type invalidToolCall struct {
	call   toolCall
	reason string
}

type toolResultSplit struct {
	kept        []message
	invalidByID map[string][]message
	orphan      []message
}

type toolCallSplit struct {
	kept   []toolCall
	orphan []toolCall
}

// sanitizeToolRound sanitizes a single assistant tool-call round with its following tool results.
func sanitizeToolRound(assistant message, toolResults []message, tools map[string]tool) []message {
	validation := validateToolCalls(assistant.ToolCalls, tools)
	split := splitToolResults(toolResults, validation.validIDs, validation.invalidIDs)
	callSplit := splitToolCalls(validation.validToolCalls, split.kept)

	filteredAssistant := assistant
	filteredAssistant.ToolCalls = callSplit.kept
	if len(filteredAssistant.ToolCalls) == 0 {
		filteredAssistant.ToolCalls = nil
	}

	out := make([]message, 0,
		1+len(toolResults)+len(validation.invalidToolCalls)+len(callSplit.orphan)+len(split.orphan))

	if !isEmptyAssistantMessage(filteredAssistant) {
		out = append(out, filteredAssistant)
		out = append(out, split.kept...)
	}
	for _, orphanCall := range callSplit.orphan {
		out = append(out, downgradeOrphanToolCall(orphanCall))
	}
	for _, invalid := range validation.invalidToolCalls {
		out = append(out, downgradeInvalidToolCall(invalid.call, invalid.reason))
		for _, tr := range split.invalidByID[invalid.call.ID] {
			out = append(out, downgradeInvalidToolResult(tr))
		}
	}
	for _, orphan := range split.orphan {
		out = append(out, downgradeOrphanToolResult(orphan))
	}
	return out
}

// splitToolCalls splits tool calls into kept (with matching tool results) and orphan groups.
func splitToolCalls(toolCalls []toolCall, toolResults []message) toolCallSplit {
	out := toolCallSplit{
		kept: make([]toolCall, 0, len(toolCalls)),
	}
	respondedIDs := make(map[string]struct{}, len(toolResults))
	for _, tr := range toolResults {
		if tr.ToolCallID == "" {
			continue
		}
		respondedIDs[tr.ToolCallID] = struct{}{}
	}
	for _, tc := range toolCalls {
		if tc.ID != "" {
			if _, ok := respondedIDs[tc.ID]; ok {
				out.kept = append(out.kept, tc)
				continue
			}
		}
		out.orphan = append(out.orphan, tc)
	}
	return out
}

// validateToolCalls validates tool call arguments and groups them by validity.
func validateToolCalls(toolCalls []toolCall, tools map[string]tool) toolCallValidation {
	out := toolCallValidation{
		validToolCalls: make([]toolCall, 0, len(toolCalls)),
		validIDs:       make(map[string]struct{}),
		invalidIDs:     make(map[string]struct{}),
	}
	for _, tc := range toolCalls {
		validated, ok, reason := validateToolCall(tc, tools)
		if ok {
			out.validToolCalls = append(out.validToolCalls, validated)
			out.validIDs[validated.ID] = struct{}{}
			continue
		}
		out.invalidToolCalls = append(out.invalidToolCalls, invalidToolCall{call: tc, reason: reason})
		out.invalidIDs[tc.ID] = struct{}{}
	}
	return out
}

// validateToolCall validates and normalizes a single tool call's arguments.
func validateToolCall(tc toolCall, tools map[string]tool) (toolCall, bool, string) {
	normalizedArgs, decoded, err := normalizeAndDecodeArguments(tc.Function.Arguments)
	if err != nil {
		return tc, false, err.Error()
	}
	if ok, reason := validateToolCallArguments(tc.Function.Name, decoded, tools); !ok {
		return tc, false, reason
	}
	tc.Function.Arguments = normalizedArgs
	return tc, true, ""
}

// normalizeAndDecodeArguments trims whitespace, validates JSON, and decodes tool call arguments.
// Empty or whitespace-only arguments are normalized to "{}".
func normalizeAndDecodeArguments(args string) (string, any, error) {
	trimmed := strings.TrimSpace(args)
	if len(trimmed) == 0 {
		trimmed = "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return "", nil, errArgumentsNotValidJSON
	}
	var decoded any
	dec := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return "", nil, errArgumentsNotValidJSON
	}
	return trimmed, decoded, nil
}

// validateToolCallArguments validates decoded arguments against the tool's input schema.
func validateToolCallArguments(toolName string, args any, tools map[string]tool) (bool, string) {
	if tools == nil {
		return true, ""
	}
	tl, ok := tools[toolName]
	if !ok {
		return true, ""
	}
	if tl.Function.Parameters == nil {
		return true, ""
	}
	return validateArgumentsAgainstSchema(args, tl.Function.Parameters)
}

// validateArgumentsAgainstSchema validates decoded arguments against a JSON Schema.
// Returns (true, "") on success or (false, reason) on mismatch.
func validateArgumentsAgainstSchema(args any, schema map[string]any) (bool, string) {
	if schema == nil {
		return true, ""
	}
	if args == nil {
		defs := getDefs(schema)
		resolved := schema
		for resolved != nil {
			ref, _ := resolved["$ref"].(string)
			if ref == "" {
				break
			}
			next := resolveSchemaRef(ref, defs)
			if next == nil {
				resolved = nil
				break
			}
			resolved = next
		}
		if resolved == nil {
			return true, ""
		}
		schemaType := inferSchemaType(resolved)
		switch schemaType {
		case "object":
			return false, "expected object at $"
		case "array":
			return false, "expected array at $"
		case "string":
			return false, "expected string at $"
		case "boolean":
			return false, "expected boolean at $"
		case "integer":
			return false, "expected integer at $"
		case "number":
			return false, "expected number at $"
		default:
			return true, ""
		}
	}
	defs := getDefs(schema)
	ok, reason := validateValueAgainstSchema(args, schema, defs, "$")
	if ok {
		return true, ""
	}
	return false, reason
}

// getDefs extracts the "$defs" (or legacy "definitions") map from a JSON Schema.
func getDefs(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	raw, ok := schema["$defs"]
	if !ok {
		raw, ok = schema["definitions"]
	}
	if !ok {
		return nil
	}
	defs, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return defs
}

// inferSchemaType returns the type of a JSON Schema, inferring from "properties" or "items" if "type" is absent.
func inferSchemaType(schema map[string]any) string {
	if schema == nil {
		return ""
	}
	if t, ok := schema["type"].(string); ok && t != "" {
		return t
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	if _, ok := schema["items"]; ok {
		return "array"
	}
	return ""
}

// validateValueAgainstSchema validates a value against a subset of JSON Schema.
// Unknown schema types are skipped (treated as valid).
func validateValueAgainstSchema(value any, schema map[string]any, defs map[string]any, path string) (bool, string) {
	if schema == nil || value == nil {
		return true, ""
	}
	if ref, ok := schema["$ref"].(string); ok && ref != "" {
		resolved := resolveSchemaRef(ref, defs)
		if resolved == nil {
			return true, ""
		}
		return validateValueAgainstSchema(value, resolved, defs, path)
	}
	switch inferSchemaType(schema) {
	case "object":
		return validateObjectValueAgainstSchema(value, schema, defs, path)
	case "array":
		return validateArrayValueAgainstSchema(value, schema, defs, path)
	case "string":
		return validateStringValueAgainstSchema(value, schema, path)
	case "boolean":
		return validateBooleanValueAgainstSchema(value, path)
	case "integer":
		return validateIntegerValueAgainstSchema(value, path)
	case "number":
		return validateNumberValueAgainstSchema(value, path)
	default:
		return true, ""
	}
}

// validateObjectValueAgainstSchema validates that value is a map matching the schema's properties.
func validateObjectValueAgainstSchema(value any, schema map[string]any, defs map[string]any, path string) (bool, string) {
	asMap, ok := value.(map[string]any)
	if !ok {
		return false, fmt.Sprintf("expected object at %s", path)
	}
	props, _ := schema["properties"].(map[string]any)
	for key, propRaw := range props {
		propSchema, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}
		propValue, exists := asMap[key]
		if !exists {
			continue
		}
		if ok, reason := validateValueAgainstSchema(propValue, propSchema, defs, path+"."+key); !ok {
			return false, reason
		}
	}
	return true, ""
}

// validateArrayValueAgainstSchema validates that value is a slice matching the schema's items.
func validateArrayValueAgainstSchema(value any, schema map[string]any, defs map[string]any, path string) (bool, string) {
	asSlice, ok := value.([]any)
	if !ok {
		return false, fmt.Sprintf("expected array at %s", path)
	}
	itemsRaw, hasItems := schema["items"]
	if !hasItems {
		return true, ""
	}
	itemsSchema, ok := itemsRaw.(map[string]any)
	if !ok {
		return true, ""
	}
	for i, item := range asSlice {
		if ok, reason := validateValueAgainstSchema(item, itemsSchema, defs, fmt.Sprintf("%s[%d]", path, i)); !ok {
			return false, reason
		}
	}
	return true, ""
}

// validateStringValueAgainstSchema validates that value is a string and matches the schema's pattern if present.
func validateStringValueAgainstSchema(value any, schema map[string]any, path string) (bool, string) {
	str, ok := value.(string)
	if !ok {
		return false, fmt.Sprintf("expected string at %s", path)
	}
	pattern, _ := schema["pattern"].(string)
	if pattern == "" {
		return true, ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		// JSON Schema patterns use ECMA-262 syntax; Go regexp supports a subset.
		return true, ""
	}
	if re.MatchString(str) {
		return true, ""
	}
	return false, fmt.Sprintf("string at %s does not match pattern", path)
}

// validateBooleanValueAgainstSchema validates that value is a bool.
func validateBooleanValueAgainstSchema(value any, path string) (bool, string) {
	if _, ok := value.(bool); ok {
		return true, ""
	}
	return false, fmt.Sprintf("expected boolean at %s", path)
}

// validateIntegerValueAgainstSchema validates that value is a JSON integer (json.Number parseable as int64).
func validateIntegerValueAgainstSchema(value any, path string) (bool, string) {
	num, ok := value.(json.Number)
	if !ok {
		return false, fmt.Sprintf("expected integer at %s", path)
	}
	if _, err := num.Int64(); err != nil {
		return false, fmt.Sprintf("expected integer at %s", path)
	}
	return true, ""
}

// validateNumberValueAgainstSchema validates that value is a JSON number (json.Number parseable as float64).
func validateNumberValueAgainstSchema(value any, path string) (bool, string) {
	num, ok := value.(json.Number)
	if !ok {
		return false, fmt.Sprintf("expected number at %s", path)
	}
	if _, err := num.Float64(); err != nil {
		return false, fmt.Sprintf("expected number at %s", path)
	}
	return true, ""
}

// resolveSchemaRef resolves a local JSON Schema #/$defs/ or #/definitions/ reference.
func resolveSchemaRef(ref string, defs map[string]any) map[string]any {
	if defs == nil {
		return nil
	}
	const prefix = "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		name := strings.TrimPrefix(ref, prefix)
		if name == "" {
			return nil
		}
		raw, ok := defs[name]
		if !ok {
			return nil
		}
		s, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		return s
	}
	const oldPrefix = "#/definitions/"
	if strings.HasPrefix(ref, oldPrefix) {
		name := strings.TrimPrefix(ref, oldPrefix)
		if name == "" {
			return nil
		}
		raw, ok := defs[name]
		if !ok {
			return nil
		}
		s, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		return s
	}
	return nil
}

// splitToolResults groups tool result messages by validity: kept (matching a valid call),
// invalid (matching an invalid call), or orphan (no matching call or duplicate).
func splitToolResults(toolResults []message, validIDs, invalidIDs map[string]struct{}) toolResultSplit {
	out := toolResultSplit{
		kept:        make([]message, 0, len(toolResults)),
		invalidByID: make(map[string][]message),
	}
	respondedValidIDs := make(map[string]struct{}, len(validIDs))
	for _, tr := range toolResults {
		if tr.ToolCallID == "" {
			out.orphan = append(out.orphan, tr)
			continue
		}
		if _, ok := validIDs[tr.ToolCallID]; ok {
			if _, responded := respondedValidIDs[tr.ToolCallID]; responded {
				out.orphan = append(out.orphan, tr)
				continue
			}
			respondedValidIDs[tr.ToolCallID] = struct{}{}
			out.kept = append(out.kept, tr)
			continue
		}
		if _, ok := invalidIDs[tr.ToolCallID]; ok {
			out.invalidByID[tr.ToolCallID] = append(out.invalidByID[tr.ToolCallID], tr)
			continue
		}
		out.orphan = append(out.orphan, tr)
	}
	return out
}

// downgradeInvalidToolCall converts an invalid tool call into a user message preserving its payload.
func downgradeInvalidToolCall(call toolCall, reason string) message {
	content := fmt.Sprintf(
		"%s Tool call arguments were downgraded to a user message (%s).\nname: %s\nid: %s\narguments:\n```text\n%s\n```",
		invalidToolCallTag,
		reason,
		call.Function.Name,
		call.ID,
		call.Function.Arguments,
	)
	return message{
		Role:    "user",
		Content: content,
	}
}

// downgradeOrphanToolCall converts a tool call without a matching result into a user message.
func downgradeOrphanToolCall(call toolCall) message {
	content := fmt.Sprintf(
		"%s Tool call was downgraded to a user message because no matching tool result exists.\nname: %s\nid: %s\narguments:\n```text\n%s\n```",
		orphanToolCallTag,
		call.Function.Name,
		call.ID,
		call.Function.Arguments,
	)
	return message{
		Role:    "user",
		Content: content,
	}
}

// downgradeInvalidToolResult converts a tool result for an invalid tool call into a user message.
func downgradeInvalidToolResult(msg message) message {
	toolName := ""
	content := ""
	if msg.Content != nil {
		switch v := msg.Content.(type) {
		case string:
			content = v
		default:
			b, _ := json.Marshal(v)
			content = string(b)
		}
	}
	content = fmt.Sprintf(
		"%s Tool result was downgraded to a user message.\ntool_call_id: %s\ntool_name: %s\ncontent:\n```text\n%s\n```",
		invalidToolResultTag,
		msg.ToolCallID,
		toolName,
		content,
	)
	return message{
		Role:    "user",
		Content: content,
	}
}

// downgradeOrphanToolResult converts an orphaned tool result into a user message.
func downgradeOrphanToolResult(msg message) message {
	toolName := ""
	content := ""
	if msg.Content != nil {
		switch v := msg.Content.(type) {
		case string:
			content = v
		default:
			b, _ := json.Marshal(v)
			content = string(b)
		}
	}
	content = fmt.Sprintf(
		"%s Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: %s\ntool_name: %s\ncontent:\n```text\n%s\n```",
		orphanToolResultTag,
		msg.ToolCallID,
		toolName,
		content,
	)
	return message{
		Role:    "user",
		Content: content,
	}
}

// isEmptyAssistantMessage reports whether an assistant message has no text, reasoning, or tool calls.
func isEmptyAssistantMessage(msg message) bool {
	if msg.Role != "assistant" {
		return false
	}
	if len(msg.ToolCalls) > 0 {
		return false
	}
	if msg.Content != nil {
		switch v := msg.Content.(type) {
		case string:
			if v != "" {
				return false
			}
		default:
			return false
		}
	}
	if msg.ReasoningContent != nil {
		return false
	}
	return true
}
