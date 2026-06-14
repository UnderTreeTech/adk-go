# Orchestration v2：基于 DAG 的多智能体编排系统

> **版本**：v2（graph-based / DAG）
> **状态**：已实现，所有 43 个测试通过
> **与 v1 的关系**：v1（tree-based）完整保留，v2 作为 `orchestration/flow/` 子包共存，零侵入

---

## 目录

1. [设计动机](#1-设计动机)
2. [架构总览](#2-架构总览)
3. [核心组件详解](#3-核心组件详解)
   - 3.1 [FlowSchema — 图结构数据模型](#31-flowschema--图结构数据模型)
   - 3.2 [AgentProvider — 外部 Agent 注入](#32-agentprovider--外部-agent-注入)
   - 3.3 [DAG 拓扑引擎](#33-dag-拓扑引擎)
   - 3.4 [Builder — DAG → Agent 树](#34-builder--dag--agent-树)
   - 3.5 [Parser — 解析与验证](#35-parser--解析与验证)
   - 3.6 [Adapter — v1→v2 转换](#36-adapter--v1v2-转换)
4. [数据流向](#4-数据流向)
5. [分支与归并](#5-分支与归并)
6. [完整示例：政务服务](#6-完整示例政务服务)
7. [v1 vs v2 对比](#7-v1-vs-v2-对比)
8. [包结构与依赖关系](#8-包结构与依赖关系)
9. [API 参考](#9-api-参考)
10. [迁移指南](#10-迁移指南)
11. [验证规则一览](#11-验证规则一览)
12. [测试覆盖](#12-测试覆盖)

---

## 1. 设计动机

### 问题背景

在真实的业务场景中（如政务服务"一次办好"智能预审），多智能体编排呈现 **图/DAG** 拓扑：

```
入口 → 材料接收 → [完整性审核, 真实性核验] → 智能引导提示 → 输出
                    ↑_________________________↑
                    （完整性审核结果也流入真实性核验）
```

在现有 v1 tree-based 编排中存在两个核心问题：

1. **嵌套树难以表达一般 DAG**：需要用 `Sequential[Parallel[...]]` 嵌套来模拟图结构，表达力受限且不直观
2. **编排与执行耦合**：v1 schema 中 `registries` 内嵌了 model/tools/callbacks 的声明，builder 负责从 registries 构建一切——编排同时承担了 **构建** 和 **编排** 两个职责

### 核心设计原则

> **编排只管 Agent 之间的关系**（数据流向、分支、归并），**不关心 Agent 的具体执行**。

具体含义：
- `skill`、`mcp`、`model_info`、知识库等执行配置由**上层业务**直接传入
- JSON schema 只声明 Agent 的身份（id/name）和输入输出元数据（outputKey/inputKeys）
- Agent 之间的关系通过 **edges** 显式定义
- 具体每个 Agent 的执行由调用方通过 `AgentProvider` 注入

---

## 2. 架构总览

```
┌─────────────────────────────────────────────────────────────────────┐
│                        调用方（业务层）                              │
│                                                                     │
│  1. 创建完整配置的 Agent（model + tools + MCP + skill + knowledge） │
│  2. 注册到 AgentProvider                                            │
│  3. 调用 executor.Build() 构建编排                                  │
└──────────┬──────────────────────────────────────┬──────────────────┘
           │                                      │
           ▼                                      ▼
┌─────────────────────┐              ┌────────────────────────────┐
│  FlowSchema (JSON)   │              │  AgentProvider            │
│  blocks + edges      │              │  blockID → Agent 实例     │
│  （只含关系，不含执行） │              │  （只含执行，不含关系）     │
└──────────┬──────────┘              └──────────┬─────────────────┘
           │                                      │
           ▼                                      ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     executor.Build()                                │
│                                                                     │
│  1. Parser.Parse(json) → FlowSchema                                │
│  2. NewDAG(blocks, edges) → DAG 拓扑                               │
│  3. dag.Levels() → 层级分组                                         │
│  4. Provider.Get(blockID) → Agent 实例                              │
│  5. 同层 1 个 → 直接加入 pipeline                                   │
│     同层 N 个 → ParallelAgent 加入 pipeline                          │
│  6. SequentialAgent(pipeline) → 根 Agent                            │
└─────────────────────────────────────────────────────────────────────┘
           │
           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     adk-go Runner                                   │
│                                                                     │
│  runner.New(Config{Agent: root})                                    │
│  按层级顺序执行，同层并行，自动处理 session state 数据流转           │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 3. 核心组件详解

### 3.1 FlowSchema — 图结构数据模型

**文件**：`orchestration/flow/schema.go`

v2 schema 用 **扁平的 blocks + edges** 定义 DAG，取代 v1 的嵌套树结构。

#### FlowSchema

```go
type FlowSchema struct {
    Schema   string       `json:"$schema,omitempty"`  // JSON Schema URI（可选）
    Version  string       `json:"version"`            // 必须为 "2"
    Metadata FlowMetadata `json:"metadata"`           // 名称、描述、标签
    Blocks   []Block      `json:"blocks"`             // 扁平的 Agent 节点列表
    Edges    []Edge       `json:"edges"`              // 数据流向边
}
```

#### Block — Agent 节点

每个 Block **只声明身份和输入输出元数据**，不含 model/tools/MCP 等执行细节：

```go
type Block struct {
    ID          string    `json:"id"`                    // 唯一标识，用于 Provider 查找和 Edge 引用
    Name        string    `json:"name"`                  // Agent 名称（不能为 "user"）
    Type        BlockType `json:"type"`                  // start / agent / end
    Description string    `json:"description,omitempty"`
    OutputKey   string    `json:"outputKey,omitempty"`   // 输出写入 session state 的 key
    InputKeys   []string  `json:"inputKeys,omitempty"`   // 依赖的上游 outputKey（验证用）
}
```

**BlockType 三种类型**：

| 类型 | 含义 | 是否需要 AgentProvider 注册 |
|------|------|---------------------------|
| `start` | 入口节点，接收用户输入 | 否（passthrough） |
| `agent` | 业务 Agent 节点 | **是** |
| `end` | 终止节点，输出最终结果 | 否（passthrough） |

#### Edge — 数据流向

```go
type Edge struct {
    SourceID string `json:"sourceId"`  // 源 Block ID
    TargetID string `json:"targetId"`  // 目标 Block ID
}
```

Edge 声明：**源 Block 的输出** 流向 **目标 Block 的输入**。

#### JSON 示例

```json
{
  "version": "2",
  "metadata": {"name": "GovServicePreReview"},
  "blocks": [
    {"id": "entry",     "name": "入口",     "type": "start",  "outputKey": "user_input"},
    {"id": "material",  "name": "材料接收", "type": "agent",  "outputKey": "material_result"},
    {"id": "review",    "name": "完整性审核","type": "agent",  "outputKey": "completeness_result"},
    {"id": "output",    "name": "输出",     "type": "end"}
  ],
  "edges": [
    {"sourceId": "entry",    "targetId": "material"},
    {"sourceId": "material", "targetId": "review"},
    {"sourceId": "review",   "targetId": "output"}
  ]
}
```

### 3.2 AgentProvider — 外部 Agent 注入

**文件**：`orchestration/flow/provider/provider.go`

AgentProvider 是 v2 的核心接口，实现 **编排与执行的分离**。

#### 接口定义

```go
type AgentProvider interface {
    Get(blockID string) (adkagent.Agent, error)  // 获取预构建的 Agent
    BlockIDs() []string                           // 所有已注册的 block ID
}
```

#### MapAgentProvider — 基于 map 的实现

```go
p := provider.NewMapAgentProvider()
p.Register("material_receive", materialAgent)  // 注册预构建的 Agent
p.Register("completeness_review", reviewAgent)

agent, err := p.Get("material_receive")  // 查找
```

特性：
- 并发安全（`sync.RWMutex`）
- 重复注册返回 error
- `BlockIDs()` 返回排序后的 ID 列表

#### AgentProviderFunc — 函数式适配器

```go
fn := provider.AgentProviderFunc(func(blockID string) (adkagent.Agent, error) {
    // 按需动态构建 Agent
    return createAgent(blockID), nil
})
```

适用于动态 Agent 解析或懒加载场景。

#### 调用方用法

```go
// 业务层创建完整配置的 Agent
materialAgent, _ := agent.NewLLMAgent(agent.Config{
    LLMAgentConfig: llmagent.Config{
        Name:        "材料接收智能体",
        Model:       openai.New(...),           // ← model 由业务层配置
        Instruction: "接收材料...\n{user_input}",
        OutputKey:   "material_result",
        Tools:       []tool.Tool{mcpTool1, skillTool1},  // ← tools/MCP/skill 由业务层配置
    },
})

// 注册到 Provider
p := provider.NewMapAgentProvider()
p.Register("material_receive", materialAgent)
```

### 3.3 DAG 拓扑引擎

**文件**：`orchestration/flow/executor/dag.go`

DAG 引擎负责从 blocks + edges 构建有向无环图，计算拓扑层级，检测环，提供上下游查询。

#### DAG 结构

```go
type DAG struct {
    blocks    []flow.Block
    edges     []flow.Edge
    blockMap  map[string]*flow.Block  // ID → Block
    adjacency map[string][]string     // source → targets（正向邻接表）
    reverse   map[string][]string     // target → sources（逆向邻接表）
    inDegree  map[string]int          // blockID → 入度
    levels    map[string]int          // blockID → 拓扑层级（计算得出）
}
```

#### 层级计算算法（Kahn's Algorithm）

```
1. 初始化：inDegree[blockID] = 入边数
2. 入度为 0 的节点 → Level 0
3. 对每个 Level L：
   - 处理该层所有节点
   - 下游节点入度 -1
   - 入度归 0 的节点 → Level L+1
4. 若不是所有节点都被分配层级 → 存在环 → 报错
```

**关键性质**：
- **同一层内的 Block 没有相互依赖**，可以并行执行
- **不同层之间有严格的依赖顺序**，必须按层级从低到高执行
- 层级划分是**最优的**：每个 Block 被分配到尽可能早的层级

#### 核心 API

| 方法 | 说明 |
|------|------|
| `NewDAG(blocks, edges)` | 构建 DAG，含环检测和引用验证 |
| `Levels()` | 返回按层级分组的 Block 列表 `[][]Block` |
| `TopologicalSort()` | 返回拓扑排序的 Block ID 列表 |
| `InDegree(blockID)` | 返回入度（-1 表示不存在） |
| `Upstream(blockID)` | 返回直接上游 Block ID 列表 |
| `Downstream(blockID)` | 返回直接下游 Block ID 列表 |
| `Block(blockID)` | 按 ID 查找 Block |
| `LevelOf(blockID)` | 返回拓扑层级（-1 表示不存在） |

#### 层级计算示例

**Diamond 模式**（Classify → {Payment, Risk} → Merge）：

```
Level 0: [ClassifyOrder]      ← 入度为 0
Level 1: [Payment, RiskCheck] ← Classify 完成后入度归 0，可并行
Level 2: [MergeAndComplete]   ← Payment + Risk 都完成后入度归 0
```

**政务服务模式**（entry → material → completeness → auth → guide → output）：

```
Level 0: [entry]              ← start 节点
Level 1: [material]           ← 依赖 entry
Level 2: [completeness]       ← 依赖 material
Level 3: [auth]               ← 依赖 material + completeness（2 个入度，需两者都完成）
Level 4: [guide]              ← 依赖 completeness + auth
Level 5: [output]             ← end 节点
```

### 3.4 Builder — DAG → Agent 树

**文件**：`orchestration/flow/executor/agent.go`

Builder 将 DAG 层级转换为 adk-go 的 `SequentialAgent` + `ParallelAgent` 树。

#### Build 算法

```
Build(schema, cfg)
│
├── 1. dag = NewDAG(schema.Blocks, schema.Edges)
│      构建并验证 DAG
│
├── 2. levels = dag.Levels()
│      按拓扑层级分组
│
├── 3. 对每个 Level L：
│      ├── 解析 Agent：provider.Get(block.ID) 或创建 passthrough
│      ├── 若该层 1 个 Agent：直接加入 pipeline
│      └── 若该层 N 个 Agent：创建 ParallelAgent，加入 pipeline
│
└── 4. 返回 SequentialAgent(pipeline)
       （若仅 1 个 Agent，直接返回，不包装）
```

#### 转换示例

**Diamond 模式**：

```
层级：[ClassifyOrder], [Payment, RiskCheck], [MergeAndComplete]

结果：
SequentialAgent "OrderPipeline" [
    ClassifyOrder,                           ← Level 0，1 个，直接加入
    ParallelAgent "Level1_Parallel" [        ← Level 1，2 个，创建并行
        Payment,
        RiskCheck
    ],
    MergeAndComplete                         ← Level 2，1 个，直接加入
]
```

**政务服务模式**：

```
层级：[entry], [material], [completeness], [auth], [guide], [output]

结果：
SequentialAgent "GovServicePreReview" [
    entry,               ← passthrough (start)
    material,            ← 从 Provider 获取
    completeness,        ← 从 Provider 获取
    auth,                ← 从 Provider 获取
    guide,               ← 从 Provider 获取
    output               ← passthrough (end)
]
```

#### BuildConfig

```go
type BuildConfig struct {
    Name     string               // 根 Agent 名称（空则取 schema.Metadata.Name）
    Provider provider.AgentProvider // 必填，提供预构建的 Agent 实例
}
```

#### Passthrough Agent

`start` 和 `end` 类型的 Block 不需要从 AgentProvider 获取 Agent，Builder 会创建一个
最小化的 passthrough Agent（使用 `adkagent.New`），它仅作为 DAG 中的标记节点，
负责将用户输入引入（start）或标记终止（end）。

### 3.5 Parser — 解析与验证

**文件**：`orchestration/flow/parser/parser.go`

#### API

```go
func Parse(data []byte) (*flow.FlowSchema, error)     // 解析 JSON → Validate → Normalize
func Validate(schema *flow.FlowSchema) error            // 结构验证
func Normalize(schema *flow.FlowSchema) error           // 默认值 + 规范化
```

#### Parse 流程

```
JSON bytes → json.Unmarshal → FlowSchema → Validate → Normalize → *FlowSchema
```

#### Normalize 规则

- `Version` 为空时默认为 `"2"`
- `ID`、`Name`、`Description`、`OutputKey` 去除首尾空白
- `Description` 为空时默认为 `Name`

#### 错误聚合

使用 `multiError` 聚合所有验证错误（而非遇到第一个错误就返回），每条错误带 JSON Path：

```
blocks[2].name: must be non-empty; edges[1].targetId: target block "nonexistent" not found in blocks
```

### 3.6 Adapter — v1→v2 转换

**文件**：`orchestration/flow/adapter/tree_to_flow.go`

将 v1 tree-based `OrchestrationSchema` 转换为 v2 graph-based `FlowSchema`。

#### 转换算法

递归遍历 v1 树，将叶节点 LLM Agent 提取为 Block，根据树结构推导 Edge：

| v1 AgentType | 转换策略 | Edge 推导 |
|-------------|---------|----------|
| `llm` | 提取为 `agent` Block | 所有 `prevBlockIDs` → 当前 Block |
| `sequential` | 不生成 Block | 子节点按顺序：每个子节点的输出成为下一个子节点的 `prevBlockIDs` |
| `parallel` | 不生成 Block | 所有子节点看到相同的 `prevBlockIDs`；所有子节点的输出归并到下游 |
| `loop` | 不生成 Block | 按 sequential 处理 |

#### 转换示例

v1 树 `Sequential[ClassifyOrder, Parallel[Payment, RiskCheck], MergeAndComplete]`：

```
convertTree(root, prevBlockIDs=[])
│
├── Sequential:
│   ├── convertTree(ClassifyOrder, prev=[])
│   │     Block: ClassifyOrder, 无入边 → 返回 ["ClassifyOrder"]
│   │
│   ├── prev = ["ClassifyOrder"]
│   │   convertTree(Parallel[Payment, RiskCheck], prev=["ClassifyOrder"])
│   │     ├── convertTree(Payment, prev=["ClassifyOrder"])
│   │     │     Edge: ClassifyOrder→Payment → 返回 ["Payment"]
│   │     └── convertTree(RiskCheck, prev=["ClassifyOrder"])
│   │           Edge: ClassifyOrder→RiskCheck → 返回 ["RiskCheck"]
│   │     返回 ["Payment", "RiskCheck"]
│   │
│   ├── prev = ["Payment", "RiskCheck"]
│   │   convertTree(MergeAndComplete, prev=["Payment", "RiskCheck"])
│   │     Edge: Payment→Merge, RiskCheck→Merge → 返回 ["MergeAndComplete"]
│   │
│   └── 返回 ["MergeAndComplete"]
```

结果：4 个 Block，4 条 Edge，形成 Diamond DAG。

---

## 4. 数据流向

### 4.1 机制：Session State + OutputKey

v2 沿用 adk-go 的 session state 机制进行数据传递：

1. 每个 Agent 执行完毕后，其输出通过 `OutputKey` 写入 session state
2. 下游 Agent 的 `Instruction` 中通过 `{outputKey}` 模板占位符引用上游输出
3. ADK Runner 在调用 LLM 前自动进行模板替换

### 4.2 Edge 与 OutputKey 的关系

- **Edge 声明的是 Block 之间的连接关系**（用于 DAG 拓扑计算和验证）
- **OutputKey 声明的是数据写入 session state 的 key**（用于运行时数据传递）
- 两者协同工作：Edge 决定执行顺序，OutputKey 决定数据内容

### 4.3 数据流示例（政务服务）

```
执行时序                     Session State
──────────                   ─────────────
1. entry 执行               user_input = "市民上传的材料..."
2. material 执行            material_result = "{结构化材料数据}"
3. completeness 执行        completeness_result = "{完整性审核报告}"
4. auth 执行                auth_result = "{真实性核验报告}"
5. guide 执行               guide_result = "{智能引导提示内容}"
6. output 执行              (最终输出)
```

每个 Agent 的 Instruction 可以引用上游 OutputKey：

```go
// 完整性审核 Agent 的 Instruction 引用上游 material_result
Instruction: "对照材料清单核验完整性...\n{material_result}"

// 真实性核验 Agent 的 Instruction 引用两个上游结果
Instruction: "核验真实性...\n{material_result}\n{completeness_result}"

// 智能引导 Agent 的 Instruction 引用两个上游结果
Instruction: "生成引导提示...\n{completeness_result}\n{auth_result}"
```

### 4.4 InputKeys 的验证作用

Block 的 `inputKeys` 字段是**声明式**的，用于 Parser 验证数据流完整性：

```json
{
  "id": "auth_verify",
  "name": "真实性核验智能体",
  "outputKey": "auth_result",
  "inputKeys": ["material_result", "completeness_result"]
}
```

声明此 Block 依赖 `material_result` 和 `completeness_result` 两个上游输出。
Parser 可以据此验证：这些 key 是否确实由上游 Block 的 `outputKey` 提供。

> 注意：`inputKeys` 是可选的。如果省略，Parser 跳过该 Block 的输入验证。
> 运行时数据传递完全依赖 Edge + OutputKey，不依赖 `inputKeys`。

---

## 5. 分支与归并

### 5.1 Fork（分支/扇出）

一个 Block 的输出流向多个下游 Block：

```
material_receive ─┬→ completeness_review
                  └→ auth_verify
```

在 DAG 中表示为两条 Edge：
```json
{"sourceId": "material_receive", "targetId": "completeness_review"},
{"sourceId": "material_receive", "targetId": "auth_verify"}
```

DAG 层级计算会自动识别：如果 `completeness_review` 和 `auth_verify` **只依赖**
`material_receive`（无其他共同依赖），则它们处于**同一层级**，会被放入同一个
`ParallelAgent` 并行执行。

### 5.2 Join（归并/扇入）

多个 Block 的输出汇聚到一个下游 Block：

```
completeness_review ─┬→ smart_guide
auth_verify         ─┘
```

在 DAG 中表示为两条 Edge：
```json
{"sourceId": "completeness_review", "targetId": "smart_guide"},
{"sourceId": "auth_verify",        "targetId": "smart_guide"}
```

DAG 层级计算保证：`smart_guide` 的层级严格大于 `completeness_review` 和 `auth_verify`，
只有**所有上游 Block 都执行完毕**后，`smart_guide` 才会开始执行。

### 5.3 复合模式（Fork + Join）

政务服务中的真实性核验同时依赖材料接收和完整性审核：

```
material_receive ─┬→ completeness_review ─┬→ auth_verify
                  └───────────────────────┘
```

DAG 层级计算：
- `material_receive` 的入度 = 0 → Level 0
- `completeness_review` 的入度 = 1（来自 material）→ Level 1
- `auth_verify` 的入度 = 2（来自 material + completeness）→ Level 2（需两者都完成）

结果：`auth_verify` 严格排在 `completeness_review` 之后，不存在并行。

---

## 6. 完整示例：政务服务

### 6.1 业务场景

政务服务"一次办好"智能预审，5 个智能体协作审核市民办事材料：

```
入口 → 材料接收 → 完整性审核 → 真实性核验 → 智能引导提示 → 输出
         │              │              │               │
         └──────────────┘              │               │
         （material_result 也给 auth）  │               │
                        ┌──────────────┘               │
                        │  ┌───────────────────────────┘
                        │  │
                        completeness_result + auth_result → guide
```

### 6.2 Flow Schema（gov_service.json）

```json
{
  "version": "2",
  "metadata": {
    "name": "GovServicePreReview",
    "description": "政务服务 '一次办好' 智能预审"
  },
  "blocks": [
    {"id": "entry",             "name": "政务服务入口",     "type": "start",  "outputKey": "user_input"},
    {"id": "material_receive",  "name": "材料接收智能体",   "type": "agent",  "outputKey": "material_result"},
    {"id": "completeness_review","name": "完整性审核智能体", "type": "agent",  "outputKey": "completeness_result"},
    {"id": "auth_verify",       "name": "真实性核验智能体", "type": "agent",  "outputKey": "auth_result"},
    {"id": "smart_guide",       "name": "智能引导提示智能体","type": "agent",  "outputKey": "guide_result"},
    {"id": "output",            "name": "最终输出",         "type": "end"}
  ],
  "edges": [
    {"sourceId": "entry",              "targetId": "material_receive"},
    {"sourceId": "material_receive",   "targetId": "completeness_review"},
    {"sourceId": "material_receive",   "targetId": "auth_verify"},
    {"sourceId": "completeness_review","targetId": "auth_verify"},
    {"sourceId": "completeness_review","targetId": "smart_guide"},
    {"sourceId": "auth_verify",        "targetId": "smart_guide"},
    {"sourceId": "smart_guide",        "targetId": "output"}
  ]
}
```

### 6.3 调用方代码

```go
// 1. 解析 Flow Schema
schema, _ := flowparser.Parse(os.ReadFile("gov_service.json"))

// 2. 创建预配置的 Agent（业务层负责 model/tools/MCP/skill/knowledge）
materialAgent, _ := agent.NewLLMAgent(agent.Config{
    LLMAgentConfig: llmagent.Config{
        Name:        "材料接收智能体",
        Model:       openai.New(openai.Config{Model: "glm-4.7", ...}),
        Instruction: "接收并标准化处理市民上传的办事材料...\n{user_input}",
        OutputKey:   "material_result",
        Tools:       []tool.Tool{documentParserMCP, ocrSkill},
    },
})

completenessAgent, _ := agent.NewLLMAgent(agent.Config{
    LLMAgentConfig: llmagent.Config{
        Name:        "完整性审核智能体",
        Model:       openai.New(openai.Config{Model: "glm-4.7", ...}),
        Instruction: "对照材料清单核验完整性...\n{material_result}",
        OutputKey:   "completeness_result",
    },
})

// ... 创建 authAgent, guideAgent 等

// 3. 注册到 Provider
p := provider.NewMapAgentProvider()
p.Register("material_receive",  materialAgent)
p.Register("completeness_review", completenessAgent)
p.Register("auth_verify",       authAgent)
p.Register("smart_guide",      guideAgent)

// 4. 构建编排
pipeline, _ := executor.Build(schema, executor.BuildConfig{
    Name:     "GovServicePreReview",
    Provider: p,
})

// 5. 运行
runnr, _ := runner.New(runner.Config{Agent: pipeline})
```

### 6.4 生成的 Agent 树

```
SequentialAgent "GovServicePreReview" [
    entry,               ← passthrough (start)
    material_receive,     ← LLM Agent（含 model + tools + MCP）
    completeness_review,  ← LLM Agent
    auth_verify,          ← LLM Agent
    smart_guide,          ← LLM Agent
    output                ← passthrough (end)
]
```

---

## 7. v1 vs v2 对比

### 7.1 架构对比

| 维度 | v1 (tree-based) | v2 (graph-based / DAG) |
|------|-----------------|----------------------|
| **拓扑结构** | 嵌套树（Sequential/Parallel/Loop） | 扁平图（blocks + edges） |
| **表达力** | 受限于树嵌套，diamond 需要嵌套模拟 | 任意 DAG，原生支持 fork/join |
| **数据流声明** | 隐式（通过嵌套位置 + OutputKey） | **显式**（通过 edges） |
| **Agent 创建** | Builder 从 registries 构建 | **调用方构建，通过 Provider 注册** |
| **配置位置** | model/tools/MCP 在 schema 的 registries 中 | **不在 schema 中，上层业务负责** |
| **编排角色** | 构建 + 编排 | **只编排** |
| **验证能力** | 树结构隐式保证无环 | **DAG 环检测 + 引用验证 + outputKey 唯一性** |

### 7.2 Schema 对比

**v1**：
```json
{
  "version": "1",
  "registries": {
    "models": [{"ref": "deepseek-v4", "provider": "openai", "config": {...}}],
    "tools": [{"ref": "generate_file", "provider": "filegentool", "config": {...}}]
  },
  "agent": {
    "type": "sequential",
    "children": [
      {"type": "llm", "name": "Step1", "model": {"ref": "deepseek-v4"}, "tools": [{"ref": "generate_file"}]},
      {"type": "parallel", "children": [...]}
    ]
  }
}
```

**v2**：
```json
{
  "version": "2",
  "blocks": [
    {"id": "step1", "name": "Step1", "type": "agent", "outputKey": "s1"},
    {"id": "step2", "name": "Step2", "type": "agent", "outputKey": "s2"}
  ],
  "edges": [
    {"sourceId": "step1", "targetId": "step2"}
  ]
}
```

### 7.3 调用方式对比

**v1**：Builder 负责一切
```go
schema, _ := parser.Parse(jsonBytes)
modelReg.RegisterFromRefs(schema.Registries.Models, svcReg)   // Builder 构建 model
toolReg.RegisterFromRefs(schema.Registries.Tools, svcReg)     // Builder 构建 tool
b := builder.New(builder.Config{ModelRegistry: modelReg, ...})
pipeline, _ := b.Build(schema)                                 // Builder 构建 Agent 树
```

**v2**：调用方构建 Agent，编排只做排列
```go
schema, _ := flowparser.Parse(jsonBytes)
p := provider.NewMapAgentProvider()
p.Register("step1", myStep1Agent)  // 调用方创建并注册 Agent
p.Register("step2", myStep2Agent)
pipeline, _ := executor.Build(schema, executor.BuildConfig{Provider: p})
```

---

## 8. 包结构与依赖关系

### 8.1 文件清单

```
orchestration/flow/
├── schema.go                     # FlowSchema, Block, Edge, BlockType
├── schema_test.go                # JSON round-trip 测试
├── provider/
│   ├── provider.go               # AgentProvider 接口, MapAgentProvider, AgentProviderFunc
│   └── provider_test.go          # Provider 测试
├── executor/
│   ├── dag.go                    # DAG 拓扑引擎（层级计算、环检测、拓扑排序）
│   ├── dag_test.go               # DAG 测试（线性链、diamond、政务服务、环等）
│   ├── agent.go                  # Build()：DAG → SequentialAgent/ParallelAgent 树
│   └── agent_test.go             # Builder 测试
├── parser/
│   ├── parser.go                 # Parse/Validate/Normalize
│   └── parser_test.go            # 15 个验证测试
├── adapter/
│   ├── tree_to_flow.go           # Convert()：v1 → v2 转换
│   └── tree_to_flow_test.go      # 转换验证测试
└── example/
    ├── main.go                   # 端到端示例
    └── gov_service.json          # 政务服务示例 JSON
```

### 8.2 依赖关系图

```
flow/parser       ──→ flow (schema types)
flow/provider     ──→ adk/agent
flow/executor     ──→ flow, flow/provider, agent (Sequential/Parallel factory)
flow/adapter      ──→ orchestration (v1 schema), flow (v2 schema)
flow/example      ──→ flow/parser, flow/provider, flow/executor, agent
```

### 8.3 与 v1 的关系

**v2 完全独立于 v1**：
- `flow/` 子包不引用 `orchestration/` 根包的任何代码
- 唯一的例外是 `flow/adapter/`，它引用 `orchestration.OrchestrationSchema` 来做 v1→v2 转换
- 所有 v1 代码（schema.go, builder/, parser/, registry/）**未做任何修改**

---

## 9. API 参考

### 9.1 flow 包

```go
// 常量
const FlowSchemaVersion = "2"
const FlowSchemaURI = "https://undertreetech.github.io/adk-go/orchestration/v2"

// 类型
type FlowSchema struct { Schema, Version string; Metadata FlowMetadata; Blocks []Block; Edges []Edge }
type FlowMetadata struct { Name, Description string; Labels map[string]string }
type BlockType string  // "start" | "agent" | "end"
type Block struct { ID, Name string; Type BlockType; Description, OutputKey string; InputKeys []string }
type Edge struct { SourceID, TargetID string }

// 函数
func ValidBlockTypes() []BlockType
```

### 9.2 provider 包

```go
type AgentProvider interface {
    Get(blockID string) (adkagent.Agent, error)
    BlockIDs() []string
}

type MapAgentProvider struct { ... }
func NewMapAgentProvider() *MapAgentProvider
func (p *MapAgentProvider) Register(blockID string, agent adkagent.Agent) error
func (p *MapAgentProvider) Get(blockID string) (adkagent.Agent, error)
func (p *MapAgentProvider) BlockIDs() []string

type AgentProviderFunc func(blockID string) (adkagent.Agent, error)
```

### 9.3 executor 包

```go
// DAG 拓扑引擎
type DAG struct { ... }
func NewDAG(blocks []flow.Block, edges []flow.Edge) (*DAG, error)
func (d *DAG) Levels() [][]flow.Block
func (d *DAG) TopologicalSort() []string
func (d *DAG) InDegree(blockID string) int
func (d *DAG) Upstream(blockID string) []string
func (d *DAG) Downstream(blockID string) []string
func (d *DAG) Block(blockID string) *flow.Block
func (d *DAG) LevelOf(blockID string) int

// Builder
type BuildConfig struct { Name string; Provider provider.AgentProvider }
func Build(schema *flow.FlowSchema, cfg BuildConfig) (adkagent.Agent, error)
```

### 9.4 parser 包

```go
func Parse(data []byte) (*flow.FlowSchema, error)
func Validate(schema *flow.FlowSchema) error
func Normalize(schema *flow.FlowSchema) error
```

### 9.5 adapter 包

```go
func Convert(tree *orchestration.OrchestrationSchema) (*flow.FlowSchema, error)
```

---

## 10. 迁移指南

### 10.1 迁移步骤

| 阶段 | 行动 | 影响 |
|------|------|------|
| 1 | 添加 `flow/` 子包 | 零影响，现有代码不改动 |
| 2 | 新工作流使用 `flow/` | v1 系统不受影响 |
| 3 | 用 `adapter.Convert()` 转换现有 v1 schema | 逐个工作流迁移 |
| 4 | （可选）废弃 v1 | 所有工作流迁移完成后 |

### 10.2 v1 → v2 转换步骤

```go
// 1. 解析 v1 schema
v1Schema, _ := parser.Parse(v1JSON)

// 2. 转换为 v2 schema
v2Schema, _ := adapter.Convert(v1Schema)

// 3. 手动创建 Agent 并注册到 Provider
//    需要为 v1 schema 中的每个 LLM agent 创建对应的 Agent 实例
p := provider.NewMapAgentProvider()
for _, model := range v1Schema.Registries.Models {
    llm, _ := modelReg.Get(model.Ref)
    // 创建 Agent 并注册...
}

// 4. 构建并运行
pipeline, _ := executor.Build(v2Schema, executor.BuildConfig{Provider: p})
```

### 10.3 注意事项

- v1 的 `registries`（models/tools/callbacks/services）在 v2 中不再存在
- v2 的 Agent 创建完全由调用方负责
- `adapter.Convert()` 只转换拓扑结构，不转换执行配置
- LoopAgent 在 v2 中暂无原生支持（Loop 语义被转为 Sequential 边）

---

## 11. 验证规则一览

Parser 的 `Validate()` 执行以下验证，所有错误聚合返回：

| # | 规则 | JSON Path | 级别 |
|---|------|-----------|------|
| 1 | `version` 必须为 `"2"` | `version` | 错误 |
| 2 | `metadata.name` 非空 | `metadata.name` | 错误 |
| 3 | `blocks` 非空 | `blocks` | 错误 |
| 4 | `block.id` 非空 | `blocks[i].id` | 错误 |
| 5 | `block.id` 唯一 | `blocks[i].id` | 错误 |
| 6 | `block.name` 非空 | `blocks[i].name` | 错误 |
| 7 | `block.name` 不为 `"user"` | `blocks[i].name` | 错误 |
| 8 | `block.type` 有效 | `blocks[i].type` | 错误 |
| 9 | `edge.sourceId` 非空 | `edges[i].sourceId` | 错误 |
| 10 | `edge.targetId` 非空 | `edges[i].targetId` | 错误 |
| 11 | Edge 引用存在的 Block | `edges[i].sourceId/targetId` | 错误 |
| 12 | 无自环 | `edges[i]` | 错误 |
| 13 | 无重复 Edge | `edges[i]` | 错误 |
| 14 | 图无环（DAG） | `edges` | 错误 |
| 15 | `outputKey` 在 Block 间唯一 | `blocks` | 错误 |

---

## 12. 测试覆盖

### 12.1 测试统计

| 包 | 测试数 | 覆盖场景 |
|---|--------|---------|
| `flow` | 3 | BlockType 枚举、JSON round-trip、政务服务 JSON 解析 |
| `flow/provider` | 5 | Register/Get、重复注册、缺失查找、BlockIDs 排序、AgentProviderFunc |
| `flow/executor` | 11 | 线性链、diamond、政务服务、环检测、自环、非法引用、重复 ID、拓扑排序、上下游查询、LevelOf、不连通图 |
| `flow/executor` (Build) | 6 | 线性链构建、diamond 构建、政务服务构建、nil Provider、缺失 Agent、单 Agent |
| `flow/parser` | 15 | 版本检查、metadata、空 blocks、重复 ID、保留名、非法类型、非法 Edge、自环、环检测、重复 Edge、重复 outputKey、Normalize 默认值/去空白 |
| `flow/adapter` | 6 | Sequential 转换、Diamond 转换、Loop 转换、单 Agent、nil schema、metadata 保留 |
| **合计** | **46** | |

### 12.2 运行测试

```bash
# 全部测试
go test ./orchestration/flow/... -v

# 仅 DAG 测试
go test ./orchestration/flow/executor/ -v -run TestDAG

# 仅 Builder 测试
go test ./orchestration/flow/executor/ -v -run TestBuild

# go vet
go vet ./orchestration/flow/...
```

### 12.3 典型测试用例

**DAG Diamond 模式**：
```go
// Classify → {Payment, Risk} → Merge
blocks := []flow.Block{
    {ID: "classify", ...}, {ID: "payment", ...}, {ID: "risk", ...}, {ID: "merge", ...},
}
edges := []flow.Edge{
    {SourceID: "classify", TargetID: "payment"},
    {SourceID: "classify", TargetID: "risk"},
    {SourceID: "payment", TargetID: "merge"},
    {SourceID: "risk", TargetID: "merge"},
}
dag, _ := NewDAG(blocks, edges)
levels := dag.Levels()
// Level 0: [classify], Level 1: [payment, risk], Level 2: [merge]
```

**环检测**：
```go
// a → b → c → a (cycle)
_, err := NewDAG(blocks, cyclicEdges)
// err = "cycle detected involving blocks [a b c]"
```

**v1→v2 Diamond 转换**：
```go
// v1: Sequential[ClassifyOrder, Parallel[Payment, RiskCheck], MergeAndComplete]
v2Schema, _ := adapter.Convert(v1Schema)
// v2Schema.Blocks = [ClassifyOrder, PaymentProcess, RiskCheck, MergeAndComplete]
// v2Schema.Edges = [ClassifyOrder→Payment, ClassifyOrder→Risk, Payment→Merge, Risk→Merge]
```
