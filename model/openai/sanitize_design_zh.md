# OpenAI 消息净化设计文档

## 概述

`sanitizeOpenAIMessages` 是在消息历史发送到 OpenAI API 之前的最后一道防线。它解决的核心问题是：消息历史中的工具调用（tool_call）和工具响应（tool result）如果不严格配对、或参数格式不合法，OpenAI API 会直接返回 400 Bad Request。

## 数据来源

函数接收两个独立来源的参数：

| 参数 | 来源 | 含义 |
|---|---|---|
| `messages` | `req.Contents` → `convertGenAIContent` | 对话历史中的消息（含历史 tool_call / tool 响应） |
| `tools` | `req.Config.Tools` → `convertFunctionDeclaration` | 本次请求可用的工具声明（含函数名、参数 Schema） |

`tools` **不是**从 messages 中解析的，而是当前请求的工具定义。历史消息中的 tool_call 可能调用的是旧轮次的工具——如果工具定义发生了变化，当前 `tools` 中可能找不到对应声明，此时 schema 校验会被跳过，视为合法。

### 消息角色

到达 `sanitizeOpenAIMessages` 时，messages 中每条消息的 `role` 只有四种值：

| Role | 来源 | 净化处理 |
|---|---|---|
| `"system"` | `req.Config.SystemInstruction` | 直接透传 |
| `"user"` | `genai.RoleUser` 的 Content | 直接透传 |
| `"assistant"` | `genai.RoleModel` 的 Content（可能含 FunctionCall → ToolCalls） | 需净化：校验参数、配对响应 |
| `"tool"` | 含 FunctionResponse 的 Content（原始 role 被丢弃，仅输出 tool 消息） | 需净化：检查是否有配对的 tool_call |

## 设计原则

### 1. 兜底防线，非完整校验器

净化的目标是拦截**会导致 API 层面报错**的问题，而不是做一个完整的 JSON Schema validator。校验边界恰好停在"会导致 OpenAI API 返回 400"的问题上：

- 非法 JSON 参数 → API 一定报错 → **必须拦截**
- 参数类型不匹配 schema → API 可能报错 → **需要拦截**
- 缺少 required 字段 → API 不会报错 → **不拦截**（理由见下文设计决策）

### 2. 降级而非丢弃

旧版实现直接丢弃孤立/无效的消息。新版将它们降级为 `role: "user"` 的消息，用特定标签保留上下文：

| 标签 | 场景 |
|---|---|
| `[invalid_tool_call]` | 参数不是合法 JSON，或不符合工具的 JSON Schema |
| `[invalid_tool_result]` | 对应的 tool_call 被判定为无效 |
| `[orphan_tool_call]` | tool_call 没有配对的 tool 响应 |
| `[orphan_tool_result]` | tool 响应没有配对的 tool_call，或同一 ID 的重复响应 |

降级保留了信息：模型仍然能看到原始的工具名称、调用 ID 和参数，只是以文本形式而非结构化形式呈现。丢弃则会造成信息黑洞。

### 3. 轮次驱动处理

不以全局扫描的方式处理消息，而是按轮次（round）驱动：

```
assistant(tool_calls) + tool + tool + ... = 一个轮次
```

每个轮次独立净化：先校验 tool_call 参数，再按合法性分组 tool 响应，最后输出过滤后的消息序列。独立出现的 `tool` 消息（没有前驱 assistant tool_call）直接视为 orphan 降级。

## 处理流程

### 主循环

```
遍历 messages:
  ├─ role="assistant" 且有 ToolCalls → 收集紧随的 tool 消息，作为一个轮次净化
  ├─ role="tool"（独立出现）→ 降级为 user 消息
  └─ 其他（system / user / assistant 无 ToolCalls）→ 直接透传
```

### 轮次净化流程

```
输入: assistant(tool_calls) + tool results

Step 1: 参数校验 (validateToolCalls)
  ├─ 每个 tool_call 分别校验:
  │   ├─ arguments 是否合法 JSON
  │   └─ arguments 是否符合工具的 JSON Schema
  ├─ 校验通过 → validToolCalls / validIDs
  └─ 校验失败 → invalidToolCalls / invalidIDs（附失败原因）

Step 2: 工具响应分组 (splitToolResults)
  ├─ ToolCallID 在 validIDs 中 → kept（最多保留一个，重复视为 orphan）
  ├─ ToolCallID 在 invalidIDs 中 → invalidByID
  └─ 无匹配 ID 或 ToolCallID 为空 → orphan

Step 3: 有效调用配对 (splitToolCalls)
  ├─ validToolCall 有对应的 kept 响应 → kept
  └─ validToolCall 无对应响应 → orphan

Step 4: 组装输出
  ├─ filteredAssistant（保留有效的 ToolCalls）+ kept tool results
  ├─ orphan tool_calls → 降级为 user 消息
  ├─ invalid tool_calls + 对应的 tool results → 降级为 user 消息
  └─ orphan tool results → 降级为 user 消息

  注意: 若 filteredAssistant 为空（无文本、无推理、无工具调用），则不输出该 assistant 消息
```

## 参数校验详解

### JSON 合法性校验

`normalizeAndDecodeArguments` 对 arguments 执行以下处理：

1. `TrimSpace` 去除首尾空白
2. 空字符串或纯空白 → 规范化为 `"{}"`
3. `json.Valid` 检查合法性
4. `json.NewDecoder` + `UseNumber()` 解码（保留数值精度，区分 integer 和 number）
5. 返回规范化后的字符串 + 解码后的值

### JSON Schema 校验

支持的 JSON Schema 特性：

| 特性 | 说明 |
|---|---|
| `type` | object, array, string, boolean, integer, number |
| `properties` | 对象属性的类型校验 |
| `items` | 数组元素的类型校验 |
| `pattern` | 字符串正则匹配（Go regexp 子集，ECMA-262 不兼容时跳过） |
| `$ref` | 本地 `#/$defs/` 和 `#/definitions/` 引用解析 |
| `required` | **不校验**（见下文设计决策） |

## 设计决策

### 为什么不校验 `required` 字段

这是有意为之，而非遗漏：

1. **OpenAI API 不因缺少 required 字段而返回 400**。它只校验 arguments 是否为合法 JSON。缺少必填参数的 tool_call 会被正常传递到工具执行层，工具发现缺参数后返回错误，LLM 自行修正重试——这是 ADK 的标准自纠正流程。

2. **降级是不可逆的破坏性操作**。如果把 `required` 校验加进来，LLM 漏填参数的场景（多轮对话中很常见）会被大量降级，反而破坏了 LLM 的自纠正能力：
   - 正常流程：LLM 缺参数 → 工具返回错误 → LLM 补全参数重试 ✓
   - 强制降级：LLM 缺参数 → 整个调用被降级为文本 → LLM 无法重试 ✗

3. **工具定义可能随时间演变**。消息历史中的 tool_call 是过去产生的，`tools` 参数来自当前请求。如果工具定义新增了 required 字段，历史上合法的调用会被误判为非法。

4. **目标定义**。净化的边界应停在"会导致 API 报错"的问题上。

## 已知限制

### `type` 数组不支持

JSON Schema 规范允许 `"type": ["string", "null"]`，但当前 `inferSchemaType` 使用 `.(string)` 断言，遇到数组类型时会返回空字符串，导致跳过校验。

影响：使用数组类型 `type` 的字段将失去类型校验。但风险方向是"漏检"而非"误杀"——最坏情况是放过了一个本该校验的字段，不会导致误降级。

修复策略：在 `inferSchemaType` 中检测 value 是否匹配数组中的任意一个 type 即可。**不应**在 `default` 分支做更保守的处理（那会导致误降级）。

## 函数索引

| 函数 | 职责 |
|---|---|
| `sanitizeOpenAIMessages` | 主入口：轮次驱动遍历消息列表，分组净化 |
| `sanitizeToolRound` | 净化单个 assistant + tool 轮次 |
| `validateToolCalls` | 批量校验 tool_call 参数，按合法性分组 |
| `validateToolCall` | 校验单个 tool_call：JSON 合法性 + Schema 校验 |
| `normalizeAndDecodeArguments` | 修剪、规范化、解码 tool_call 的 arguments |
| `validateToolCallArguments` | 根据工具名查找 Schema 并校验解码后的参数 |
| `validateArgumentsAgainstSchema` | 根据完整 JSON Schema 校验参数 |
| `validateValueAgainstSchema` | 递归校验值与 JSON Schema 子集的匹配性 |
| `validateObjectValueAgainstSchema` | 校验 object 类型 |
| `validateArrayValueAgainstSchema` | 校验 array 类型 |
| `validateStringValueAgainstSchema` | 校验 string 类型（含 pattern） |
| `validateBooleanValueAgainstSchema` | 校验 boolean 类型 |
| `validateIntegerValueAgainstSchema` | 校验 integer 类型 |
| `validateNumberValueAgainstSchema` | 校验 number 类型 |
| `inferSchemaType` | 从 Schema 推断类型（显式 type / properties / items） |
| `getDefs` | 提取 `$defs` 或 `definitions` 定义 |
| `resolveSchemaRef` | 解析本地 `#/$defs/` 或 `#/definitions/` 引用 |
| `splitToolResults` | 按合法性将 tool 响应分为 kept / invalid / orphan |
| `splitToolCalls` | 按是否有对应响应将 tool 调用分为 kept / orphan |
| `downgradeInvalidToolCall` | 无效 tool_call → user 消息 |
| `downgradeOrphanToolCall` | 孤立 tool_call → user 消息 |
| `downgradeInvalidToolResult` | 无效 tool 响应 → user 消息 |
| `downgradeOrphanToolResult` | 孤立 tool 响应 → user 消息 |
| `isEmptyAssistantMessage` | 判断 assistant 消息是否为空（无文本、无推理、无调用） |
