# Orchestration 设计文档

> 多 Agent 编排系统：JSON Schema → 动态构建 adkagent.Agent 树

## 目录

- [1. 概述](#1-概述)
- [2. 架构设计](#2-架构设计)
  - [2.1 整体架构](#21-整体架构)
  - [2.2 为什么选择树形 Schema](#22-为什么选择树形-schema)
  - [2.3 包结构](#23-包结构)
- [3. 组件设计](#3-组件设计)
  - [3.1 Schema — 元数据类型系统](#31-schema--元数据类型系统)
  - [3.2 Parser — 解析、校验、规范化](#32-parser--解析校验规范化)
  - [3.3 Registry — 注册表与 Provider 机制](#33-registry--注册表与-provider-机制)
  - [3.4 Builder — 递归构建引擎](#34-builder--递归构建引擎)
- [4. JSON Schema 完整参考](#4-json-schema-完整参考)
  - [4.1 顶层结构](#41-顶层结构)
  - [4.2 AgentNode 联合类型](#42-agentnode-联合类型)
  - [4.3 Registries 声明](#43-registries-声明)
  - [4.4 完整示例](#44-完整示例)
- [5. Provider 扩展指南](#5-provider-扩展指南)
- [6. 工作流程](#6-工作流程)
  - [6.1 端到端构建流程](#61-端到端构建流程)
  - [6.2 Registry 构建顺序](#62-registry-构建顺序)
  - [6.3 Builder 递归构建](#63-builder-递归构建)
- [7. 使用方式](#7-使用方式)
  - [7.1 基本用法](#71-基本用法)
  - [7.2 使用内置 Provider](#72-使用内置-provider)
  - [7.3 注册自定义 Provider](#73-注册自定义-provider)
  - [7.4 手动注册实例](#74-手动注册实例)
  - [7.5 运行示例](#75-运行示例)
- [8. 条件执行机制详解](#8-条件执行机制详解)
- [9. 校验规则参考](#9-校验规则参考)
- [10. 错误处理策略](#10-错误处理策略)
- [11. 已知限制与未来扩展](#11-已知限制与未来扩展)

---

## 1. 概述

### 背景

adk-go 基于 Google ADK 封装，能快速开发单体 Agent，但多 Agent 编排（嵌套、分支、条件执行等）需要手动组合 `ParallelAgent`/`LoopAgent`/`SequentialAgent`/`LLMAgent`，过程繁琐且不直观。

### 目标

| # | 功能 | 描述 |
|---|------|------|
| 1 | JSON Metadata Schema | 声明式描述多 Agent 编排树，供 UI 拖拽生成 |
| 2 | Schema Parser | 解析 JSON、校验合法性、规范化默认值 |
| 3 | Dynamic Builder | 基于 Schema 递归构建 `adkagent.Agent` 树 |

### 核心设计原则

- **树形 Schema**：直接映射 adk-go Agent 嵌套树结构，Builder 递归构建无需 graph→tree 转换
- **全声明式 Registry**：services → models → tools → callbacks 按序构建，工具通过 `serviceRef` 引用基础设施服务
- **Provider 注册机制**：类似 `database/sql.Register`，内置 provider 通过 `init()` 自动注册，可扩展自定义 provider
- **条件执行声明式**：`conditional_skip` provider 自动从 config 生成 `BeforeAgentCallback`
- **复用现有工厂函数**：Builder 调用 `agent.NewLLMAgent`/`NewSequentialAgent` 等，保持默认日志回调等行为一致

---

## 2. 架构设计

### 2.1 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                        UI 拖拽编排器                          │
│                    (生成 JSON metadata)                      │
└──────────────────────┬──────────────────────────────────────┘
                       │ JSON bytes
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                      Parser (解析层)                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│  │ Unmarshal │→│ Validate │→│ Normalize│                   │
│  └──────────┘  └──────────┘  └──────────┘                  │
│  输出: *OrchestrationSchema (已校验、已规范化)                  │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                    Registry (注册表层)                        │
│                                                              │
│  构建顺序: services → models → tools → callbacks             │
│                                                              │
│  ┌────────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │ ServiceRegistry │  │ ModelRegistry │  │ ToolRegistry │    │
│  │ (基础设施服务)   │  │ (LLM 模型)    │  │ (工具实例)   │     │
│  └────────────────┘  └──────────────┘  └──────────────┘     │
│  ┌──────────────────┐                                        │
│  │ CallbackRegistry  │                                       │
│  │ (回调函数)         │                                       │
│  └──────────────────┘                                        │
│                                                              │
│  每个 Registry 通过 Provider 机制从 JSON 声明自动构建实例        │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                     Builder (构建层)                          │
│                                                              │
│  递归遍历 AgentNode 树，按 type 分发构建:                       │
│                                                              │
│  buildNode(node)                                             │
│  ├── type == "llm"        → buildLLMAgent()                 │
│  │   ├── ModelRegistry.Get(model.ref)                       │
│  │   ├── ToolRegistry.Get(tool.ref) × N                     │
│  │   ├── CallbackRegistry.GetBeforeAgent(cb.ref) × N        │
│  │   └── agent.NewLLMAgent(cfg)                             │
│  ├── type == "sequential" → buildSequentialAgent()           │
│  │   ├── buildNode(child) × N (递归)                        │
│  │   └── agent.NewSequentialAgent(cfg)                      │
│  ├── type == "parallel"   → buildParallelAgent()             │
│  │   ├── buildNode(child) × N (递归)                        │
│  │   └── agent.NewParallelAgent(cfg)                        │
│  └── type == "loop"       → buildLoopAgent()                 │
│      ├── buildNode(child) × N (递归)                        │
│      └── agent.NewLoopAgent(cfg)                            │
│                                                              │
│  输出: adkagent.Agent (可直接传给 Runner)                     │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 为什么选择树形 Schema

| 对比维度 | 树形 Schema | 节点-边 DAG |
|----------|-------------|-------------|
| 与 adk-go 映射 | 直接映射（嵌套 SubAgents） | 需要 graph→tree 转换 |
| Builder 复杂度 | O(n) 递归遍历 | 需要拓扑排序 + 循环检测 |
| JSON 可读性 | 高，嵌套一目了然 | 低，节点和边分离 |
| 数据流表达 | OutputKey + `{placeholder}` 天然串联 | 需要额外 edge 定义 |
| UI 适配 | UI 内部可用 DAG，序列化为树 | 天然适配 |
| 条件执行 | BeforeAgentCallback 声明式 | 需要 conditional edge |

**结论**：adk-go Agent 天然是嵌套树结构，树形 Schema 直接映射到构建 API，Builder 可递归构建。UI 拖拽层内部可用 DAG 表示，但序列化为树格式给后端。未来可增加 "graph-to-tree normalizer" 转换层。

### 2.3 包结构

```
orchestration/
├── schema.go                         # OrchestrationSchema, AgentNode, 所有 JSON 类型定义
├── schema_test.go                    # Schema JSON marshal/unmarshal round-trip 测试
│
├── parser/
│   ├── parser.go                     # Parse(), Validate(), Normalize() + 校验规则实现
│   └── parser_test.go                # Parser 单元测试 (14 个用例)
│
├── registry/
│   ├── service_registry.go           # ServiceRegistry 接口 + DefaultServiceRegistry
│   ├── model_registry.go             # ModelRegistry 接口 + DefaultModelRegistry
│   ├── tool_registry.go              # ToolRegistry 接口 + DefaultToolRegistry
│   ├── callback_registry.go          # CallbackRegistry 接口 + DefaultCallbackRegistry
│   ├── providers.go                  # 全局 Provider 注册机制 (4 类 Provider)
│   └── providers/
│       ├── openai_model.go           # OpenAI 兼容模型 provider
│       ├── anthropic_model.go        # Anthropic 模型 provider
│       ├── disk_artifact.go          # 磁盘 artifact 服务 provider
│       ├── s3_artifact.go            # S3 artifact 服务 provider
│       ├── conditional_skip.go       # 条件跳过 callback provider
│       ├── filegentool.go            # 文件生成工具 provider
│       └── memory_toolset.go         # Memory 工具集 provider
│
├── builder/
│   ├── builder.go                    # Builder 主体, Build(), buildNode()
│   ├── build_llm.go                  # buildLLMAgent() — LLM Agent 构建
│   ├── build_workflow.go             # buildSequential/Parallel/LoopAgent()
│   └── builder_test.go               # Builder 集成测试 (7 个用例)
│
└── example/
    ├── main.go                       # 端到端示例
    └── pipeline.json                 # parallel-conditional 场景 JSON
```

**包依赖关系**：

```
parser → orchestration (schema types)
registry → orchestration (schema types)
builder → orchestration, registry, agent (工厂函数)
providers → registry, 对应实现包 (model/openai, artifact/diskstorage, etc.)
example → parser, registry, builder, providers (init 注册)
```

---

## 3. 组件设计

### 3.1 Schema — 元数据类型系统

Schema 定义了 JSON 元数据的 Go 类型，是整个系统的数据模型基础。

#### 顶层结构

```go
// OrchestrationSchema 是 JSON 文档的顶层结构
type OrchestrationSchema struct {
    Schema    string          `json:"$schema,omitempty"`  // JSON Schema URI (可选)
    Version   string          `json:"version"`            // 必须为 "1"
    Metadata  SchemaMetadata  `json:"metadata"`           // 元数据
    Registries Registries     `json:"registries"`         // 注册表声明
    Agent     AgentNode       `json:"agent"`              // 根 Agent 节点
}
```

#### AgentNode 联合类型

`AgentNode` 使用 `Type` 字段作为判别式（discriminator），不同 type 下有效字段不同：

```
AgentNode
├── 通用字段: Type, Name, Description, Callbacks, DisableDefaultCallbacks
│
├── type == "llm" 时有效:
│   ├── Model              *ModelReference     // 必填，引用 registries.models
│   ├── Instruction        string              // LLM 指令模板，支持 {state_key} 占位
│   ├── GlobalInstruction  string              // 全局指令
│   ├── OutputKey          string              // 输出存入 session state 的 key
│   ├── Tools              []ToolReference     // 引用 registries.tools
│   ├── IncludeContents    string              // "none" | "default"
│   ├── DisallowTransferToParent  bool
│   └── DisallowTransferToPeers   bool
│
├── type == "sequential" | "parallel" | "loop" 时有效:
│   └── Children           []AgentNode         // 子 Agent 列表，至少 1 个
│
└── type == "loop" 时额外有效:
    └── MaxIterations      uint                // 0 = 无限循环直到终止
```

#### 引用类型（解耦 ref 与实现）

| 类型 | 用途 | 引用目标 |
|------|------|---------|
| `ModelReference` | Agent 引用模型 | `registries.models[].ref` |
| `ToolReference` | Agent 引用工具 | `registries.tools[].ref` |
| `CallbackReference` | Agent 引用回调 | `registries.callbacks[].ref` |

引用机制将 Schema 定义（声明什么）与具体实现（怎么构建）解耦。Schema 只声明 ref，Registry 通过 Provider 机制将 ref 解析为实际对象。

#### Registries 声明

```go
type Registries struct {
    Services  []ServiceRef   `json:"services,omitempty"`   // 基础设施服务
    Models    []ModelRef     `json:"models,omitempty"`     // LLM 模型
    Tools     []ToolRef      `json:"tools,omitempty"`      // 工具
    Callbacks []CallbackRef  `json:"callbacks,omitempty"`  // 回调函数
}
```

每个 Ref 由三个字段组成：

```
┌─────────┬──────────┬───────────────────────────────┐
│  Ref    │ Provider │ Config                        │
│ 唯一引用名│ 提供者名称 │ 提供者特定的配置参数              │
└─────────┴──────────┴───────────────────────────────┘
```

**关键：工具引用服务的模式**。有运行时依赖的工具（如 filegentool 需要 artifact.Service）通过 Config 中的 `serviceRef` 字段引用 ServiceRegistry 中的服务：

```json
{
  "tools": [{
    "ref": "generate_file",
    "provider": "filegentool",
    "config": { "serviceRef": "disk_artifact" }   // ← 引用 services 中的 ref
  }]
}
```

### 3.2 Parser — 解析、校验、规范化

Parser 提供三个核心函数：

```go
// Parse: 一步到位（解析 + 校验 + 规范化）
func Parse(data []byte) (*orchestration.OrchestrationSchema, error)

// Validate: 仅校验，不修改
func Validate(schema *orchestration.OrchestrationSchema) error

// Normalize: 仅规范化（必须在 Validate 之后调用）
func Normalize(schema *orchestration.OrchestrationSchema) error
```

#### 处理流水线

```
JSON bytes
    │
    ▼
┌──────────┐
│ Unmarshal │  JSON → Go struct
└─────┬────┘
      │
      ▼
┌──────────┐
│ Validate │  10 条校验规则，错误聚合
└─────┬────┘
      │
      ▼
┌──────────┐
│ Normalize│  去空白、填默认值
└─────┬────┘
      │
      ▼
  *OrchestrationSchema
```

#### 校验规则（详见 [第 9 节](#9-校验规则参考)）

Parser 的校验器采用**错误聚合**策略：不是遇到第一个错误就返回，而是遍历整个 Schema 收集所有错误，一次性返回。每条错误附带 JSON Path，方便 UI 精确定位问题字段。

#### 规范化规则

| 规则 | 说明 |
|------|------|
| `version` 空值 | 默认为 `"1"` |
| `agent.description` 空值 | 默认为 `agent.name` |
| 字符串字段 | 去除首尾空白字符 |
| 递归应用 | 所有 children 节点递归规范化 |

#### 错误类型

```go
// 单条校验错误
type validationError struct {
    Path    string   // JSON 路径，如 "agent.children[1].children[0].model.ref"
    Message string   // 人类可读的错误描述
}

// 多条校验错误聚合
type multiError []*validationError
```

错误示例输出：
```
agent.children[1].children[0].model.ref: model reference "gpt-4" not found in registries.models;
agent.children[1].children[0].callbacks.beforeAgent[0].ref: callback reference "skip_if_no_risk" not found in registries.callbacks
```

### 3.3 Registry — 注册表与 Provider 机制

#### 四大注册表

| 注册表 | 存储类型 | 接口方法 | Provider 签名 |
|--------|---------|---------|-------------|
| ServiceRegistry | `any` | `Register(ref, svc)` / `Get(ref)` | `func(config map[string]any) (any, error)` |
| ModelRegistry | `model.LLM` | `Register(ref, llm)` / `Get(ref)` | `func(config ModelProviderConfig, svcReg) (model.LLM, error)` |
| ToolRegistry | `tool.Tool` | `Register(ref, t)` / `Get(ref)` | `func(config map[string]any, svcReg) (tool.Tool, error)` |
| CallbackRegistry | `BeforeAgentCallback` / `AfterAgentCallback` | `RegisterBeforeAgent(ref, cb)` / `GetBeforeAgent(ref)` | `func(config map[string]any, svcReg) (Before, After, error)` |

所有注册表都是并发安全的（内部使用 `sync.RWMutex`），且支持两种注册方式：

1. **声明式注册** — `RegisterFromRefs(refs, svcReg)`：从 JSON 中的 Ref 声明，通过 Provider 自动构建实例
2. **手动注册** — `Register(ref, instance)`：直接传入已构建好的实例

#### Provider 注册机制

Provider 采用全局注册模式（类似 `database/sql.Register`），每个 Provider 通过 `init()` 函数自动注册：

```go
// 在 providers/openai_model.go 中
func init() {
    registry.RegisterModelProvider("openai", openaiModelProvider)
}
```

使用时通过 `_ "github.com/UnderTreeTech/adk-go/orchestration/registry/providers"` 空白导入触发 `init()`。

**全局 Provider 注册表**：

| Provider 类型 | 注册函数 | 查找函数 |
|--------------|---------|---------|
| ServiceProvider | `RegisterServiceProvider(name, provider)` | `GetServiceProvider(name)` |
| ModelProvider | `RegisterModelProvider(name, provider)` | `GetModelProvider(name)` |
| ToolProvider | `RegisterToolProvider(name, provider)` | `GetToolProvider(name)` |
| CallbackProvider | `RegisterCallbackProvider(name, provider)` | `GetCallbackProvider(name)` |

#### 内置 Provider 清单

| Provider 名称 | 类型 | 对应包 | 说明 |
|--------------|------|-------|------|
| `"openai"` | Model | `model/openai` | OpenAI 兼容模型（含 DeepSeek 等） |
| `"anthropic"` | Model | `model/anthropic` | Anthropic Claude 模型 |
| `"disk_artifact"` | Service | `artifact/diskstorage` | 磁盘 artifact 存储服务 |
| `"s3_artifact"` | Service | `artifact/s3` | S3 兼容 artifact 存储服务 |
| `"filegentool"` | Tool | `tools/filegentool` | 文件生成工具（需 artifact.Service） |
| `"memory_toolset"` | Tool | `tools/memory` | Memory 工具集（需 MemoryService） |
| `"conditional_skip"` | Callback | 内置 | 条件跳过 BeforeAgentCallback |

#### 各 Provider Config 参数

**openai (Model Provider)**：

| Config 字段 | 类型 | 必填 | 说明 |
|------------|------|------|------|
| `modelName` | string | 是 | 模型标识（如 `deepseek-v4-pro`） |
| `apiKeyEnv` | string | 是 | API Key 环境变量名 |
| `baseUrlEnv` | string | 否 | API Base URL 环境变量名 |
| `extraBody` | map | 否 | 额外请求体参数 |

**anthropic (Model Provider)**：

| Config 字段 | 类型 | 必填 | 说明 |
|------------|------|------|------|
| `modelName` | string | 是 | 模型标识（如 `claude-sonnet-4-5-20250929`） |
| `apiKeyEnv` | string | 是 | API Key 环境变量名 |
| `baseUrlEnv` | string | 否 | API Base URL 环境变量名 |
| `maxOutputTokens` | int | 否 | 最大输出 token 数 |
| `thinkingBudgetTokens` | int | 否 | 思考 token 预算 |

**disk_artifact (Service Provider)**：

| Config 字段 | 类型 | 必填 | 说明 |
|------------|------|------|------|
| `rootDir` | string | 否 | 存储根目录，默认 `/tmp/artifacts` |
| `baseUrl` | string | 否 | 访问 URL 前缀 |

**s3_artifact (Service Provider)**：

| Config 字段 | 类型 | 必填 | 说明 |
|------------|------|------|------|
| `internalEndpoint` | string | 是 | S3 内部端点 |
| `internalSchema` | string | 否 | 内部协议（http/https） |
| `externalEndpoint` | string | 否 | 外部端点 |
| `externalSchema` | string | 否 | 外部协议 |
| `region` | string | 否 | 区域 |
| `accessKey` | string | 否 | 访问密钥 |
| `secretKey` | string | 否 | 密钥 |
| `bucket` | string | 是 | 桶名 |

**filegentool (Tool Provider)**：

| Config 字段 | 类型 | 必填 | 说明 |
|------------|------|------|------|
| `serviceRef` | string | 是 | 引用 ServiceRegistry 中的 artifact 服务 |
| `baseUrl` | string | 否 | Artifact 访问 URL |

**conditional_skip (Callback Provider)**：

| Config 字段 | 类型 | 必填 | 说明 |
|------------|------|------|------|
| `conditionKey` | string | 是 | Session state 中条件判断的 key |
| `outputKey` | string | 是 | 跳过时写入默认值的 state key |
| `defaultValue` | string | 是 | 跳过时写入的默认值 |

### 3.4 Builder — 递归构建引擎

Builder 是将 Schema 转换为 `adkagent.Agent` 树的核心引擎。

#### 核心接口

```go
type BuilderConfig struct {
    ModelRegistry    registry.ModelRegistry
    ToolRegistry     registry.ToolRegistry
    CallbackRegistry registry.CallbackRegistry
}

type Builder struct { cfg BuilderConfig }

func New(cfg BuilderConfig) *Builder
func (b *Builder) Build(schema *OrchestrationSchema) (adkagent.Agent, error)
```

#### 递归构建算法

```
Build(schema)
  │
  └── buildNode(schema.Agent)          // 从根节点开始递归
       │
       ├── type == "llm"
       │   └── buildLLMAgent(node)
       │       ├── 1. ModelRegistry.Get(model.ref)       → model.LLM
       │       ├── 2. ToolRegistry.Get(tool.ref) × N     → []tool.Tool
       │       ├── 3. CallbackRegistry.GetBeforeAgent(cb.ref) × N
       │       ├── 4. CallbackRegistry.GetAfterAgent(cb.ref) × N
       │       ├── 5. 组装 llmagent.Config
       │       └── 6. agent.NewLLMAgent(cfg)              → adkagent.Agent
       │
       ├── type == "sequential"
       │   └── buildSequentialAgent(node)
       │       ├── 1. buildNode(child) × N (递归)         → []adkagent.Agent
       │       ├── 2. resolveCallbacks(node.Callbacks)
       │       ├── 3. 组装 adkagent.Config{SubAgents}
       │       └── 4. agent.NewSequentialAgent(cfg)       → adkagent.Agent
       │
       ├── type == "parallel"
       │   └── buildParallelAgent(node)                   // 同 sequential，但调用 NewParallelAgent
       │
       └── type == "loop"
           └── buildLoopAgent(node)                       // 同 sequential + MaxIterations，调用 NewLoopAgent
```

#### 关键设计决策

**调用现有 `agent.NewXxxAgent` 工厂函数**：Builder 不直接调用上游 `llmagent.New()` / `sequentialagent.New()`，而是通过 adk-go 封装层的 `agent.NewLLMAgent()` 等。这确保了默认日志回调（`LogBeforeModelCallback` 等）自动注入，与手写 Agent 构建行为一致。

**模型共享**：同一个 `model ref`（如 `"deepseek-v4"`）可被多个 LLMAgent 引用。ModelRegistry 返回同一 `model.LLM` 实例（模型是无状态的，可安全共享）。

**工具共享**：同理，同一 tool ref 返回同一 `tool.Tool` 实例。

---

## 4. JSON Schema 完整参考

### 4.1 顶层结构

```json
{
  "$schema": "https://undertreetech.github.io/adk-go/orchestration/v1",
  "version": "1",
  "metadata": {
    "name": "string (必填)",
    "description": "string (可选)",
    "labels": { "string": "string" }
  },
  "registries": {
    "services": [ServiceRef],
    "models": [ModelRef],
    "tools": [ToolRef],
    "callbacks": [CallbackRef]
  },
  "agent": AgentNode
}
```

### 4.2 AgentNode 联合类型

#### LLM Agent (`type: "llm"`)

```json
{
  "type": "llm",
  "name": "string (必填，树内唯一)",
  "description": "string (可选，默认=name)",
  "model": { "ref": "string (必填，引用 registries.models)" },
  "instruction": "string (支持 {state_key} 占位符)",
  "globalInstruction": "string (可选)",
  "outputKey": "string (输出存入 session state 的 key)",
  "tools": [{ "ref": "string" }],
  "includeContents": "none | default",
  "disallowTransferToParent": false,
  "disallowTransferToPeers": false,
  "callbacks": {
    "beforeAgent": [{ "ref": "string", "config": {} }],
    "afterAgent": [{ "ref": "string", "config": {} }]
  },
  "disableDefaultCallbacks": false
}
```

#### Sequential Agent (`type: "sequential"`)

```json
{
  "type": "sequential",
  "name": "string",
  "description": "string",
  "children": [AgentNode],
  "callbacks": { "beforeAgent": [], "afterAgent": [] },
  "disableDefaultCallbacks": false
}
```

#### Parallel Agent (`type: "parallel"`)

```json
{
  "type": "parallel",
  "name": "string",
  "description": "string",
  "children": [AgentNode],
  "callbacks": { "beforeAgent": [], "afterAgent": [] },
  "disableDefaultCallbacks": false
}
```

#### Loop Agent (`type: "loop"`)

```json
{
  "type": "loop",
  "name": "string",
  "description": "string",
  "maxIterations": 0,
  "children": [AgentNode],
  "callbacks": { "beforeAgent": [], "afterAgent": [] },
  "disableDefaultCallbacks": false
}
```

> `maxIterations` 为 0 表示无限循环，直到子 Agent escalate 或调用 `EndInvocation()`。

### 4.3 Registries 声明

```json
{
  "services": [
    {
      "ref": "disk_artifact",
      "provider": "disk_artifact",
      "config": { "rootDir": "/tmp/artifacts" }
    }
  ],
  "models": [
    {
      "ref": "deepseek-v4",
      "provider": "openai",
      "config": {
        "modelName": "deepseek-v4-pro",
        "apiKeyEnv": "OPENAI_API_KEY",
        "baseUrlEnv": "OPENAI_BASE_URL"
      }
    }
  ],
  "tools": [
    {
      "ref": "generate_file",
      "provider": "filegentool",
      "config": { "serviceRef": "disk_artifact" }
    }
  ],
  "callbacks": [
    {
      "ref": "skip_if_no_risk_check",
      "provider": "conditional_skip",
      "config": {
        "conditionKey": "needs_risk_check",
        "outputKey": "risk_result",
        "defaultValue": "{\"status\":\"auto_approved\"}"
      }
    }
  ]
}
```

### 4.4 完整示例

以下 JSON 描述了一个**订单处理流水线**，与 `examples/agents/parallel-conditional/main.go` 中的手写代码等价：

```
Sequential[ClassifyOrder, Parallel[PaymentProcess, RiskCheck(条件)], MergeAndComplete]
```

```json
{
  "$schema": "https://undertreetech.github.io/adk-go/orchestration/v1",
  "version": "1",
  "metadata": {
    "name": "OrderProcessingPipeline",
    "description": "订单处理：分类 → 并行[支付+风控] → 合并"
  },
  "registries": {
    "models": [
      {
        "ref": "deepseek-v4",
        "provider": "openai",
        "config": {
          "modelName": "deepseek-v4-pro",
          "apiKeyEnv": "OPENAI_API_KEY",
          "baseUrlEnv": "OPENAI_BASE_URL"
        }
      }
    ],
    "callbacks": [
      {
        "ref": "skip_if_no_risk_check",
        "provider": "conditional_skip",
        "config": {
          "conditionKey": "needs_risk_check",
          "outputKey": "risk_result",
          "defaultValue": "{\"status\":\"auto_approved\",\"risk_level\":\"none\",\"message\":\"小额订单，自动通过风控\"}"
        }
      }
    ]
  },
  "agent": {
    "type": "sequential",
    "name": "OrderProcessingPipeline",
    "description": "订单处理流水线：分类 → 并行[支付 + 风控] → 合并完成",
    "children": [
      {
        "type": "llm",
        "name": "ClassifyOrder",
        "description": "分析订单，提取订单号、金额，判断是否需要风控审查",
        "model": {"ref": "deepseek-v4"},
        "instruction": "你是一个订单分类 Agent。分析用户输入的订单信息...",
        "outputKey": "order_classification"
      },
      {
        "type": "parallel",
        "name": "ParallelProcessing",
        "description": "并行处理支付和风控审查",
        "children": [
          {
            "type": "llm",
            "name": "PaymentProcess",
            "description": "支付处理（总是执行）",
            "model": {"ref": "deepseek-v4"},
            "instruction": "你是一个支付处理 Agent...\n{order_classification}",
            "outputKey": "payment_result"
          },
          {
            "type": "llm",
            "name": "RiskCheck",
            "description": "风控审查（条件执行：仅大额订单）",
            "model": {"ref": "deepseek-v4"},
            "instruction": "你是一个风控审查 Agent...\n{order_classification}",
            "outputKey": "risk_result",
            "callbacks": {
              "beforeAgent": [{"ref": "skip_if_no_risk_check"}]
            }
          }
        ]
      },
      {
        "type": "llm",
        "name": "MergeAndComplete",
        "description": "汇聚节点，合并两个分支结果",
        "model": {"ref": "deepseek-v4"},
        "instruction": "你是一个订单完成 Agent...\n{order_classification}\n{payment_result}\n{risk_result}",
        "outputKey": "final_report"
      }
    ]
  }
}
```

---

## 5. Provider 扩展指南

### 5.1 新增 Model Provider

```go
// my_provider.go
package providers

import (
    "github.com/UnderTreeTech/adk-go/orchestration"
    "github.com/UnderTreeTech/adk-go/orchestration/registry"
    "google.golang.org/adk/model"
)

func init() {
    registry.RegisterModelProvider("my_model", myModelProvider)
}

func myModelProvider(config orchestration.ModelProviderConfig, svcReg registry.ServiceRegistry) (model.LLM, error) {
    // 从 config 读取参数，构建 model.LLM 实例
    return mymodel.New(config.ModelName, ...), nil
}
```

### 5.2 新增 Service Provider

```go
func init() {
    registry.RegisterServiceProvider("redis_session", redisSessionProvider)
}

func redisSessionProvider(config map[string]any) (any, error) {
    addr, _ := config["addr"].(string)
    return redis.NewClient(&redis.Options{Addr: addr}), nil
}
```

### 5.3 新增 Tool Provider

```go
func init() {
    registry.RegisterToolProvider("my_tool", myToolProvider)
}

func myToolProvider(config map[string]any, svcReg registry.ServiceRegistry) (tool.Tool, error) {
    // 如需依赖服务：
    serviceRef, _ := config["serviceRef"].(string)
    svcAny, err := svcReg.Get(serviceRef)
    if err != nil { return nil, err }
    svc := svcAny.(MyService)

    return mytool.New(svc), nil
}
```

### 5.4 新增 Callback Provider

```go
func init() {
    registry.RegisterCallbackProvider("my_callback", myCallbackProvider)
}

func myCallbackProvider(config map[string]any, svcReg registry.ServiceRegistry) (
    adkagent.BeforeAgentCallback,
    adkagent.AfterAgentCallback,
    error,
) {
    // 返回 before 和/或 after callback
    before := func(ctx adkagent.CallbackContext) (*genai.Content, error) {
        // 回调逻辑
        return nil, nil
    }
    return before, nil, nil
}
```

### 5.5 使用自定义 Provider

```go
import (
    _ "github.com/UnderTreeTech/adk-go/orchestration/registry/providers"  // 内置 providers
    _ "mycompany.com/myproject/my_providers"                               // 自定义 providers
)
```

通过 `init()` 链式注册，所有 Provider 在 `main()` 执行前就绑定到全局注册表。

---

## 6. 工作流程

### 6.1 端到端构建流程

```
用户拖拽编排 Agent (UI)
       │
       ▼
  生成 JSON metadata
       │
       ▼
  ┌──────────────────────────────────────────────────┐
  │ 1. parser.Parse(jsonBytes)                       │
  │    → Unmarshal + Validate + Normalize            │
  │    → *OrchestrationSchema                        │
  └────────────────┬─────────────────────────────────┘
                   │
                   ▼
  ┌──────────────────────────────────────────────────┐
  │ 2. 构建 Registries (按固定顺序)                     │
  │                                                   │
  │    svcReg := NewServiceRegistry()                 │
  │    svcReg.RegisterFromRefs(schema.Registries.Services)
  │                                                   │
  │    modelReg := NewModelRegistry()                 │
  │    modelReg.RegisterFromRefs(schema.Registries.Models, svcReg)
  │                                                   │
  │    toolReg := NewToolRegistry()                   │
  │    toolReg.RegisterFromRefs(schema.Registries.Tools, svcReg)
  │                                                   │
  │    callbackReg := NewCallbackRegistry()           │
  │    callbackReg.RegisterFromRefs(schema.Registries.Callbacks, svcReg)
  └────────────────┬─────────────────────────────────┘
                   │
                   ▼
  ┌──────────────────────────────────────────────────┐
  │ 3. builder.Build(schema)                         │
  │    → 递归遍历 AgentNode 树                        │
  │    → 通过 Registry 解析 ref                      │
  │    → 调用 agent.NewXxxAgent 工厂函数               │
  │    → adkagent.Agent                              │
  └────────────────┬─────────────────────────────────┘
                   │
                   ▼
  ┌──────────────────────────────────────────────────┐
  │ 4. runner.New(runner.Config{Agent: pipeline})    │
  │    → 启动 Agent 执行                              │
  └──────────────────────────────────────────────────┘
```

### 6.2 Registry 构建顺序

构建顺序**必须是 services → models → tools → callbacks**，原因：

1. **Services 最先**：基础设施服务（artifact、session、memory）无外部依赖
2. **Models 第二**：模型构建通常只需环境变量，不需要服务
3. **Tools 第三**：工具可能依赖服务（如 filegentool 需要 artifact.Service）
4. **Callbacks 最后**：回调通常不依赖服务和工具

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────────┐
│  Services   │────→│    Models    │────→│    Tools     │────→│    Callbacks     │
│ (无依赖)     │     │ (可能引用 svc)│     │ (引用 svc)    │     │ (通常无依赖)      │
└─────────────┘     └──────────────┘     └──────────────┘     └──────────────────┘
```

每个 `RegisterFromRefs` 调用内部流程：

```
RegisterFromRefs(refs, svcReg)
  │
  for each ref in refs:
  │
  ├── GetProvider(ref.Provider)          // 从全局 Provider 注册表查找
  │
  ├── provider(ref.Config, svcReg)       // 调用 Provider 构建实例
  │
  └── Register(ref.Ref, instance)        // 存入 Registry
```

### 6.3 Builder 递归构建

以 parallel-conditional 场景为例，Builder 的递归构建过程：

```
Build(schema)
  │
  └── buildNode(OrderProcessingPipeline)        type=sequential
       │
       ├── buildNode(ClassifyOrder)             type=llm
       │   ├── modelReg.Get("deepseek-v4")      → model.LLM
       │   └── agent.NewLLMAgent(cfg)           → LLMAgent "ClassifyOrder"
       │
       ├── buildNode(ParallelProcessing)        type=parallel
       │   │
       │   ├── buildNode(PaymentProcess)        type=llm
       │   │   ├── modelReg.Get("deepseek-v4")  → model.LLM
       │   │   └── agent.NewLLMAgent(cfg)       → LLMAgent "PaymentProcess"
       │   │
       │   └── buildNode(RiskCheck)             type=llm
       │       ├── modelReg.Get("deepseek-v4")  → model.LLM
       │       ├── cbReg.GetBeforeAgent("skip_if_no_risk_check")
       │       │                                → BeforeAgentCallback
       │       └── agent.NewLLMAgent(cfg)       → LLMAgent "RiskCheck"
       │
       │   subAgents = [PaymentProcess, RiskCheck]
       │   └── agent.NewParallelAgent(cfg)      → ParallelAgent "ParallelProcessing"
       │
       ├── buildNode(MergeAndComplete)          type=llm
       │   ├── modelReg.Get("deepseek-v4")      → model.LLM
       │   └── agent.NewLLMAgent(cfg)           → LLMAgent "MergeAndComplete"
       │
       subAgents = [ClassifyOrder, ParallelProcessing, MergeAndComplete]
       └── agent.NewSequentialAgent(cfg)        → SequentialAgent "OrderProcessingPipeline"
```

---

## 7. 使用方式

### 7.1 基本用法

```go
package main

import (
    "os"

    "github.com/UnderTreeTech/adk-go/orchestration/builder"
    "github.com/UnderTreeTech/adk-go/orchestration/parser"
    "github.com/UnderTreeTech/adk-go/orchestration/registry"
    _ "github.com/UnderTreeTech/adk-go/orchestration/registry/providers"  // 注册内置 providers
)

func main() {
    // 1. 加载并解析 JSON schema
    schemaData, _ := os.ReadFile("pipeline.json")
    schema, err := parser.Parse(schemaData)
    if err != nil {
        panic(err)
    }

    // 2. 按顺序构建 Registries
    svcReg := registry.NewServiceRegistry()
    svcReg.RegisterFromRefs(schema.Registries.Services)

    modelReg := registry.NewModelRegistry()
    modelReg.RegisterFromRefs(schema.Registries.Models, svcReg)

    toolReg := registry.NewToolRegistry()
    toolReg.RegisterFromRefs(schema.Registries.Tools, svcReg)

    callbackReg := registry.NewCallbackRegistry()
    callbackReg.RegisterFromRefs(schema.Registries.Callbacks, svcReg)

    // 3. 构建 Agent 树
    b := builder.New(builder.BuilderConfig{
        ModelRegistry:    modelReg,
        ToolRegistry:     toolReg,
        CallbackRegistry: callbackReg,
    })
    pipeline, err := b.Build(schema)
    if err != nil {
        panic(err)
    }

    // 4. 用 ADK Runner 启动
    runner, _ := runner.New(runner.Config{Agent: pipeline})
    // ...
}
```

### 7.2 使用内置 Provider

空白导入 `providers` 包即可自动注册所有内置 provider：

```go
import _ "github.com/UnderTreeTech/adk-go/orchestration/registry/providers"
```

当前内置 provider：

| Provider | 类型 | 自动注册名 |
|----------|------|-----------|
| OpenAI 兼容模型 | Model | `"openai"` |
| Anthropic 模型 | Model | `"anthropic"` |
| 磁盘 Artifact 服务 | Service | `"disk_artifact"` |
| S3 Artifact 服务 | Service | `"s3_artifact"` |
| 文件生成工具 | Tool | `"filegentool"` |
| Memory 工具集 | Tool | `"memory_toolset"` |
| 条件跳过回调 | Callback | `"conditional_skip"` |

### 7.3 注册自定义 Provider

```go
package my_providers

import (
    "github.com/UnderTreeTech/adk-go/orchestration/registry"
    "google.golang.org/adk/model"
)

func init() {
    registry.RegisterModelProvider("gemini", geminiModelProvider)
}

func geminiModelProvider(config orchestration.ModelProviderConfig, svcReg registry.ServiceRegistry) (model.LLM, error) {
    return gemini.New(config.ModelName), nil
}
```

然后在 main.go 中导入：

```go
import _ "mycompany.com/myproject/my_providers"
```

JSON 中即可使用：

```json
{
  "registries": {
    "models": [{
      "ref": "gemini-pro",
      "provider": "gemini",
      "config": { "modelName": "gemini-2.0-pro" }
    }]
  }
}
```

### 7.4 手动注册实例

对于无法通过 JSON 声明式构建的复杂对象，可以直接手动注册：

```go
modelReg := registry.NewModelRegistry()

// 手动注册（不通过 Provider）
llm := openai.New(&openai.Config{
    ModelName: "custom-model",
    APIKey:    "sk-xxx",
    BaseURL:   "https://api.custom.com",
})
modelReg.Register("custom-llm", lll)

// JSON 中仍可通过 ref 引用
// { "model": { "ref": "custom-llm" } }
```

### 7.5 运行示例

```bash
# 设置环境变量
export OPENAI_API_KEY=sk-xxx
export OPENAI_BASE_URL=https://api.deepseek.com

# 运行编排示例
go run ./orchestration/example --run "处理订单 #12345，金额 15000 元"

# 小额订单（RiskCheck 会被条件跳过）
go run ./orchestration/example --run "处理订单 #67890，金额 500 元"
```

---

## 8. 条件执行机制详解

条件执行是多 Agent 编排中的核心难点。本系统采用 **"将条件下沉到分支"** 的模式，与 adk-go 的 `BeforeAgentCallback` 机制完美对齐。

### 问题描述

```
         ┌── BranchA (总是执行) ──┐
START ──┤                         ├── MergeNode ── END
         └── BranchB (条件执行) ──┘
```

当 BranchB 条件不满足时不执行，但 MergeNode 仍期望从两个分支获取数据。如果 BranchB 不运行，MergeNode 会缺少数据。

### 解决方案：conditional_skip

1. `ParallelAgent` **总是启动所有分支**
2. 条件分支通过 `BeforeAgentCallback`（由 `conditional_skip` provider 生成）检查 session state
3. 条件不满足时：
   - callback 将默认值写入 `state[outputKey]`（确保下游有数据）
   - callback 返回非 nil `Content`（触发 ADK 框架跳过 Agent 执行）
4. 条件满足时：callback 返回 nil，Agent 正常执行

### 数据流示意

```
ClassifyOrder
  │ outputKey: "order_classification"
  │ state["needs_risk_check"] = true/false
  │
  ▼
ParallelProcessing
  ├── PaymentProcess (总是执行)
  │     │ outputKey: "payment_result"
  │     │ state["payment_result"] = "{...}"
  │
  └── RiskCheck (条件执行)
        │ BeforeAgentCallback: conditional_skip
        │   → 检查 state["needs_risk_check"]
        │
        ├── needs_risk_check == true
        │     callback 返回 nil → Agent 正常执行
        │     outputKey: "risk_result" = "{...审批通过...}"
        │
        └── needs_risk_check == false
              callback 返回 non-nil Content → Agent 被跳过
              callback 将默认值写入 state["risk_result"] = "{auto_approved}"
              → 节省 LLM 调用成本 + 下游有数据
  │
  ▼
MergeAndComplete
  读取: {order_classification} + {payment_result} + {risk_result}
  → 三个 state key 都有值，正常生成报告
```

### 在 JSON 中声明

```json
{
  "registries": {
    "callbacks": [{
      "ref": "skip_if_no_risk_check",
      "provider": "conditional_skip",
      "config": {
        "conditionKey": "needs_risk_check",
        "outputKey": "risk_result",
        "defaultValue": "{\"status\":\"auto_approved\"}"
      }
    }]
  },
  "agent": {
    "type": "llm",
    "name": "RiskCheck",
    "model": {"ref": "deepseek-v4"},
    "outputKey": "risk_result",
    "callbacks": {
      "beforeAgent": [{"ref": "skip_if_no_risk_check"}]
    }
  }
}
```

### 条件值判断逻辑

`conditional_skip` provider 对 state 中条件值的判断：

| 值类型 | 条件满足（执行 Agent） | 条件不满足（跳过 Agent） |
|--------|----------------------|------------------------|
| `bool` | `true` | `false` |
| `string` | 非 `"false"`/`"no"`/`"0"` | `"false"` / `"no"` / `"0"` |
| 其他类型 | 默认执行 | — |
| key 不存在 | 默认执行 | — |

---

## 9. 校验规则参考

Parser 的 `Validate()` 函数执行以下 10 条校验规则：

| # | 规则 | 错误路径示例 | 错误消息 |
|---|------|-------------|---------|
| 1 | `version` 必须为 `"1"` | `version` | `must be "1", got "99"` |
| 2 | `metadata.name` 非空 | `metadata.name` | `must be non-empty` |
| 3 | `agent.type` 非空且有效 | `agent.type` | `invalid agent type "unknown"` |
| 4 | `agent.name` 非空 | `agent.name` | `must be non-empty` |
| 5 | Agent name 树内唯一 | `agent.children[1].name` | `duplicate agent name "Step" (first declared at agent.children[0])` |
| 6 | Agent name ≠ `"user"` (ADK 保留) | `agent.name` | `agent name "user" is reserved by ADK` |
| 7 | LLM agent `model.ref` 必须在 `registries.models` 中 | `agent.children[0].model.ref` | `model reference "gpt-4" not found in registries.models` |
| 8 | `tools[].ref` 必须在 `registries.tools` 中 | `agent.children[0].tools[0].ref` | `tool reference "xxx" not found in registries.tools` |
| 9 | `callbacks.beforeAgent[].ref` / `afterAgent[].ref` 必须在 `registries.callbacks` 中 | `agent.callbacks.beforeAgent[0].ref` | `callback reference "xxx" not found in registries.callbacks` |
| 10 | sequential/parallel/loop 的 `children` 非空 | `agent.children` | `sequential agent must have at least one child` |
| 11 | registries 内 ref 唯一（services/models/tools/callbacks 各自） | `registries.models[1].ref` | `duplicate ref "m1" (first declared at index 0)` |
| 12 | LLM agent 必须有 model 引用 | `agent.model.ref` | `LLM agent must reference a model` |

---

## 10. 错误处理策略

系统采用三层错误处理：

### Layer 1: Parser 校验错误（用户侧）

面向 UI/用户，使用 JSON Path 精确定位。**错误聚合**，一次性返回所有问题：

```
agent.children[1].children[0].model.ref: model reference "gpt-4" not found in registries.models;
agent.children[1].children[0].callbacks.beforeAgent[0].ref: callback reference "skip_if_no_risk" not found in registries.callbacks
```

### Layer 2: Builder 构建错误（运行时）

面向开发者，包含上下文信息（Agent 名称、引用 ref），使用 `%w` 包装上游错误：

```go
return nil, fmt.Errorf("agent %q: resolve model %q: %w", node.Name, node.Model.Ref, err)
```

### Layer 3: Registry 错误

区分 "未找到" 和 "构建失败"：

| 错误类型 | 示例 |
|---------|------|
| ref 未找到 | `orchestration/registry: model ref "gpt-4" not found` |
| ref 重复 | `orchestration/registry: duplicate model ref "m1"` |
| Provider 未注册 | `orchestration/registry: unknown model provider "my_provider"` |
| Provider 构建失败 | `orchestration/registry: model ref "m1": provider "openai": ...` |

---

## 11. 已知限制与未来扩展

### 当前限制

| # | 限制 | 说明 |
|---|------|------|
| 1 | 不支持 Toolset | 当前 Schema 的 `tools` 字段只支持 `tool.Tool`，`tool.Toolset`（如 memory_toolset）暂不支持声明式注册 |
| 2 | Callback Config 不支持 per-attachment override | `CallbackReference.Config` 字段已定义但 Builder 尚未实现合并逻辑 |
| 3 | BeforeModel/AfterModel/BeforeTool/AfterTool 回调 | Schema 仅支持 `beforeAgent`/`afterAgent`，未覆盖 model/tool 级别回调 |
| 4 | 无 Instruction 模板校验 | `{placeholder}` 引用的 state key 未与上游 Agent 的 `outputKey` 做一致性检查 |
| 5 | 不支持 Graph 模式 | 无法表达 DAG（如共享子节点的重入），只有纯树结构 |

### 未来扩展方向

| # | 方向 | 说明 |
|---|------|------|
| 1 | Schema v2 | 增加 `toolsets` 字段、更多回调类型、per-attachment config override |
| 2 | Graph-to-Tree Normalizer | UI 产生 DAG 后自动转换为树形 Schema |
| 3 | Schema Migration | 版本升级工具（v1 → v2），自动迁移旧 JSON |
| 4 | Instruction Lint | 可选的严格模式：校验 `{placeholder}` 与 `outputKey` 的一致性 |
| 5 | 条件表达式引擎 | 除 `conditional_skip` 外，支持更丰富的条件表达式（如 `{amount} >= 10000`） |
| 6 | Remote Agent | 支持 `"type": "remote"`，跨进程/跨服务编排 |
| 7 | Hot Reload | 监听 JSON 文件变化，动态重建 Agent 树无需重启 |
| 8 | Schema 可视化 | 从 JSON 自动生成 Agent 拓扑图 |
