package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// tc is a helper to create a toolCall for tests.
func tc(id, name, args string) toolCall {
	return toolCall{ID: id, Type: "function", Function: functionCall{Name: name, Arguments: args}}
}

// tl is a helper to create a tool definition for tests.
func tl(name string, parameters map[string]any) tool {
	return tool{Type: "function", Function: function{Name: name, Parameters: parameters}}
}

func TestSanitizeOpenAIMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []message
		tools    []tool
		want     []message
	}{
		// --- 基础边界 ---
		{
			name:     "empty_messages",
			messages: []message{},
			tools:    nil,
			want:     []message{},
		},
		{
			name:     "nil_messages",
			messages: nil,
			tools:    nil,
			want:     nil,
		},
		// --- 完整配对保留 ---
		{
			name: "no_orphans_all_matched",
			messages: []message{
				{Role: "system", Content: "You are helpful"},
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "system", Content: "You are helpful"},
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 孤立的工具调用被降级为 user 消息 ---
		{
			name: "orphaned_tool_call_downgraded_to_user",
			messages: []message{
				{Role: "system", Content: "You are helpful"},
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{"key": "val"}`)}},
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "system", Content: "You are helpful"},
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: tc_1\narguments:\n```text\n{\"key\": \"val\"}\n```"},
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 孤立工具调用有文本内容时保留 assistant 消息 ---
		{
			name: "orphaned_tool_call_with_text_preserves_assistant",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Let me check that", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "user", Content: "Continue"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Let me check that"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: tc_1\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
		// --- 孤立工具调用有推理内容时保留 assistant 消息 ---
		{
			name: "all_tool_calls_orphaned_with_reasoning_content_preserves_reasoning",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ReasoningContent: "Let me think", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "user", Content: "Continue"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ReasoningContent: "Let me think"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: tc_1\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
		// --- 所有工具调用孤立且无内容时被降级（不保留空 assistant） ---
		{
			name: "all_tool_calls_orphaned_no_content_downgraded",
			messages: []message{
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: tc_1\narguments:\n```text\n{}\n```"},
			},
		},
		// --- 多个孤立工具调用全被降级 ---
		{
			name: "multiple_orphaned_tool_calls_all_downgraded",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "func2", `{}`)}},
				{Role: "user", Content: "Continue"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: tc_1\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func2\nid: tc_2\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
		// --- 部分工具有响应，部分孤立 ---
		{
			name: "partial_tool_call_match_keeps_matched_downgrades_orphan",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{
					tc("tc_1", "func1", `{}`),
					tc("tc_2", "func2", `{}`),
				}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "user", Content: "Continue"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func2\nid: tc_2\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
		// --- 三个工具调用中部分孤立 ---
		{
			name: "multiple_tool_calls_partial_orphan_keeps_matched",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{
					tc("tc_1", "func1", `{}`),
					tc("tc_2", "func2", `{}`),
					tc("tc_3", "func3", `{}`),
				}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "1"}`},
				{Role: "tool", ToolCallID: "tc_3", Content: `{"result": "3"}`},
				{Role: "user", Content: "Continue"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{
					tc("tc_1", "func1", `{}`),
					tc("tc_3", "func3", `{}`),
				}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "1"}`},
				{Role: "tool", ToolCallID: "tc_3", Content: `{"result": "3"}`},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func2\nid: tc_2\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
		// --- 孤立的工具响应被降级 ---
		{
			name: "orphaned_tool_response_downgraded",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "tool", ToolCallID: "tc_orphan", Content: `{"result": "orphan"}`},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_orphan\ntool_name: \ncontent:\n```text\n{\"result\": \"orphan\"}\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 独立的 tool 消息（无前驱 assistant）被降级 ---
		{
			name: "standalone_orphan_tool_result_downgraded",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "tool", ToolCallID: "tc_orphan1", Content: `{"result": "orphan1"}`},
				{Role: "tool", ToolCallID: "tc_orphan2", Content: `{"result": "orphan2"}`},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_orphan1\ntool_name: \ncontent:\n```text\n{\"result\": \"orphan1\"}\n```"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_orphan2\ntool_name: \ncontent:\n```text\n{\"result\": \"orphan2\"}\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 有效的工具调用保留，孤立响应降级 ---
		{
			name: "orphaned_tool_response_preserves_valid_tool_calls",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "tool", ToolCallID: "tc_orphan", Content: `{"result": "orphan"}`},
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_orphan\ntool_name: \ncontent:\n```text\n{\"result\": \"orphan\"}\n```"},
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 同时存在孤立调用和孤立响应 ---
		{
			name: "both_orphaned_tool_call_and_orphaned_tool_response",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "func2", `{}`)}},
				{Role: "tool", ToolCallID: "tc_orphan", Content: `{"result": "orphan"}`},
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func2\nid: tc_2\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_orphan\ntool_name: \ncontent:\n```text\n{\"result\": \"orphan\"}\n```"},
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 空 tool_call_id 的 tool 响应被降级（无前驱 assistant 无法配对） ---
		{
			name: "tool_response_with_empty_tool_call_id_standalone_orphan",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "tool", Content: `{"result": "no_id"}`},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: \ntool_name: \ncontent:\n```text\n{\"result\": \"no_id\"}\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 级联孤立：移除孤立响应后暴露孤立调用 ---
		{
			name: "orphaned_response_removal_exposes_orphaned_call",
			messages: []message{
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_orphan", Content: `{"result": "orphan"}`},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: tc_1\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_orphan\ntool_name: \ncontent:\n```text\n{\"result\": \"orphan\"}\n```"},
			},
		},
		// --- 完整的调用-响应对保留 ---
		{
			name: "complete_tool_call_response_pair_preserved",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "assistant", Content: "Result is ok"},
				{Role: "user", Content: "Thanks"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "ok"}`},
				{Role: "assistant", Content: "Result is ok"},
				{Role: "user", Content: "Thanks"},
			},
		},
		// --- 孤立响应在有效对之间 ---
		{
			name: "orphaned_tool_response_between_valid_pairs",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "1"}`},
				{Role: "tool", ToolCallID: "tc_orphan", Content: `{"result": "orphan"}`},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "func2", `{}`)}},
				{Role: "tool", ToolCallID: "tc_2", Content: `{"result": "2"}`},
				{Role: "assistant", Content: "All done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: `{"result": "1"}`},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_orphan\ntool_name: \ncontent:\n```text\n{\"result\": \"orphan\"}\n```"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "func2", `{}`)}},
				{Role: "tool", ToolCallID: "tc_2", Content: `{"result": "2"}`},
				{Role: "assistant", Content: "All done"},
			},
		},
		// --- 空 tool_call_id 的调用被视为孤立（无响应）并降级 ---
		{
			name: "empty_tool_call_id_orphan_downgraded",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("", "func1", `{}`)}},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: \narguments:\n```text\n{}\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeOpenAIMessages(tt.messages, tt.tools)
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("sanitizeOpenAIMessages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSanitizeOpenAIMessages_ArgumentValidation(t *testing.T) {
	tests := []struct {
		name     string
		messages []message
		tools    []tool
		want     []message
	}{
		// --- 无效 JSON 参数被降级 ---
		{
			name: "invalid_json_arguments_downgraded",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{broken json}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
				{Role: "assistant", Content: "Done"},
			},
			tools: []tool{tl("my_func", nil)},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (arguments are not valid JSON).\nname: my_func\nid: tc_1\narguments:\n```text\n{broken json}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 参数类型不匹配 schema 被降级 ---
		{
			name: "arguments_type_mismatch_schema_downgraded",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `"not_an_object"`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
				{Role: "assistant", Content: "Done"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected object at $).\nname: my_func\nid: tc_1\narguments:\n```text\n\"not_an_object\"\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 有效参数通过校验 ---
		{
			name: "valid_arguments_pass_validation",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": "test"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
				{Role: "assistant", Content: "Done"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": "test"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- 空参数自动规范化为 {} ---
		{
			name: "empty_arguments_normalized_to_empty_object",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", "")}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", "{}")}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- 无工具定义时跳过 schema 校验 ---
		{
			name: "no_tools_skips_schema_validation",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"key": 123}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"key": 123}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- 未知工具名跳过 schema 校验 ---
		{
			name: "unknown_tool_name_skips_schema_validation",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "unknown_func", `{"key": 123}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("other_func", map[string]any{"type": "object"})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "unknown_func", `{"key": 123}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- 混合场景：部分调用无效、部分孤立、部分有效 ---
		{
			name: "mixed_invalid_orphan_and_valid",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{
					tc("tc_1", "my_func", `{"name": "valid"}`),
					tc("tc_2", "my_func", `{broken}`),
					tc("tc_3", "my_func", `{"name": "orphan"}`),
				}},
				{Role: "tool", ToolCallID: "tc_1", Content: "valid result"},
				{Role: "tool", ToolCallID: "tc_2", Content: "invalid result"},
				{Role: "user", Content: "Continue"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": "valid"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "valid result"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: my_func\nid: tc_3\narguments:\n```text\n{\"name\": \"orphan\"}\n```"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (arguments are not valid JSON).\nname: my_func\nid: tc_2\narguments:\n```text\n{broken}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_2\ntool_name: \ncontent:\n```text\ninvalid result\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeOpenAIMessages(tt.messages, tt.tools)
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("sanitizeOpenAIMessages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSanitizeOpenAIMessages_SchemaValidation(t *testing.T) {
	tests := []struct {
		name     string
		messages []message
		tools    []tool
		want     []message
	}{
		// --- 对象类型校验：属性值类型不匹配 ---
		{
			name: "object_property_type_mismatch",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"count": "not_a_number"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected integer at $.count).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"count\": \"not_a_number\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- 数组类型校验：元素类型不匹配 ---
		{
			name: "array_item_type_mismatch",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"items": [1, "two", 3]}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "integer"},
					},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected integer at $.items[1]).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"items\": [1, \"two\", 3]}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- 布尔类型校验 ---
		{
			name: "boolean_type_mismatch",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"flag": "yes"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flag": map[string]any{"type": "boolean"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected boolean at $.flag).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"flag\": \"yes\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- 数值类型校验 ---
		{
			name: "number_type_mismatch",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"value": "not_a_number"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "number"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected number at $.value).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"value\": \"not_a_number\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- 字符串 pattern 校验 ---
		{
			name: "string_pattern_mismatch",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"email": "not-an-email"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"email": map[string]any{"type": "string", "pattern": "^[^@]+@[^@]+\\.[^@]+$"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (string at $.email does not match pattern).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"email\": \"not-an-email\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- $defs 引用解析 ---
		{
			name: "schema_with_defs_ref",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"item": "not_an_object"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"item": map[string]any{"$ref": "#/$defs/ItemDef"},
				},
				"$defs": map[string]any{
					"ItemDef": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
						},
					},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected object at $.item).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"item\": \"not_an_object\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- 有效的嵌套对象通过校验 ---
		{
			name: "valid_nested_object_passes",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"outer": {"inner": 42}}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"outer": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"inner": map[string]any{"type": "integer"},
						},
					},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"outer": {"inner": 42}}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeOpenAIMessages(tt.messages, tt.tools)
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("sanitizeOpenAIMessages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDowngradeFunctions(t *testing.T) {
	t.Run("downgrade_invalid_tool_call", func(t *testing.T) {
		call := toolCall{ID: "tc_1", Type: "function", Function: functionCall{Name: "my_func", Arguments: `{"bad": true}`}}
		msg := downgradeInvalidToolCall(call, "some reason")
		if msg.Role != "user" {
			t.Errorf("expected role user, got %q", msg.Role)
		}
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, invalidToolCallTag) {
			t.Errorf("expected content to contain %q, got %q", invalidToolCallTag, content)
		}
		if !strings.Contains(content, "some reason") {
			t.Errorf("expected content to contain reason, got %q", content)
		}
		if !strings.Contains(content, "my_func") {
			t.Errorf("expected content to contain tool name, got %q", content)
		}
		if !strings.Contains(content, "tc_1") {
			t.Errorf("expected content to contain tool call ID, got %q", content)
		}
	})

	t.Run("downgrade_orphan_tool_call", func(t *testing.T) {
		call := toolCall{ID: "tc_2", Type: "function", Function: functionCall{Name: "orphan_func", Arguments: `{}`}}
		msg := downgradeOrphanToolCall(call)
		if msg.Role != "user" {
			t.Errorf("expected role user, got %q", msg.Role)
		}
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, orphanToolCallTag) {
			t.Errorf("expected content to contain %q, got %q", orphanToolCallTag, content)
		}
		if !strings.Contains(content, "orphan_func") {
			t.Errorf("expected content to contain tool name, got %q", content)
		}
	})

	t.Run("downgrade_invalid_tool_result", func(t *testing.T) {
		orig := message{Role: "tool", ToolCallID: "tc_3", Content: "some result"}
		msg := downgradeInvalidToolResult(orig)
		if msg.Role != "user" {
			t.Errorf("expected role user, got %q", msg.Role)
		}
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, invalidToolResultTag) {
			t.Errorf("expected content to contain %q, got %q", invalidToolResultTag, content)
		}
		if !strings.Contains(content, "tc_3") {
			t.Errorf("expected content to contain tool call ID, got %q", content)
		}
	})

	t.Run("downgrade_orphan_tool_result", func(t *testing.T) {
		orig := message{Role: "tool", ToolCallID: "tc_4", Content: "orphan result"}
		msg := downgradeOrphanToolResult(orig)
		if msg.Role != "user" {
			t.Errorf("expected role user, got %q", msg.Role)
		}
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, orphanToolResultTag) {
			t.Errorf("expected content to contain %q, got %q", orphanToolResultTag, content)
		}
		if !strings.Contains(content, "tc_4") {
			t.Errorf("expected content to contain tool call ID, got %q", content)
		}
	})
}

func TestNormalizeAndDecodeArguments(t *testing.T) {
	tests := []struct {
		name      string
		args      string
		wantArgs  string
		wantValid bool
	}{
		{"empty_string", "", "{}", true},
		{"whitespace_only", "  ", "{}", true},
		{"valid_object", `{"key": "val"}`, `{"key": "val"}`, true},
		{"valid_array", `[1, 2, 3]`, `[1, 2, 3]`, true},
		{"invalid_json", `{broken}`, "", false},
		{"trimmed_whitespace", `  {"a": 1}  `, `{"a": 1}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, _, err := normalizeAndDecodeArguments(tt.args)
			if tt.wantValid {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
				if normalized != tt.wantArgs {
					t.Errorf("expected normalized %q, got %q", tt.wantArgs, normalized)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			}
		})
	}
}

func TestIsEmptyAssistantMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  message
		want bool
	}{
		{"empty_assistant", message{Role: "assistant"}, true},
		{"with_text", message{Role: "assistant", Content: "hello"}, false},
		{"with_empty_string_content", message{Role: "assistant", Content: ""}, true},
		{"with_tool_calls", message{Role: "assistant", ToolCalls: []toolCall{{ID: "1"}}}, false},
		{"with_reasoning", message{Role: "assistant", ReasoningContent: "thinking"}, false},
		{"non_assistant_role", message{Role: "user", Content: "hello"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyAssistantMessage(tt.msg)
			if got != tt.want {
				t.Errorf("isEmptyAssistantMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInferSchemaType(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{"nil_schema", nil, ""},
		{"explicit_type", map[string]any{"type": "string"}, "string"},
		{"inferred_object", map[string]any{"properties": map[string]any{}}, "object"},
		{"inferred_array", map[string]any{"items": map[string]any{}}, "array"},
		{"no_type_info", map[string]any{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSchemaType(tt.schema)
			if got != tt.want {
				t.Errorf("inferSchemaType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Additional coverage tests ---

func TestSanitizeOpenAIMessages_AdditionalCoverage(t *testing.T) {
	tests := []struct {
		name     string
		messages []message
		tools    []tool
		want     []message
	}{
		// --- assistant with empty ToolCalls slice passes through ---
		{
			name: "assistant_with_empty_toolcalls_slice_passes_through",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{}},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{}},
			},
		},
		// --- only system messages ---
		{
			name: "only_system_messages",
			messages: []message{
				{Role: "system", Content: "You are helpful"},
			},
			tools: nil,
			want: []message{
				{Role: "system", Content: "You are helpful"},
			},
		},
		// --- multiple consecutive tool rounds ---
		{
			name: "multiple_consecutive_tool_rounds",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "f1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "r1"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "f2", `{}`)}},
				{Role: "tool", ToolCallID: "tc_2", Content: "r2"},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "f1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "r1"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "f2", `{}`)}},
				{Role: "tool", ToolCallID: "tc_2", Content: "r2"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- duplicate tool result for same valid tool_call_id treated as orphan ---
		{
			name: "duplicate_tool_result_same_valid_id_treated_as_orphan",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "first"},
				{Role: "tool", ToolCallID: "tc_1", Content: "duplicate"},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "first"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nduplicate\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- assistant with both text and invalid tool call ---
		{
			name: "assistant_with_text_and_invalid_tool_call",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "I will call a tool", ToolCalls: []toolCall{tc("tc_1", "my_func", `{bad}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", nil)},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "I will call a tool"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (arguments are not valid JSON).\nname: my_func\nid: tc_1\narguments:\n```text\n{bad}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- arguments whitespace trimmed and normalized ---
		{
			name: "arguments_whitespace_trimmed_and_normalized",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `  {"key": "val"}  `)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{"key": "val"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- tool with nil Parameters skips schema validation ---
		{
			name: "tool_with_nil_parameters_skips_schema_validation",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `"not_an_object"`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", nil)},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `"not_an_object"`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- tool result with empty ToolCallID within a round is orphan ---
		{
			name: "tool_result_with_empty_tool_call_id_within_round_is_orphan",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", Content: "no_id_result"},
				{Role: "tool", ToolCallID: "tc_1", Content: "has_id_result"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "has_id_result"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: \ntool_name: \ncontent:\n```text\nno_id_result\n```"},
			},
		},
		// --- single message: only user ---
		{
			name: "single_user_message",
			messages: []message{
				{Role: "user", Content: "Hello"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
			},
		},
		// --- valid array arguments pass ---
		{
			name: "valid_array_arguments_pass",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `[1, 2, 3]`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `[1, 2, 3]`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- valid boolean and integer arguments pass ---
		{
			name: "valid_mixed_type_arguments_pass",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"flag": true, "count": 5}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flag":  map[string]any{"type": "boolean"},
					"count": map[string]any{"type": "integer"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"flag": true, "count": 5}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- integer value where number expected passes ---
		{
			name: "integer_where_number_expected_passes",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"value": 42}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "number"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"value": 42}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- float value where integer expected fails ---
		{
			name: "float_where_integer_expected_fails",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"count": 3.14}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected integer at $.count).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"count\": 3.14}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- schema with no type and no properties/items passes ---
		{
			name: "schema_with_no_type_info_passes",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"anything": "goes"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"description": "a tool with no type or properties",
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"anything": "goes"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- string pattern that matches passes ---
		{
			name: "string_pattern_match_passes",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"email": "user@example.com"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"email": map[string]any{"type": "string", "pattern": "^[^@]+@[^@]+\\.[^@]+$"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"email": "user@example.com"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeOpenAIMessages(tt.messages, tt.tools)
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("sanitizeOpenAIMessages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNormalizeAndDecodeArguments_Extended(t *testing.T) {
	tests := []struct {
		name      string
		args      string
		wantArgs  string
		wantValid bool
	}{
		{"tab_only", "\t", "{}", true},
		{"newline_only", "\n", "{}", true},
		{"mixed_whitespace", " \t\n ", "{}", true},
		{"valid_null", "null", "null", true},
		{"valid_number", "42", "42", true},
		{"valid_true", "true", "true", true},
		{"truncated_json", `{"key":`, "", false},
		{"half_json_object", `{"key": "val"`, "", false},
		{"extra_closing_brace", `{}}`, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, _, err := normalizeAndDecodeArguments(tt.args)
			if tt.wantValid {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
				if normalized != tt.wantArgs {
					t.Errorf("expected normalized %q, got %q", tt.wantArgs, normalized)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			}
		})
	}
}

func TestResolveSchemaRef(t *testing.T) {
	defs := map[string]any{
		"StringDef": map[string]any{"type": "string"},
		"IntDef":    map[string]any{"type": "integer"},
		"NotAMap":   "just a string",
	}

	tests := []struct {
		name string
		ref  string
		defs map[string]any
		want map[string]any
	}{
		{"valid_defs_ref", "#/$defs/StringDef", defs, map[string]any{"type": "string"}},
		{"valid_definitions_ref_legacy", "#/definitions/IntDef", defs, map[string]any{"type": "integer"}},
		{"unknown_prefix", "#/components/schemas/Foo", defs, nil},
		{"empty_name", "#/$defs/", defs, nil},
		{"missing_key", "#/$defs/NotFound", defs, nil},
		{"not_a_map", "#/$defs/NotAMap", defs, nil},
		{"nil_defs", "#/$defs/StringDef", nil, nil},
		{"empty_ref_string", "", defs, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSchemaRef(tt.ref, tt.defs)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("resolveSchemaRef() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetDefs(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   map[string]any
	}{
		{"nil_schema", nil, nil},
		{"no_defs_key", map[string]any{"type": "object"}, nil},
		{"valid_defs", map[string]any{"$defs": map[string]any{"Foo": map[string]any{"type": "string"}}}, map[string]any{"Foo": map[string]any{"type": "string"}}},
		{"legacy_definitions", map[string]any{"definitions": map[string]any{"Bar": map[string]any{"type": "integer"}}}, map[string]any{"Bar": map[string]any{"type": "integer"}}},
		{"defs_not_a_map", map[string]any{"$defs": "not a map"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDefs(tt.schema)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("getDefs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateValueAgainstSchema(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		defs   map[string]any
		wantOK bool
	}{
		{"nil_schema", "anything", nil, nil, true},
		{"nil_value", nil, map[string]any{"type": "string"}, nil, true},
		{"ref_resolution", "hello", map[string]any{"$ref": "#/$defs/StrDef"}, map[string]any{"StrDef": map[string]any{"type": "string"}}, true},
		{"ref_resolution_type_mismatch", 42, map[string]any{"$ref": "#/$defs/StrDef"}, map[string]any{"StrDef": map[string]any{"type": "string"}}, false},
		{"unresolvable_ref_skips_validation", "anything", map[string]any{"$ref": "#/$defs/Missing"}, map[string]any{"OtherDef": map[string]any{"type": "string"}}, true},
		{"unknown_schema_type_passes", "hello", map[string]any{"type": "custom_unknown"}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateValueAgainstSchema(tt.value, tt.schema, tt.defs, "$")
			if ok != tt.wantOK {
				t.Errorf("validateValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateObjectValueAgainstSchema_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		defs   map[string]any
		wantOK bool
	}{
		{"non_map_value", "not_a_map", map[string]any{"type": "object"}, nil, false},
		{"prop_schema_not_a_map_skipped", map[string]any{"key": "val"}, map[string]any{"properties": map[string]any{"key": "not_a_map"}}, nil, true},
		{"empty_object_valid_for_object_schema", map[string]any{}, map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateObjectValueAgainstSchema(tt.value, tt.schema, tt.defs, "$")
			if ok != tt.wantOK {
				t.Errorf("validateObjectValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateArrayValueAgainstSchema_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		defs   map[string]any
		wantOK bool
	}{
		{"non_slice_value", "not_an_array", map[string]any{"type": "array"}, nil, false},
		{"no_items_in_schema", []any{1, 2}, map[string]any{"type": "array"}, nil, true},
		{"items_not_a_map_skipped", []any{1, 2}, map[string]any{"type": "array", "items": "not_a_map"}, nil, true},
		{"empty_array_valid", []any{}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateArrayValueAgainstSchema(tt.value, tt.schema, tt.defs, "$")
			if ok != tt.wantOK {
				t.Errorf("validateArrayValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateStringValueAgainstSchema_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		wantOK bool
	}{
		{"non_string_value", 42, map[string]any{"type": "string"}, false},
		{"nil_schema_no_pattern", "hello", nil, true},
		{"empty_pattern", "hello", map[string]any{"type": "string", "pattern": ""}, true},
		{"uncompilable_pattern_skips_validation", "hello", map[string]any{"type": "string", "pattern": "(?P<invalid"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateStringValueAgainstSchema(tt.value, tt.schema, "$")
			if ok != tt.wantOK {
				t.Errorf("validateStringValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateArgumentsAgainstSchema_NilArgs(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		wantOK bool
	}{
		{"nil_args_object_schema", map[string]any{"type": "object"}, false},
		{"nil_args_array_schema", map[string]any{"type": "array"}, false},
		{"nil_args_string_schema", map[string]any{"type": "string"}, false},
		{"nil_args_boolean_schema", map[string]any{"type": "boolean"}, false},
		{"nil_args_integer_schema", map[string]any{"type": "integer"}, false},
		{"nil_args_number_schema", map[string]any{"type": "number"}, false},
		{"nil_args_unknown_schema", map[string]any{"type": "custom"}, true},
		{"nil_schema", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, _ := validateArgumentsAgainstSchema(nil, tt.schema)
			if ok != tt.wantOK {
				t.Errorf("validateArgumentsAgainstSchema(nil, schema) = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestDowngradeFunctions_NonStringContent(t *testing.T) {
	t.Run("downgrade_invalid_tool_result_non_string_content", func(t *testing.T) {
		orig := message{Role: "tool", ToolCallID: "tc_1", Content: map[string]any{"key": "val"}}
		msg := downgradeInvalidToolResult(orig)
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, invalidToolResultTag) {
			t.Errorf("expected content to contain %q", invalidToolResultTag)
		}
		if !strings.Contains(content, `"key"`) {
			t.Errorf("expected content to contain original content, got %q", content)
		}
	})

	t.Run("downgrade_orphan_tool_result_non_string_content", func(t *testing.T) {
		orig := message{Role: "tool", ToolCallID: "tc_2", Content: []any{1, 2, 3}}
		msg := downgradeOrphanToolResult(orig)
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, orphanToolResultTag) {
			t.Errorf("expected content to contain %q", orphanToolResultTag)
		}
	})

	t.Run("downgrade_invalid_tool_result_nil_content", func(t *testing.T) {
		orig := message{Role: "tool", ToolCallID: "tc_3"}
		msg := downgradeInvalidToolResult(orig)
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, invalidToolResultTag) {
			t.Errorf("expected content to contain %q", invalidToolResultTag)
		}
	})

	t.Run("downgrade_orphan_tool_result_nil_content", func(t *testing.T) {
		orig := message{Role: "tool", ToolCallID: "tc_4"}
		msg := downgradeOrphanToolResult(orig)
		content, ok := msg.Content.(string)
		if !ok {
			t.Fatalf("expected string content, got %T", msg.Content)
		}
		if !strings.Contains(content, orphanToolResultTag) {
			t.Errorf("expected content to contain %q", orphanToolResultTag)
		}
	})
}

func TestIsEmptyAssistantMessage_Extended(t *testing.T) {
	tests := []struct {
		name string
		msg  message
		want bool
	}{
		{"non_string_content_non_empty", message{Role: "assistant", Content: []any{1, 2}}, false},
		{"non_string_content_map", message{Role: "assistant", Content: map[string]any{"key": "val"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyAssistantMessage(tt.msg)
			if got != tt.want {
				t.Errorf("isEmptyAssistantMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInferSchemaType_Extended(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{"type_is_number", map[string]any{"type": "number"}, "number"},
		{"type_is_integer", map[string]any{"type": "integer"}, "integer"},
		{"type_is_boolean", map[string]any{"type": "boolean"}, "boolean"},
		{"type_is_empty_string", map[string]any{"type": ""}, ""},
		{"type_is_non_string_ignored", map[string]any{"type": []any{"string", "null"}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSchemaType(tt.schema)
			if got != tt.want {
				t.Errorf("inferSchemaType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Comprehensive edge-case tests for near-100% coverage ---

func TestSanitizeOpenAIMessages_ComprehensiveEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		messages []message
		tools    []tool
		want     []message
	}{
		// --- Multiple tool results for the same invalid tool call ID ---
		{
			name: "multiple_tool_results_for_same_invalid_call_id",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{bad}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "first result"},
				{Role: "tool", ToolCallID: "tc_1", Content: "second result"},
				{Role: "assistant", Content: "Done"},
			},
			tools: []tool{tl("my_func", nil)},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (arguments are not valid JSON).\nname: my_func\nid: tc_1\narguments:\n```text\n{bad}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nfirst result\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nsecond result\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- Tool result with non-string content (map) in a valid kept round ---
		{
			name: "tool_result_with_map_content_in_valid_round",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: map[string]any{"key": "val"}},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: map[string]any{"key": "val"}},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- Array args at top level where object schema expected ---
		{
			name: "top_level_array_args_where_object_schema_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `[1,2,3]`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected object at $).\nname: my_func\nid: tc_1\narguments:\n```text\n[1,2,3]\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Null args where object schema expected ---
		{
			name: "null_args_where_object_schema_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected object at $).\nname: my_func\nid: tc_1\narguments:\n```text\nnull\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Null args where array schema expected ---
		{
			name: "null_args_where_array_schema_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected array at $).\nname: my_func\nid: tc_1\narguments:\n```text\nnull\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Null args where string schema expected ---
		{
			name: "null_args_where_string_schema_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{"type": "string"})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected string at $).\nname: my_func\nid: tc_1\narguments:\n```text\nnull\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Null args where boolean/integer/number schema expected ---
		{
			name: "null_args_where_boolean_schema_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{"type": "boolean"})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected boolean at $).\nname: my_func\nid: tc_1\narguments:\n```text\nnull\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		{
			name: "null_args_where_integer_schema_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{"type": "integer"})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected integer at $).\nname: my_func\nid: tc_1\narguments:\n```text\nnull\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		{
			name: "null_args_where_number_schema_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{"type": "number"})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected number at $).\nname: my_func\nid: tc_1\narguments:\n```text\nnull\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Null args where unknown schema type → passes ---
		{
			name: "null_args_where_unknown_schema_type_passes",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{"type": "null"})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `null`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- Nested $ref resolution in property schema ---
		{
			name: "nested_ref_in_property_schema",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"item": {"name": "valid"}}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"item": map[string]any{"$ref": "#/$defs/ItemDef"},
				},
				"$defs": map[string]any{
					"ItemDef": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
						},
					},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"item": {"name": "valid"}}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- $ref in array items schema ---
		{
			name: "ref_in_array_items_schema",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"items": [{"name": "a"}, {"name": "b"}]}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/$defs/ItemDef"},
					},
				},
				"$defs": map[string]any{
					"ItemDef": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
						},
					},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"items": [{"name": "a"}, {"name": "b"}]}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- $ref in array items schema: mismatch ---
		{
			name: "ref_in_array_items_schema_mismatch",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"items": [{"name": "a"}, 42]}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/$defs/ItemDef"},
					},
				},
				"$defs": map[string]any{
					"ItemDef": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
						},
					},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected object at $.items[1]).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"items\": [{\"name\": \"a\"}, 42]}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Deep nested validation failure ---
		{
			name: "deep_nested_validation_failure",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"outer": {"inner": "not_a_number"}}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"outer": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"inner": map[string]any{"type": "integer"},
						},
					},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected integer at $.outer.inner).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"outer\": {\"inner\": \"not_a_number\"}}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Assistant with non-string content (e.g., content parts) and all orphaned tool calls ---
		{
			name: "assistant_non_string_content_with_orphaned_tool_calls",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: []any{map[string]any{"type": "text", "text": "I will call a tool"}}, ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "user", Content: "Continue"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: []any{map[string]any{"type": "text", "text": "I will call a tool"}}},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: tc_1\narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
		// --- Empty tool call ID + tool result with empty ID within round: both treated as orphans ---
		{
			name: "empty_id_call_and_empty_id_result_within_round",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("", "func1", `{}`)}},
				{Role: "tool", Content: "no_id_result"},
				{Role: "user", Content: "Continue"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[orphan_tool_call] Tool call was downgraded to a user message because no matching tool result exists.\nname: func1\nid: \narguments:\n```text\n{}\n```"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: \ntool_name: \ncontent:\n```text\nno_id_result\n```"},
				{Role: "user", Content: "Continue"},
			},
		},
		// --- Tool result referencing a call from a different round → orphan ---
		// In the second round, tc_1 result is orphan (not in validIDs/invalidIDs of tc_2's round),
		// and tc_2 result is kept. Output order: kept results first, then orphan results.
		{
			name: "tool_result_referencing_different_round_call_is_orphan",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result1"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "func2", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "stale_result_for_tc1"},
				{Role: "tool", ToolCallID: "tc_2", Content: "result2"},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "func1", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result1"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_2", "func2", `{}`)}},
				{Role: "tool", ToolCallID: "tc_2", Content: "result2"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nstale_result_for_tc1\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- Consecutive orphan tool results at top level ---
		{
			name: "consecutive_orphan_tool_results_at_top_level",
			messages: []message{
				{Role: "tool", ToolCallID: "orphan1", Content: "r1"},
				{Role: "tool", ToolCallID: "orphan2", Content: "r2"},
				{Role: "tool", ToolCallID: "orphan3", Content: "r3"},
				{Role: "assistant", Content: "Done"},
			},
			tools: nil,
			want: []message{
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: orphan1\ntool_name: \ncontent:\n```text\nr1\n```"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: orphan2\ntool_name: \ncontent:\n```text\nr2\n```"},
				{Role: "user", Content: "[orphan_tool_result] Tool result was downgraded to a user message because it is orphaned.\ntool_call_id: orphan3\ntool_name: \ncontent:\n```text\nr3\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- All tool calls invalid: empty assistant suppressed ---
		{
			name: "all_tool_calls_invalid_empty_assistant_suppressed",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{bad}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
				{Role: "assistant", Content: "Done"},
			},
			tools: []tool{tl("my_func", map[string]any{"type": "object", "properties": map[string]any{}})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (arguments are not valid JSON).\nname: my_func\nid: tc_1\narguments:\n```text\n{bad}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
				{Role: "assistant", Content: "Done"},
			},
		},
		// --- String value where array expected ---
		{
			name: "string_where_array_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"items": "not_an_array"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{"type": "array"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected array at $.items).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"items\": \"not_an_array\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Object value where string expected ---
		{
			name: "object_where_string_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": {"nested": true}}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected string at $.name).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"name\": {\"nested\": true}}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- String value where integer expected ---
		{
			name: "string_where_integer_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"count": "42"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected integer at $.count).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"count\": \"42\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- String value where number expected ---
		{
			name: "string_where_number_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"value": "3.14"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "number"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected number at $.value).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"value\": \"3.14\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- String value where boolean expected ---
		{
			name: "string_where_boolean_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"flag": "true"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flag": map[string]any{"type": "boolean"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected boolean at $.flag).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"flag\": \"true\"}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Object property not in args: extra args pass (no additionalProperties check) ---
		{
			name: "extra_args_not_in_schema_pass",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": "test", "extra": "field"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": "test", "extra": "field"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- Schema property not provided in args: passes (no required check) ---
		{
			name: "missing_required_property_passes",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"count": map[string]any{"type": "integer"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- Array with no items schema: any content passes ---
		{
			name: "array_with_no_items_schema_any_content_passes",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"arr": [1, "two", true]}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"arr": map[string]any{"type": "array"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"arr": [1, "two", true]}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- Legacy #/definitions/ ref resolution ---
		{
			name: "legacy_definitions_ref_resolution",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"item": 42}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"item": map[string]any{"$ref": "#/definitions/IntDef"},
				},
				"definitions": map[string]any{
					"IntDef": map[string]any{"type": "integer"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"item": 42}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- Unresolvable $ref: treated as valid (skip validation) ---
		{
			name: "unresolvable_ref_skips_validation",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"item": "anything"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"item": map[string]any{"$ref": "#/$defs/MissingDef"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"item": "anything"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
		// --- Integer where string expected ---
		{
			name: "integer_where_string_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": 42}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected string at $.name).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"name\": 42}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Boolean where string expected ---
		{
			name: "boolean_where_string_expected",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": true}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "[invalid_tool_call] Tool call arguments were downgraded to a user message (expected string at $.name).\nname: my_func\nid: tc_1\narguments:\n```text\n{\"name\": true}\n```"},
				{Role: "user", Content: "[invalid_tool_result] Tool result was downgraded to a user message.\ntool_call_id: tc_1\ntool_name: \ncontent:\n```text\nresult\n```"},
			},
		},
		// --- Schema with both properties and items: properties takes precedence for type inference ---
		{
			name: "schema_properties_and_items_infers_object",
			messages: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": "test"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
			tools: []tool{tl("my_func", map[string]any{
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
				"items":      map[string]any{"type": "integer"},
			})},
			want: []message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", ToolCalls: []toolCall{tc("tc_1", "my_func", `{"name": "test"}`)}},
				{Role: "tool", ToolCallID: "tc_1", Content: "result"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeOpenAIMessages(tt.messages, tt.tools)
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("sanitizeOpenAIMessages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNormalizeAndDecodeArguments_Comprehensive(t *testing.T) {
	tests := []struct {
		name      string
		args      string
		wantArgs  string
		wantValid bool
	}{
		// json.Valid rejects multiple JSON values in one string
		{"multiple_json_values", "{}{}", "", false},
		// Trailing comma in object
		{"trailing_comma_object", `{"key": "val",}`, "", false},
		// Single-quoted string (not valid JSON)
		{"single_quoted_string", `{'key': 'val'}`, "", false},
		// Unicode in JSON
		{"unicode_in_json", `{"name": "日本語"}`, `{"name": "日本語"}`, true},
		// Escaped characters
		{"escaped_characters", `{"path": "C:\\Users\\test"}`, `{"path": "C:\\Users\\test"}`, true},
		// Very large number
		{"large_number", "999999999999999999999", "999999999999999999999", true},
		// Negative number
		{"negative_number", "-42", "-42", true},
		// Floating point
		{"floating_point", "3.14", "3.14", true},
		// Empty JSON object
		{"empty_object", "{}", "{}", true},
		// Nested empty object
		{"nested_empty_object", `{"a": {}}`, `{"a": {}}`, true},
		// JSON with null value
		{"json_null_value", `{"key": null}`, `{"key": null}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, _, err := normalizeAndDecodeArguments(tt.args)
			if tt.wantValid {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
				if normalized != tt.wantArgs {
					t.Errorf("expected normalized %q, got %q", tt.wantArgs, normalized)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			}
		})
	}
}

func TestValidateBooleanValueAgainstSchema_Direct(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		wantOK bool
	}{
		{"bool_true", true, true},
		{"bool_false", false, true},
		{"int_value", 42, false},
		{"string_value", "true", false},
		{"nil_value", nil, false},
		{"json_number", json.Number("1"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateBooleanValueAgainstSchema(tt.value, "$")
			if ok != tt.wantOK {
				t.Errorf("validateBooleanValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateIntegerValueAgainstSchema_Direct(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		wantOK bool
	}{
		{"valid_integer", json.Number("42"), true},
		{"negative_integer", json.Number("-7"), true},
		{"zero", json.Number("0"), true},
		{"float_fails", json.Number("3.14"), false},
		{"string_fails", "42", false},
		{"int_fails", 42, false},
		{"bool_fails", true, false},
		{"nil_fails", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateIntegerValueAgainstSchema(tt.value, "$")
			if ok != tt.wantOK {
				t.Errorf("validateIntegerValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateNumberValueAgainstSchema_Direct(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		wantOK bool
	}{
		{"valid_integer_as_number", json.Number("42"), true},
		{"valid_float", json.Number("3.14"), true},
		{"negative_number", json.Number("-1.5"), true},
		{"zero", json.Number("0"), true},
		{"overflow_float64", json.Number("1e9999"), false},
		{"string_fails", "3.14", false},
		{"int_fails", 42, false},
		{"bool_fails", true, false},
		{"nil_fails", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateNumberValueAgainstSchema(tt.value, "$")
			if ok != tt.wantOK {
				t.Errorf("validateNumberValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestResolveSchemaRef_Comprehensive(t *testing.T) {
	defs := map[string]any{
		"StrDef":  map[string]any{"type": "string"},
		"NotAMap": "just a string",
	}

	tests := []struct {
		name string
		ref  string
		defs map[string]any
		want map[string]any
	}{
		{"valid_defs_ref", "#/$defs/StrDef", defs, map[string]any{"type": "string"}},
		{"unknown_prefix", "#/components/schemas/Foo", defs, nil},
		{"empty_name_defs", "#/$defs/", defs, nil},
		{"missing_key_defs", "#/$defs/NotFound", defs, nil},
		{"not_a_map_defs", "#/$defs/NotAMap", defs, nil},
		{"nil_defs", "#/$defs/StrDef", nil, nil},
		{"empty_ref_string", "", defs, nil},
		// Legacy #/definitions/
		{"valid_definitions_ref", "#/definitions/StrDef", defs, map[string]any{"type": "string"}},
		{"empty_name_definitions", "#/definitions/", defs, nil},
		{"missing_key_definitions", "#/definitions/NotFound", defs, nil},
		{"not_a_map_definitions", "#/definitions/NotAMap", defs, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSchemaRef(tt.ref, tt.defs)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("resolveSchemaRef() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateArgumentsAgainstSchema_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		args   any
		schema map[string]any
		wantOK bool
	}{
		// nil args with $ref resolving to object → fails
		{"nil_args_ref_to_object", nil, map[string]any{"$ref": "#/$defs/ObjDef"}, false},
		// nil args with unresolvable $ref → passes
		{"nil_args_unresolvable_ref", nil, map[string]any{"$ref": "#/$defs/Missing"}, true},
		// nil args with $ref resolving to unknown type → passes
		{"nil_args_ref_to_unknown_type", nil, map[string]any{"$ref": "#/$defs/CustomDef"}, true},
		// nil schema → passes
		{"nil_schema", map[string]any{"key": "val"}, nil, true},
		// nil args and nil schema → passes
		{"nil_args_nil_schema", nil, nil, true},
		// valid object with matching schema
		{"valid_object_matching_schema", map[string]any{"name": "test"}, map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}, true},
		// object with wrong property type
		{"object_wrong_property_type", map[string]any{"name": 42}, map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}, false},
	}

	defsMap := map[string]any{
		"$defs": map[string]any{
			"ObjDef":    map[string]any{"type": "object"},
			"CustomDef": map[string]any{"type": "custom_unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := tt.schema
			// For $ref tests, inject $defs into the schema
			if _, hasRef := schema["$ref"]; hasRef {
				merged := make(map[string]any, len(schema)+1)
				for k, v := range schema {
					merged[k] = v
				}
				for k, v := range defsMap {
					merged[k] = v
				}
				schema = merged
			}
			ok, _ := validateArgumentsAgainstSchema(tt.args, schema)
			if ok != tt.wantOK {
				t.Errorf("validateArgumentsAgainstSchema() = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestSplitToolCalls_Direct(t *testing.T) {
	tests := []struct {
		name       string
		toolCalls  []toolCall
		toolResults []message
		wantKept   int
		wantOrphan int
	}{
		{"all_matched", []toolCall{tc("1", "f", `{}`)}, []message{{Role: "tool", ToolCallID: "1"}}, 1, 0},
		{"none_matched", []toolCall{tc("1", "f", `{}`)}, []message{{Role: "tool", ToolCallID: "2"}}, 0, 1},
		{"empty_results", []toolCall{tc("1", "f", `{}`)}, []message{}, 0, 1},
		{"empty_calls", []toolCall{}, []message{{Role: "tool", ToolCallID: "1"}}, 0, 0},
		{"both_empty", []toolCall{}, []message{}, 0, 0},
		{"empty_id_call_always_orphan", []toolCall{tc("", "f", `{}`)}, []message{{Role: "tool", ToolCallID: ""}}, 0, 1},
		{"partial_match", []toolCall{tc("1", "f", `{}`), tc("2", "f", `{}`), tc("3", "f", `{}`)}, []message{{Role: "tool", ToolCallID: "1"}, {Role: "tool", ToolCallID: "3"}}, 2, 1},
		{"result_with_empty_id_ignored", []toolCall{tc("1", "f", `{}`)}, []message{{Role: "tool", ToolCallID: ""}, {Role: "tool", ToolCallID: "1"}}, 1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			split := splitToolCalls(tt.toolCalls, tt.toolResults)
			if len(split.kept) != tt.wantKept {
				t.Errorf("kept: got %d, want %d", len(split.kept), tt.wantKept)
			}
			if len(split.orphan) != tt.wantOrphan {
				t.Errorf("orphan: got %d, want %d", len(split.orphan), tt.wantOrphan)
			}
		})
	}
}

func TestSplitToolResults_Direct(t *testing.T) {
	validIDs := map[string]struct{}{"v1": {}, "v2": {}}
	invalidIDs := map[string]struct{}{"i1": {}}

	tests := []struct {
		name          string
		toolResults   []message
		validIDs      map[string]struct{}
		invalidIDs    map[string]struct{}
		wantKept      int
		wantOrphan    int
		wantInvalidByIDCount int
	}{
		{"all_valid", []message{{Role: "tool", ToolCallID: "v1"}, {Role: "tool", ToolCallID: "v2"}}, validIDs, invalidIDs, 2, 0, 0},
		{"all_invalid", []message{{Role: "tool", ToolCallID: "i1"}}, validIDs, invalidIDs, 0, 0, 1},
		{"all_orphan", []message{{Role: "tool", ToolCallID: "unknown"}}, validIDs, invalidIDs, 0, 1, 0},
		{"empty_id_is_orphan", []message{{Role: "tool", ToolCallID: ""}}, validIDs, invalidIDs, 0, 1, 0},
		{"duplicate_valid_is_orphan", []message{{Role: "tool", ToolCallID: "v1"}, {Role: "tool", ToolCallID: "v1"}}, validIDs, invalidIDs, 1, 1, 0},
		{"multiple_for_same_invalid", []message{{Role: "tool", ToolCallID: "i1"}, {Role: "tool", ToolCallID: "i1"}}, validIDs, invalidIDs, 0, 0, 1},
		{"mixed", []message{{Role: "tool", ToolCallID: "v1"}, {Role: "tool", ToolCallID: "i1"}, {Role: "tool", ToolCallID: "unknown"}, {Role: "tool", ToolCallID: ""}}, validIDs, invalidIDs, 1, 2, 1},
		{"empty_results", []message{}, validIDs, invalidIDs, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			split := splitToolResults(tt.toolResults, tt.validIDs, tt.invalidIDs)
			if len(split.kept) != tt.wantKept {
				t.Errorf("kept: got %d, want %d", len(split.kept), tt.wantKept)
			}
			if len(split.orphan) != tt.wantOrphan {
				t.Errorf("orphan: got %d, want %d", len(split.orphan), tt.wantOrphan)
			}
			if len(split.invalidByID) != tt.wantInvalidByIDCount {
				t.Errorf("invalidByID count: got %d, want %d", len(split.invalidByID), tt.wantInvalidByIDCount)
			}
		})
	}
}

func TestIsEmptyAssistantMessage_Comprehensive(t *testing.T) {
	tests := []struct {
		name string
		msg  message
		want bool
	}{
		{"empty_assistant", message{Role: "assistant"}, true},
		{"with_empty_string_content", message{Role: "assistant", Content: ""}, true},
		{"with_nil_content", message{Role: "assistant", Content: nil}, true},
		{"with_non_empty_string", message{Role: "assistant", Content: "hello"}, false},
		{"with_non_string_content_slice", message{Role: "assistant", Content: []any{1, 2}}, false},
		{"with_non_string_content_map", message{Role: "assistant", Content: map[string]any{"key": "val"}}, false},
		{"with_empty_non_nil_slice", message{Role: "assistant", Content: []any{}}, false},
		{"with_tool_calls", message{Role: "assistant", ToolCalls: []toolCall{{ID: "1"}}}, false},
		{"with_empty_tool_calls_slice", message{Role: "assistant", ToolCalls: []toolCall{}}, true},
		{"with_reasoning_content", message{Role: "assistant", ReasoningContent: "thinking"}, false},
		{"non_assistant_role", message{Role: "user", Content: "hello"}, false},
		{"assistant_with_both_content_and_reasoning", message{Role: "assistant", Content: "hi", ReasoningContent: "thinking"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyAssistantMessage(tt.msg)
			if got != tt.want {
				t.Errorf("isEmptyAssistantMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInferSchemaType_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{"nil_schema", nil, ""},
		{"explicit_type_string", map[string]any{"type": "string"}, "string"},
		{"explicit_type_object", map[string]any{"type": "object"}, "object"},
		{"explicit_type_array", map[string]any{"type": "array"}, "array"},
		{"explicit_type_number", map[string]any{"type": "number"}, "number"},
		{"explicit_type_integer", map[string]any{"type": "integer"}, "integer"},
		{"explicit_type_boolean", map[string]any{"type": "boolean"}, "boolean"},
		{"type_empty_string", map[string]any{"type": ""}, ""},
		{"type_non_string_ignored", map[string]any{"type": []any{"string", "null"}}, ""},
		{"inferred_from_properties", map[string]any{"properties": map[string]any{}}, "object"},
		{"inferred_from_items", map[string]any{"items": map[string]any{}}, "array"},
		// properties takes precedence over items when type is absent
		{"both_properties_and_items_prefers_properties", map[string]any{"properties": map[string]any{}, "items": map[string]any{}}, "object"},
		{"no_type_info", map[string]any{}, ""},
		{"only_description", map[string]any{"description": "a tool"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSchemaType(tt.schema)
			if got != tt.want {
				t.Errorf("inferSchemaType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateObjectValueAgainstSchema_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		defs   map[string]any
		wantOK bool
	}{
		{"non_map_value", "not_a_map", map[string]any{"type": "object"}, nil, false},
		{"prop_schema_not_a_map_skipped", map[string]any{"key": "val"}, map[string]any{"properties": map[string]any{"key": "not_a_map"}}, nil, true},
		{"empty_object_valid_for_object_schema", map[string]any{}, map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}, nil, true},
		{"nil_properties_valid", map[string]any{"key": "val"}, map[string]any{"type": "object"}, nil, true},
		// Property with $ref in the property schema
		{"property_with_ref_valid", map[string]any{"item": "hello"}, map[string]any{"properties": map[string]any{"item": map[string]any{"$ref": "#/$defs/StrDef"}}}, map[string]any{"StrDef": map[string]any{"type": "string"}}, true},
		{"property_with_ref_invalid", map[string]any{"item": 42}, map[string]any{"properties": map[string]any{"item": map[string]any{"$ref": "#/$defs/StrDef"}}}, map[string]any{"StrDef": map[string]any{"type": "string"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateObjectValueAgainstSchema(tt.value, tt.schema, tt.defs, "$")
			if ok != tt.wantOK {
				t.Errorf("validateObjectValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateArrayValueAgainstSchema_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		defs   map[string]any
		wantOK bool
	}{
		{"non_slice_value", "not_an_array", map[string]any{"type": "array"}, nil, false},
		{"no_items_in_schema", []any{1, 2}, map[string]any{"type": "array"}, nil, true},
		{"items_not_a_map_skipped", []any{1, 2}, map[string]any{"type": "array", "items": "not_a_map"}, nil, true},
		{"empty_array_valid", []any{}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, nil, true},
		// Array items with $ref
		{"items_with_ref_valid", []any{"a", "b"}, map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/StrDef"}}, map[string]any{"StrDef": map[string]any{"type": "string"}}, true},
		{"items_with_ref_invalid", []any{"a", 42}, map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/StrDef"}}, map[string]any{"StrDef": map[string]any{"type": "string"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateArrayValueAgainstSchema(tt.value, tt.schema, tt.defs, "$")
			if ok != tt.wantOK {
				t.Errorf("validateArrayValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateStringValueAgainstSchema_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		wantOK bool
	}{
		{"valid_string", "hello", map[string]any{"type": "string"}, true},
		{"non_string_value", 42, map[string]any{"type": "string"}, false},
		{"nil_schema", "hello", nil, true},
		{"empty_pattern", "hello", map[string]any{"type": "string", "pattern": ""}, true},
		{"pattern_match", "user@example.com", map[string]any{"type": "string", "pattern": "^[^@]+@[^@]+\\.[^@]+$"}, true},
		{"pattern_mismatch", "not-an-email", map[string]any{"type": "string", "pattern": "^[^@]+@[^@]+\\.[^@]+$"}, false},
		{"uncompilable_pattern_skips", "hello", map[string]any{"type": "string", "pattern": "(?P<invalid"}, true},
		{"pattern_not_string_in_schema", "hello", map[string]any{"type": "string", "pattern": []any{1, 2}}, true},
		{"empty_string_valid", "", map[string]any{"type": "string"}, true},
		{"bool_not_string", true, map[string]any{"type": "string"}, false},
		{"nil_not_string", nil, map[string]any{"type": "string"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateStringValueAgainstSchema(tt.value, tt.schema, "$")
			if ok != tt.wantOK {
				t.Errorf("validateStringValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateValueAgainstSchema_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema map[string]any
		defs   map[string]any
		wantOK bool
	}{
		{"nil_schema", "anything", nil, nil, true},
		{"nil_value", nil, map[string]any{"type": "string"}, nil, true},
		{"both_nil", nil, nil, nil, true},
		{"ref_resolution_valid", "hello", map[string]any{"$ref": "#/$defs/StrDef"}, map[string]any{"StrDef": map[string]any{"type": "string"}}, true},
		{"ref_resolution_type_mismatch", 42, map[string]any{"$ref": "#/$defs/StrDef"}, map[string]any{"StrDef": map[string]any{"type": "string"}}, false},
		{"unresolvable_ref_skips", "anything", map[string]any{"$ref": "#/$defs/Missing"}, map[string]any{"OtherDef": map[string]any{"type": "string"}}, true},
		{"unknown_schema_type_passes", "hello", map[string]any{"type": "custom_unknown"}, nil, true},
		// Verify all type dispatches work
		{"object_type_dispatch", map[string]any{"key": "val"}, map[string]any{"type": "object"}, nil, true},
		{"array_type_dispatch", []any{1, 2}, map[string]any{"type": "array"}, nil, true},
		{"string_type_dispatch", "hello", map[string]any{"type": "string"}, nil, true},
		{"boolean_type_dispatch", true, map[string]any{"type": "boolean"}, nil, true},
		{"integer_type_dispatch", json.Number("42"), map[string]any{"type": "integer"}, nil, true},
		{"number_type_dispatch", json.Number("3.14"), map[string]any{"type": "number"}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := validateValueAgainstSchema(tt.value, tt.schema, tt.defs, "$")
			if ok != tt.wantOK {
				t.Errorf("validateValueAgainstSchema() = %v, reason=%q, want %v", ok, reason, tt.wantOK)
			}
		})
	}
}

func TestValidateToolCallArguments_Comprehensive(t *testing.T) {
	tools := map[string]tool{
		"my_func": {Type: "function", Function: function{Name: "my_func", Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		}}},
		"no_params_func": {Type: "function", Function: function{Name: "no_params_func"}},
	}

	tests := []struct {
		name     string
		toolName string
		args     any
		tools    map[string]tool
		wantOK   bool
	}{
		{"nil_tools_passes", "any_func", map[string]any{}, nil, true},
		{"unknown_tool_passes", "unknown", map[string]any{}, tools, true},
		{"nil_parameters_passes", "no_params_func", "not_an_object", tools, true},
		{"valid_args_passes", "my_func", map[string]any{"name": "test"}, tools, true},
		{"invalid_args_fails", "my_func", map[string]any{"name": 42}, tools, false},
		{"empty_tools_map", "any_func", map[string]any{}, map[string]tool{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, _ := validateToolCallArguments(tt.toolName, tt.args, tt.tools)
			if ok != tt.wantOK {
				t.Errorf("validateToolCallArguments() = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}
