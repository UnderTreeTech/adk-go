# Flow v2 多分支归并跳过方案

> 核心问题：在 DAG flow 中，多分支归并（Merge/Join）时，如何让某个条件分支可以跳过，且归并点能正确感知？

## 场景示意

```
         ┌── BranchA (always) ──┐
Start ──┤                        ├── Merge ── End
         └── BranchB (conditional, may skip) ──┘
```

当 `BranchB` 不需要执行时：
1. **BranchB 不应消耗 LLM 调用**（节省成本和时间）
2. **Merge 节点仍需正常执行**（不能被阻塞或报错）
3. **Merge 节点需要知道 BranchB 的状态**（是实际结果还是默认值）

---

## 现状分析

### 当前架构

```
FlowSchema (blocks + edges)
       │
       ▼
   NewDAG() ──── Kahn's 算法 ──── 拓扑层级
       │
       ▼
   Build() ──── level 0: Agent ──── SequentialAgent
              ──── level 1: [A, B] ── ParallelAgent(A, B)  ← 所有分支都会执行
              ──── level 2: Merge ─── SequentialAgent
       │
       ▼
  ADK Runner 执行
```

### 已有跳过机制

v1 的 `conditional_skip` 模式（`examples/agents/parallel-conditional/main.go`）：

```go
// BeforeAgentCallback 返回非 nil Content → ADK 框架跳过 Agent 执行
// 同时写入默认值到 state → 下游有数据可读
riskCheck, _ := agent.NewLLMAgent(agent.Config{
    LLMAgentConfig: llmagent.Config{
        Name:      "RiskCheck",
        OutputKey: "risk_result",
        BeforeAgentCallbacks: []adkagent.BeforeAgentCallback{
            conditionalSkipCallback("needs_risk_check", "risk_result",
                `{"status":"auto_approved","risk_level":"none"}`),
        },
    },
})
```

**局限**：
- 仅在代码层面配置，schema 不可见
- 归并点无法感知上游哪些分支可跳过
- 无 parser 层校验（如：可跳过分支是否提供了默认值）

---

## 方案 A：Edge-Level Condition（边条件激活）

### 核心思路

在 `Edge` 上增加条件字段，运行时评估条件决定该边是否"激活"。不激活的边意味着上游分支对归并点"不可见"——归并点不再等待该分支。

### Schema 变更

```go
// schema.go
type Edge struct {
    SourceID  string          `json:"sourceId"`
    TargetID  string          `json:"targetId"`

    // 新增：边激活条件
    Condition *EdgeCondition  `json:"condition,omitempty"`
}

// EdgeCondition 定义边激活条件
type EdgeCondition struct {
    // 模式1：引用 session state 中的布尔值
    // 当 state[StateKey] 为 true 时，边激活；否则边不激活
    StateKey string `json:"stateKey,omitempty"`

    // 模式2（未来扩展）：表达式求值
    // 例如 "amount >= 10000"、"risk_level == 'high'"
    Expression string `json:"expression,omitempty"`

    // 当边不激活时，写入 TargetID 的 InputKeys 对应的默认值
    // key = InputKey name, value = default value
    Defaults map[string]string `json:"defaults,omitempty"`
}
```

### JSON 示例

```json
{
  "blocks": [
    {"id": "start",      "name": "Start",     "type": "start"},
    {"id": "classify",   "name": "Classify",   "type": "agent", "outputKey": "order_info"},
    {"id": "payment",    "name": "Payment",    "type": "agent", "outputKey": "payment_result"},
    {"id": "risk_check", "name": "RiskCheck",  "type": "agent", "outputKey": "risk_result"},
    {"id": "merge",      "name": "Merge",      "type": "agent", "outputKey": "final_result",
     "inputKeys": ["payment_result", "risk_result"]},
    {"id": "end",        "name": "End",        "type": "end"}
  ],
  "edges": [
    {"sourceId": "start",    "targetId": "classify"},
    {"sourceId": "classify", "targetId": "payment"},
    {"sourceId": "classify", "targetId": "risk_check",
     "condition": {"stateKey": "needs_risk_check"}},
    {"sourceId": "payment",    "targetId": "merge"},
    {"sourceId": "risk_check", "targetId": "merge",
     "condition": {"stateKey": "needs_risk_check",
                   "defaults": {"risk_result": "{\"status\":\"auto_approved\"}"}}},
    {"sourceId": "merge", "targetId": "end"}
  ]
}
```

### 执行引擎变更

**核心变更**：DAG 拓扑从"静态计算一次"变为"运行时动态计算"。

```go
// dag.go 新增
// EffectiveInDegree 计算在给定 state 下，节点的有效入度
// 条件不满足的边不计入
func (d *DAG) EffectiveInDegree(blockID string, state State) int {
    count := 0
    for _, upstream := range d.reverse[blockID] {
        edge := d.findEdge(upstream, blockID)
        if edge.Condition != nil && !evaluateEdgeCondition(edge.Condition, state) {
            continue // 该边不激活
        }
        count++
    }
    return count
}

// ActiveUpstream 返回在给定 state 下，哪些上游分支是活跃的
func (d *DAG) ActiveUpstream(blockID string, state State) []string {
    var active []string
    for _, upstream := range d.reverse[blockID] {
        edge := d.findEdge(upstream, blockID)
        if edge.Condition == nil || evaluateEdgeCondition(edge.Condition, state) {
            active = append(active, upstream)
        }
    }
    return active
}
```

**Executor 变更**：从"先拓扑后执行"变为"动态调度"。

```go
// executor/agent.go
// Build 需要重构为动态 DAG executor
type DAGExecutor struct {
    dag       *DAG
    provider  provider.AgentProvider
    state     State
    completed map[string]bool
    results   map[string]string
}

func (e *DAGExecutor) Run(ctx context.Context) error {
    // 1. 找到所有入度为0的节点，启动
    // 2. 节点完成后，更新 state，检查下游节点
    // 3. 下游节点的有效入度=0时，启动该节点
    // 4. 条件边不满足时，写入默认值，归并点入度减少
    // 5. 重复直到所有节点完成
}
```

### 归并点感知机制

```
                 ┌── payment ──────────────────┐
    classify ───┤                               ├── merge
                 └── risk_check ── (条件边) ────┘
                              │
                    state["needs_risk_check"] = false
                              │
                    边不激活 → merge.EffectiveInDegree = 1
                    默认值写入 → state["risk_result"] = "auto_approved"
                    merge 只需等待 payment 完成
```

### 优缺点

| 维度 | 评价 |
|------|------|
| **语义直观性** | ⭐⭐⭐⭐⭐ 边=数据流，条件边=条件数据流，概念一致 |
| **归并点感知** | ⭐⭐⭐⭐⭐ 入度重算，归并点自然知道等待谁 |
| **实现复杂度** | ⭐⭐ 需要重构 executor 为事件驱动模式 |
| **架构兼容性** | ⭐⭐ 需要替换 SequentialAgent/ParallelAgent 模型 |
| **灵活性** | ⭐⭐⭐ 只能基于 state key 判断，表达式模式需额外实现 |
| **默认值管理** | ⭐⭐⭐ 在 Edge 上配置默认值，语义稍违和（默认值应该是 Block 的事） |
| **可观测性** | ⭐⭐⭐⭐ 条件边评估结果可追踪 |

### 关键风险

1. **executor 重构范围大**：当前 `Build()` 返回 `SequentialAgent`，改为事件驱动后接口需要大改
2. **ADK ParallelAgent 兼容**：动态 DAG executor 不能直接用 `ParallelAgent`，需要自建并发调度
3. **条件评估时机**：边的条件在何时评估？是在 source 执行完成后，还是在 DAG 调度时？
4. **默认值放置位置**：Edge 上的 `defaults` 语义不够自然——默认值本应是 Block（数据生产者）的属性

---

## 方案 B：Block-Level Condition Guard（节点条件守卫）

### 核心思路

在 `Block` 上增加 `guard` 字段，声明该节点的执行前提条件。守卫不满足时，节点不执行 LLM 调用，而是直接产出默认值。底层自动转换为 `BeforeAgentCallback` 注入。

### Schema 变更

```go
// schema.go
type Block struct {
    ID          string     `json:"id"`
    Name        string     `json:"name"`
    Type        BlockType  `json:"type"`
    Description string     `json:"description,omitempty"`
    OutputKey   string     `json:"outputKey,omitempty"`
    InputKeys   []string   `json:"inputKeys,omitempty"`

    // 新增：执行守卫
    Guard *BlockGuard `json:"guard,omitempty"`
}

// BlockGuard 定义节点的执行前提条件
type BlockGuard struct {
    // 条件 state key：当 state[ConditionKey] 为 false/"false"/"0"/"no" 时跳过
    ConditionKey string `json:"conditionKey"`

    // 跳过时写入 OutputKey 的默认值
    DefaultValue string `json:"defaultValue"`

    // 可选：跳过标记 key，跳过时写入 state[_skipped_{blockId}] = true
    SkippedKey string `json:"skippedKey,omitempty"`
}
```

### JSON 示例

```json
{
  "blocks": [
    {"id": "start",      "name": "Start",     "type": "start"},
    {"id": "classify",   "name": "Classify",   "type": "agent", "outputKey": "order_info"},
    {"id": "payment",    "name": "Payment",    "type": "agent", "outputKey": "payment_result"},
    {"id": "risk_check", "name": "RiskCheck",  "type": "agent", "outputKey": "risk_result",
     "guard": {
       "conditionKey": "needs_risk_check",
       "defaultValue": "{\"status\":\"auto_approved\",\"risk_level\":\"none\",\"message\":\"小额订单自动通过\"}",
       "skippedKey": "_skipped_risk_check"
     }},
    {"id": "merge",      "name": "Merge",      "type": "agent", "outputKey": "final_result",
     "inputKeys": ["payment_result", "risk_result"]},
    {"id": "end",        "name": "End",        "type": "end"}
  ],
  "edges": [
    {"sourceId": "start",      "targetId": "classify"},
    {"sourceId": "classify",   "targetId": "payment"},
    {"sourceId": "classify",   "targetId": "risk_check"},
    {"sourceId": "payment",    "targetId": "merge"},
    {"sourceId": "risk_check", "targetId": "merge"},
    {"sourceId": "merge",      "targetId": "end"}
  ]
}
```

### 执行引擎变更

**最小变更**：只在 `resolveAgent` 中注入 callback。

```go
// executor/agent.go
func resolveAgent(block flow.Block, prov provider.AgentProvider) (adkagent.Agent, error) {
    var ag adkagent.Agent
    var err error

    switch block.Type {
    case flow.BlockTypeAgent:
        ag, err = prov.Get(block.ID)
        if err != nil {
            return nil, fmt.Errorf("resolve agent for block %q: %w", block.ID, err)
        }

        // ★ 新增：如果 Block 有 Guard，注入 BeforeAgentCallback ★
        if block.Guard != nil {
            ag, err = injectGuardCallback(ag, block)
            if err != nil {
                return nil, fmt.Errorf("inject guard for block %q: %w", block.ID, err)
            }
        }

    case flow.BlockTypeStart, flow.BlockTypeEnd:
        ag, err = newPassthroughAgent(block.Name, block.OutputKey)
    }

    return ag, err
}

// injectGuardCallback 为 Agent 注入条件守卫 callback
func injectGuardCallback(ag adkagent.Agent, block flow.Block) (adkagent.Agent, error) {
    guard := block.Guard
    cb := func(ctx adkagent.CallbackContext) (*genai.Content, error) {
        val, err := ctx.ReadonlyState().Get(guard.ConditionKey)
        if err != nil {
            // state 中没有条件 key，默认不跳过
            return nil, nil
        }

        shouldSkip := false
        switch v := val.(type) {
        case bool:
            shouldSkip = !v
        case string:
            shouldSkip = (v == "false" || v == "no" || v == "0")
        }

        if !shouldSkip {
            return nil, nil // 条件满足，正常执行
        }

        // ★ 条件不满足，跳过并写入默认值 ★
        if block.OutputKey != "" {
            if err := ctx.State().Set(block.OutputKey, guard.DefaultValue); err != nil {
                return nil, fmt.Errorf("guard: failed to set default for %q: %w", block.OutputKey, err)
            }
        }

        // 可选：写入跳过标记
        if guard.SkippedKey != "" {
            _ = ctx.State().Set(guard.SkippedKey, "true")
        }

        return genai.NewContentFromText(
            fmt.Sprintf("[SKIPPED] Block %q skipped: condition %q is false", block.ID, guard.ConditionKey),
            genai.RoleModel,
        ), nil
    }

    // 注入到 Agent 的 BeforeAgentCallbacks
    // 注意：这里需要根据 ADK Agent 的具体接口来注入
    return agent.WithBeforeCallback(ag, cb), nil
}
```

### 归并点感知机制

```
                 ┌── payment (always) ────────────────┐
    classify ───┤                                     ├── merge
                 └── risk_check (guard: needs_risk) ──┘
                              │
                    state["needs_risk_check"] = false
                              │
                    risk_check 的 BeforeAgentCallback 触发：
                    1. state["risk_result"] = 默认值 ✅
                    2. state["_skipped_risk_check"] = "true" ✅
                    3. 返回非 nil Content → LLM 不调用 ✅
                              │
                    merge 读取 state["risk_result"] → 有数据 ✅
                    merge 读取 state["_skipped_risk_check"] → 可感知跳过状态 ✅
```

### Parser 校验增强

```go
// parser/parser.go 新增
func (p *Parser) validateGuard(block Block) error {
    if block.Guard == nil {
        return nil
    }

    // 1. Guard 要求 Block 必须有 OutputKey
    if block.OutputKey == "" {
        return fmt.Errorf("block %q has guard but no outputKey", block.ID)
    }

    // 2. Guard 必须有 DefaultValue
    if block.Guard.DefaultValue == "" {
        return fmt.Errorf("block %q has guard but no defaultValue", block.ID)
    }

    // 3. Guard 必须有 ConditionKey
    if block.Guard.ConditionKey == "" {
        return fmt.Errorf("block %q has guard but no conditionKey", block.ID)
    }

    return nil
}
```

### 优缺点

| 维度 | 评价 |
|------|------|
| **语义直观性** | ⭐⭐⭐⭐ "这个节点在什么条件下执行"是自然的思维模型 |
| **归并点感知** | ⭐⭐⭐ 通过默认值+跳过标记间接感知，不是主动感知 |
| **实现复杂度** | ⭐⭐⭐⭐ 只需修改 resolveAgent，注入 callback |
| **架构兼容性** | ⭐⭐⭐⭐⭐ 完全兼容现有 SequentialAgent/ParallelAgent 模型 |
| **灵活性** | ⭐⭐⭐ 只能基于 state key，不支持表达式 |
| **默认值管理** | ⭐⭐⭐⭐⭐ 默认值在 Block 上，语义自然 |
| **可观测性** | ⭐⭐⭐⭐ 跳过标记 + callback 返回的 Content 可追踪 |
| **Parser 校验** | ⭐⭐⭐⭐⭐ 可在 parser 层校验 Guard 配置完整性 |

### 关键风险

1. **ParallelAgent 仍会启动所有分支**：虽然 callback 会快速跳过，但仍有极小的调度开销
2. **归并点被动感知**：Merge 节点只能通过 `state` 间接知道上游是否跳过，不能主动决策"不等了"
3. **callback 注入方式**：需要确认 ADK Agent 接口是否支持运行时注入 `BeforeAgentCallback`（可能需要包装器）

---

## 方案 C：Schema 声明式 Skip + 自动 Instruction 增强

### 核心思路

在方案 B 的基础上，进一步让 **归并点的 instruction 自动感知上游跳过状态**。Parser 在构建时分析归并点的上游跳过信息，自动在 instruction 中注入上下文。

### Schema 变更

与方案 B 相同（Block.Guard），新增归并点元数据：

```go
type Block struct {
    // ... 现有字段 ...

    // 新增：跳过配置（等价于方案 B 的 Guard，命名不同体现声明式风格）
    Skip *SkipConfig `json:"skip,omitempty"`
}

type SkipConfig struct {
    When          string `json:"when"`          // 条件 state key
    DefaultOutput string `json:"defaultOutput"` // 跳过时默认输出
    MarkSkipped   bool   `json:"markSkipped"`   // 是否写入 _skipped 标记
}

// 新增：归并点合并配置
type Block struct {
    // ... 现有字段 + Skip ...

    // 新增：归并点配置
    Merge *MergeConfig `json:"merge,omitempty"`
}

type MergeConfig struct {
    // 归并模式：
    // - "all"     : 等待所有上游（默认）
    // - "partial" : 允许部分上游跳过
    Mode string `json:"mode,omitempty"`

    // 是否自动在 instruction 中注入上游跳过信息
    AutoInjectSkipContext bool `json:"autoInjectSkipContext,omitempty"`
}
```

### JSON 示例

```json
{
  "blocks": [
    {"id": "risk_check", "name": "RiskCheck", "type": "agent", "outputKey": "risk_result",
     "skip": {
       "when": "needs_risk_check",
       "defaultOutput": "{\"status\":\"auto_approved\",\"risk_level\":\"none\"}",
       "markSkipped": true
     }},
    {"id": "merge", "name": "Merge", "type": "agent", "outputKey": "final_result",
     "inputKeys": ["payment_result", "risk_result"],
     "merge": {
       "mode": "partial",
       "autoInjectSkipContext": true
     }}
  ]
}
```

### 自动 Instruction 增强

```go
// executor/agent.go
// 当归并点配置了 autoInjectSkipContext 时，自动增强 instruction
func injectSkipContext(block Block, schema FlowSchema, prov AgentProvider) {
    if block.Merge == nil || !block.Merge.AutoInjectSkipContext {
        return
    }

    // 查找上游可跳过的 Block
    var skippableUpstream []Block
    for _, inputKey := range block.InputKeys {
        for _, b := range schema.Blocks {
            if b.OutputKey == inputKey && b.Skip != nil {
                skippableUpstream = append(skippableUpstream, b)
            }
        }
    }

    if len(skippableUpstream) == 0 {
        return
    }

    // 生成注入文本
    var sb strings.Builder
    sb.WriteString("\n\n--- 上游分支跳过信息 ---\n")
    for _, b := range skippableUpstream {
        sb.WriteString(fmt.Sprintf(
            "- %s (%s): 当 state[%q] 为 false 时跳过，默认输出为 %q\n",
            b.Name, b.OutputKey, b.Skip.When, b.Skip.DefaultOutput,
        ))
    }
    sb.WriteString("请注意区分实际执行结果和跳过默认值。\n")

    // 注入到 Agent 的 Instruction 中
    ag, _ := prov.Get(block.ID)
    // ag.Instruction += sb.String()  // 取决于 ADK Agent 是否支持运行时修改 instruction
}
```

### 优缺点

| 维度 | 评价 |
|------|------|
| **语义直观性** | ⭐⭐⭐⭐ 与方案 B 类似，多了归并点配置 |
| **归并点感知** | ⭐⭐⭐⭐ 通过 instruction 注入，LLM 能理解跳过语义 |
| **实现复杂度** | ⭐⭐⭐ 需要处理 instruction 注入逻辑 |
| **架构兼容性** | ⭐⭐⭐⭐ 与方案 B 相同 |
| **灵活性** | ⭐⭐⭐ instruction 注入不一定可靠——LLM 可能忽略 |
| **默认值管理** | ⭐⭐⭐⭐⭐ 同方案 B |
| **可观测性** | ⭐⭐⭐⭐⭐ 最强——schema 声明 + instruction 增强 + 跳过标记 |

### 关键风险

1. **LLM 不一定遵守 instruction 注入**：注入的"请注意区分实际结果和默认值"可能被忽略
2. **instruction 注入实现复杂**：需要修改已构建的 Agent，ADK 可能不支持运行时修改 instruction
3. **方案 B 的所有风险同样适用**

---

## 方案 D：Callback 注入（纯代码侧，Schema 零侵入）

### 核心思路

不在 schema 中增加任何条件字段，完全依赖业务代码在注册 Agent 时自行注入 `BeforeAgentCallback`。Schema 只管拓扑，条件逻辑全部在代码侧。

### Schema 变更

**无变更**。`schema.go` 的 `Block` 和 `Edge` 保持原样。

### 实现方式

业务代码侧：

```go
// 业务代码：注册 Agent 时自行配置 callback
riskAgent, _ := agent.NewLLMAgent(agent.Config{
    LLMAgentConfig: llmagent.Config{
        Name:      "RiskCheck",
        Model:     llmModel,
        Instruction: "你是一个风控审查 Agent...",
        OutputKey: "risk_result",
        BeforeAgentCallbacks: []adkagent.BeforeAgentCallback{
            conditionalSkipCallback("needs_risk_check", "risk_result",
                `{"status":"auto_approved","risk_level":"none"}`),
        },
    },
})

prov := provider.NewMapAgentProvider()
prov.Register("risk_check", riskAgent)
```

Schema 侧：

```json
{
  "blocks": [
    {"id": "risk_check", "name": "RiskCheck", "type": "agent", "outputKey": "risk_result"}
  ]
}
```

→ schema 中 `risk_check` block 没有任何特殊配置，条件逻辑完全在代码中。

### 归并点感知

归并点无法感知跳过状态——只能被动读取 `state["risk_result"]`：
- 如果 `risk_check` 执行了，`risk_result` 是实际风控结果
- 如果 `risk_check` 跳过了，`risk_result` 是默认值

归并点的 instruction 需要人工写明："如果 `risk_result` 包含 `auto_approved`，说明是跳过的默认值"。

### 优缺点

| 维度 | 评价 |
|------|------|
| **语义直观性** | ⭐⭐ schema 不可见条件逻辑 |
| **归并点感知** | ⭐⭐ 归并点完全不知道上游可跳过 |
| **实现复杂度** | ⭐⭐⭐⭐⭐ 零实现成本，已有 callback 机制 |
| **架构兼容性** | ⭐⭐⭐⭐⭐ 完全兼容 |
| **灵活性** | ⭐⭐⭐⭐⭐ callback 可写任何逻辑 |
| **可观测性** | ⭐ 需要看代码才能知道哪些分支可跳过 |
| **Parser 校验** | ❌ 无法在 parser 层校验 |

### 关键风险

1. **配置分散**：拓扑在 schema，条件在代码，维护需两边对齐
2. **无法自动化校验**：可跳过的分支是否提供了默认值？归并点是否能处理缺失数据？
3. **归并点完全被动**：只能通过默认值间接感知，不能主动决策

---

## 方案 E：Join Strategy（归并点策略化）

### 核心思路

从归并点出发——让归并点本身具有策略，决定等待多少上游分支。这是唯一一个**归并点主动感知**的方案。

### Schema 变更

```go
type Block struct {
    // ... 现有字段 ...

    // 新增：归并策略
    Join *JoinConfig `json:"join,omitempty"`
}

type JoinConfig struct {
    // 策略类型
    // - "all"    : 等待所有上游完成（默认，当前行为）
    // - "any"    : 任意一个上游完成即可启动
    // - "quorum" : 至少 N 个上游完成即可启动
    // - "dynamic": 运行时根据条件动态决定
    Strategy JoinStrategy `json:"strategy"`

    // Quorum 所需的最小完成数（仅 strategy=quorum 时有效）
    MinCount int `json:"minCount,omitempty"`

    // 超时后自动启动（毫秒），即使未满足策略条件
    TimeoutMs int64 `json:"timeoutMs,omitempty"`

    // 未完成的分支使用的默认值
    Defaults map[string]string `json:"defaults,omitempty"`
}

type JoinStrategy string

const (
    JoinStrategyAll    JoinStrategy = "all"
    JoinStrategyAny    JoinStrategy = "any"
    JoinStrategyQuorum JoinStrategy = "quorum"
    JoinStrategyDynamic JoinStrategy = "dynamic"
)
```

### JSON 示例

```json
{
  "blocks": [
    {"id": "merge", "name": "Merge", "type": "agent", "outputKey": "final_result",
     "inputKeys": ["payment_result", "risk_result"],
     "join": {
       "strategy": "quorum",
       "minCount": 1,
       "timeoutMs": 30000,
       "defaults": {
         "risk_result": "{\"status\":\"auto_approved\",\"risk_level\":\"none\"}"
       }
     }}
  ]
}
```

### 执行引擎变更（重大重构）

```go
// 需要完全替换 SequentialAgent + ParallelAgent 模型
// 改为事件驱动的 DAG executor

type DAGExecutor struct {
    dag        *DAG
    provider   provider.AgentProvider
    joinConfig map[string]*JoinConfig

    mu        sync.Mutex
    completed map[string]bool
    running   map[string]bool
    results   map[string]any
    waiting   map[string]int  // blockID → 剩余等待数

    events    chan blockEvent
}

type blockEvent struct {
    blockID string
    result  any
    err     error
}

func (e *DAGExecutor) Run(ctx context.Context, input any) (any, error) {
    // 1. 初始化：所有入度为0的节点加入 ready 队列
    // 2. 启动 ready 节点
    // 3. 节点完成后发送事件
    // 4. 归并点根据 Join 策略判断是否可以启动：
    //    - all: 等待所有上游完成
    //    - any: 第一个上游完成即启动
    //    - quorum: N 个上游完成即启动
    //    - dynamic: 运行时评估条件
    // 5. 未等待到的分支使用 defaults 填充
    // 6. 超时机制兜底
    // 7. 重复直到 end 节点完成
}
```

### 执行流程示意

```
                 ┌── payment ──────────────────────┐
    classify ───┤                                   ├── merge (join: quorum, minCount=1)
                 └── risk_check (guard/skip) ──────┘

    1. classify 执行完成 → state["needs_risk_check"] = false
    2. payment 和 risk_check 同时启动
    3. risk_check 的 guard 触发跳过 → 快速完成
    4. merge 策略 = quorum, minCount = 1
       - payment 完成 → 已有 1 个上游完成 → 满足 minCount
       - risk_check 跳过 → 不算"完成"，但 defaults 填充了 risk_result
       → merge 启动，读取 payment_result + 默认 risk_result
```

### 优缺点

| 维度 | 评价 |
|------|------|
| **语义直观性** | ⭐⭐⭐⭐⭐ "归并点需要等待几个上游"是最自然的提问 |
| **归并点感知** | ⭐⭐⭐⭐⭐ 归并点主动决策，不再是被动等待 |
| **实现复杂度** | ⭐ 需要完全重写 executor |
| **架构兼容性** | ⭐ 与 ADK SequentialAgent/ParallelAgent 不兼容 |
| **灵活性** | ⭐⭐⭐⭐ all/any/quorum/dynamic 覆盖所有归并语义 |
| **超时兜底** | ⭐⭐⭐⭐⭐ 唯一能处理"某个分支卡住"的方案 |
| **可观测性** | ⭐⭐⭐⭐ 策略评估过程可追踪 |

### 关键风险

1. **executor 完全重写**：工作量大，需要自建事件驱动的 DAG 调度器
2. **与 ADK 框架解耦**：不能复用 `SequentialAgent`/`ParallelAgent`，需要直接调用单个 Agent
3. **并发安全**：事件驱动模型需要处理并发、超时、取消等复杂场景
4. **defaults 语义模糊**：未完成的分支使用 defaults，但这与"跳过"不是同一概念
5. **测试复杂度**：any/quorum 策略的正确性验证比 all 复杂得多

---

## 方案对比总览

| 维度 | A: Edge条件 | B: Block守卫 | C: 声明式Skip+Instruction | D: Callback注入 | E: Join策略 |
|------|------------|-------------|--------------------------|----------------|------------|
| **Schema侵入** | Edge+Condition | Block+Guard | Block+Skip+Merge | 无 | Block+Join |
| **归并点感知方式** | 入度重算(主动) | 默认值(被动) | 默认值+Instruction(被动增强) | 默认值(被动) | 策略决策(主动) |
| **实现复杂度** | ⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐ |
| **架构兼容性** | ⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐ |
| **灵活性** | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| **可观测性** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐ | ⭐⭐⭐⭐ |
| **归并点缺数据** | 需默认值 | ✅默认值填充 | ✅默认值+增强 | ✅默认值填充 | ✅defaults填充 |
| **Parser校验** | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ❌ | ⭐⭐⭐⭐ |
| **超时兜底** | ❌ | ❌ | ❌ | ❌ | ✅ |

### 分数说明

- 实现复杂度：⭐ = 最复杂，⭐⭐⭐⭐⭐ = 最简单
- 架构兼容性：⭐ = 完全不兼容，⭐⭐⭐⭐⭐ = 完全兼容

---

## 组合方案建议

### 短期（快速落地）：B + D 组合

```
Schema: Block.Guard 声明条件守卫
Engine: resolveAgent 时自动注入 BeforeAgentCallback (方案D的底层实现)
归并点: 通过 DefaultValue + SkippedKey 被动感知
```

- Block Guard 作为 schema 声明式入口
- 底层自动转换为 BeforeAgentCallback 注入（复用方案 D 的 callback 机制）
- 与现有架构完全兼容，改动最小
- 归并点通过 `state` 间接感知跳过状态

### 中期（增强感知）：B + C 组合

```
Schema: Block.Guard + Block.Merge
Engine: resolveAgent 注入 callback + 自动增强归并点 instruction
归并点: DefaultValue + SkippedKey + Instruction 上下文增强
```

- 在方案 B 基础上增加归并点 Merge 配置
- 自动在归并点的 instruction 中注入上游跳过信息
- LLM 能理解"某个上游是默认值"

### 长期（架构演进）：A + E 组合

```
Schema: Edge.Condition + Block.Join
Engine: 事件驱动 DAG executor 替换 Sequential/Parallel 模型
归并点: 主动策略决策（all/any/quorum/dynamic）
```

- Edge Condition 解决条件路径问题
- Join Strategy 解决归并语义问题
- 需要重写 executor，工作量大但架构更优
- 超时机制可兜底异常场景

### 渐进路线

```
Phase 1: D (纯Callback) ──→ 快速验证模式可行性
Phase 2: B (Block Guard) ──→ Schema 声明式 + Parser 校验
Phase 3: C (Merge Config) ──→ 归并点感知增强
Phase 4: A (Edge Condition) ──→ 条件路径（需重构 executor）
Phase 5: E (Join Strategy) ──→ 归并策略化（需事件驱动 executor）
```
