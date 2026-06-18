# OpenAI Message Sanitization Design

## Overview

`sanitizeOpenAIMessages` is the last line of defense before message history is sent to the OpenAI API. The core problem it solves: if tool calls and tool results in the message history are not strictly paired, or if arguments are malformed, the OpenAI API will return a 400 Bad Request.

## Data Sources

The function receives two independent parameters:

| Parameter | Source | Meaning |
|---|---|---|
| `messages` | `req.Contents` → `convertGenAIContent` | Conversation history messages (including historical tool_calls / tool results) |
| `tools` | `req.Config.Tools` → `convertFunctionDeclaration` | Tool declarations available for the current request (with function names and parameter schemas) |

`tools` is **not** parsed from messages — it comes from the current request's tool definitions. Historical tool_calls may reference tools from previous turns; if tool definitions have changed, the current `tools` may not contain a matching declaration, in which case schema validation is skipped and the call is treated as valid.

### Message Roles

When messages reach `sanitizeOpenAIMessages`, each message's `role` can only be one of four values:

| Role | Source | Sanitization |
|---|---|---|
| `"system"` | `req.Config.SystemInstruction` | Pass through unchanged |
| `"user"` | `genai.RoleUser` Content | Pass through unchanged |
| `"assistant"` | `genai.RoleModel` Content (may contain FunctionCall → ToolCalls) | Sanitize: validate arguments, pair with results |
| `"tool"` | Content with FunctionResponse (original role is discarded; only tool messages are emitted) | Sanitize: check for matching tool_call |

## Design Principles

### 1. Last-Resort Defense, Not a Full Validator

The sanitization goal is to intercept issues that **cause API-level errors**, not to be a full JSON Schema validator. The validation boundary stops at "will cause the OpenAI API to return 400":

- Invalid JSON arguments → API will definitely error → **Must intercept**
- Argument type mismatches schema → API may error → **Should intercept**
- Missing required fields → API will not error → **Do not intercept** (see Design Decisions below)

### 2. Downgrade, Don't Drop

The previous implementation silently dropped orphaned/invalid messages. The new version downgrades them to `role: "user"` messages with specific tags to preserve context:

| Tag | Scenario |
|---|---|
| `[invalid_tool_call]` | Arguments are not valid JSON, or do not match the tool's JSON Schema |
| `[invalid_tool_result]` | The corresponding tool_call was determined to be invalid |
| `[orphan_tool_call]` | tool_call has no matching tool result |
| `[orphan_tool_result]` | tool result has no matching tool_call, or is a duplicate response for the same ID |

Downgrading preserves information: the model can still see the original tool name, call ID, and arguments in text form. Dropping creates an information void.

### 3. Round-Driven Processing

Instead of global scanning, messages are processed in rounds:

```
assistant(tool_calls) + tool + tool + ... = one round
```

Each round is sanitized independently: first validate tool_call arguments, then group tool results by validity, and finally emit the filtered message sequence. A standalone `tool` message (without a preceding assistant tool_call) is immediately treated as an orphan and downgraded.

## Processing Flow

### Main Loop

```
Iterate messages:
  ├─ role="assistant" with ToolCalls → collect following tool messages, sanitize as a round
  ├─ role="tool" (standalone) → downgrade to user message
  └─ Others (system / user / assistant without ToolCalls) → pass through unchanged
```

### Round Sanitization Flow

```
Input: assistant(tool_calls) + tool results

Step 1: Argument Validation (validateToolCalls)
  ├─ Each tool_call is validated:
  │   ├─ Are arguments valid JSON?
  │   └─ Do arguments match the tool's JSON Schema?
  ├─ Pass → validToolCalls / validIDs
  └─ Fail → invalidToolCalls / invalidIDs (with failure reason)

Step 2: Tool Result Grouping (splitToolResults)
  ├─ ToolCallID in validIDs → kept (at most one; duplicates treated as orphan)
  ├─ ToolCallID in invalidIDs → invalidByID
  └─ No matching ID or empty ToolCallID → orphan

Step 3: Valid Call Pairing (splitToolCalls)
  ├─ validToolCall has a matching kept result → kept
  └─ validToolCall has no matching result → orphan

Step 4: Assemble Output
  ├─ filteredAssistant (with valid ToolCalls only) + kept tool results
  ├─ orphan tool_calls → downgrade to user messages
  ├─ invalid tool_calls + their tool results → downgrade to user messages
  └─ orphan tool results → downgrade to user messages

  Note: If filteredAssistant is empty (no text, no reasoning, no tool calls), the assistant message is omitted
```

## Argument Validation Details

### JSON Validity Check

`normalizeAndDecodeArguments` performs the following:

1. `TrimSpace` to remove leading/trailing whitespace
2. Empty or whitespace-only → normalize to `"{}"`
3. `json.Valid` to check validity
4. `json.NewDecoder` + `UseNumber()` to decode (preserves numeric precision, distinguishes integer from number)
5. Returns the normalized string + the decoded value

### JSON Schema Validation

Supported JSON Schema features:

| Feature | Description |
|---|---|
| `type` | object, array, string, boolean, integer, number |
| `properties` | Type validation for object properties |
| `items` | Type validation for array elements |
| `pattern` | String regex matching (Go regexp subset; skipped if ECMA-262 incompatible) |
| `$ref` | Local `#/$defs/` and `#/definitions/` reference resolution |
| `required` | **Not validated** (see Design Decisions below) |

## Design Decisions

### Why `required` Fields Are Not Validated

This is intentional, not an oversight:

1. **The OpenAI API does not return 400 for missing required fields.** It only validates that arguments are valid JSON. A tool_call with missing required parameters is passed to the tool execution layer normally; the tool returns an error, and the LLM self-corrects and retries — this is the standard ADK self-correction flow.

2. **Downgrading is an irreversible destructive operation.** If `required` validation were added, the common scenario of LLMs omitting parameters (especially in multi-turn conversations) would cause mass downgrades, which destroys the LLM's self-correction ability:
   - Normal flow: LLM omits param → tool returns error → LLM retries with complete params ✓
   - Forced downgrade: LLM omits param → entire call downgraded to text → LLM cannot retry ✗

3. **Tool definitions may evolve over time.** Historical tool_calls were generated in previous turns, while `tools` comes from the current request. If a tool definition adds a new required field, historically valid calls would be incorrectly flagged as invalid.

4. **Implementation goal.** The sanitization boundary should stop at issues that cause API errors.

## Known Limitations

### Array-typed `type` Not Supported

The JSON Schema spec allows `"type": ["string", "null"]`, but the current `inferSchemaType` uses `.(string)` assertion and returns an empty string for array types, causing validation to be skipped.

Impact: Fields using array-typed `type` will lose type validation. However, the risk direction is "missed detection" rather than "false positive" — the worst case is letting through a field that should have been validated, not incorrectly downgrading a valid call.

Fix strategy: In `inferSchemaType`, check if the value matches any type in the array. Do **not** make the `default` branch more conservative (that would cause false-positive downgrades).

## Function Index

| Function | Responsibility |
|---|---|
| `sanitizeOpenAIMessages` | Entry point: round-driven iteration over message list, grouped sanitization |
| `sanitizeToolRound` | Sanitize a single assistant + tool round |
| `validateToolCalls` | Batch-validate tool_call arguments, group by validity |
| `validateToolCall` | Validate a single tool_call: JSON validity + Schema validation |
| `normalizeAndDecodeArguments` | Trim, normalize, and decode tool_call arguments |
| `validateToolCallArguments` | Look up schema by tool name and validate decoded arguments |
| `validateArgumentsAgainstSchema` | Validate arguments against a full JSON Schema |
| `validateValueAgainstSchema` | Recursively validate a value against a JSON Schema subset |
| `validateObjectValueAgainstSchema` | Validate object type |
| `validateArrayValueAgainstSchema` | Validate array type |
| `validateStringValueAgainstSchema` | Validate string type (including pattern) |
| `validateBooleanValueAgainstSchema` | Validate boolean type |
| `validateIntegerValueAgainstSchema` | Validate integer type |
| `validateNumberValueAgainstSchema` | Validate number type |
| `inferSchemaType` | Infer type from Schema (explicit type / properties / items) |
| `getDefs` | Extract `$defs` or `definitions` definitions |
| `resolveSchemaRef` | Resolve local `#/$defs/` or `#/definitions/` references |
| `splitToolResults` | Group tool results by validity: kept / invalid / orphan |
| `splitToolCalls` | Group tool calls by whether they have matching results: kept / orphan |
| `downgradeInvalidToolCall` | Invalid tool_call → user message |
| `downgradeOrphanToolCall` | Orphan tool_call → user message |
| `downgradeInvalidToolResult` | Invalid tool result → user message |
| `downgradeOrphanToolResult` | Orphan tool result → user message |
| `isEmptyAssistantMessage` | Check if an assistant message is empty (no text, no reasoning, no calls) |
