# Edge-Level Condition（边条件激活）— 设计与实现文档

> 版本: v1.0  
> 日期: 2026-06-14  
> 状态: 设计完成，待实现

---

## 1. 问题与动机

### 1.1 核心问题

在 Flow v2 DAG 多 Agent 编排中，当多分支归并时某个分支不需要执行，如何让归并点主动感知该分支可以跳过？

```
         ┌── BranchA (always) ──┐
Start ──┤                        ├── Merge ── End
         └── BranchB (conditional, may skip) ──┘
```

### 1.2 当前局限

- Flow v2 执行模型是**静态全路径执行**：所有边都会被遍历
- ParallelAgent 等待所有子 Agent 完成，无法感知某分支可以跳过
- 已有的 `conditional_skip` callback 模式（v1）是被动方案：先启动再跳过，归并点被动读默认值

### 1.3 选定方案

**Edge-Level Condition（边条件激活）**：在 Edge 上声明激活条件，运行时评估条件决定边是否活跃。不活跃的边触发**传递性可达性分析**，不可达的节点被剪枝不执行，归并点主动感知（入度减少）。

辅以 **Block.SkipOutput**：节点声明被跳过时的默认输出，确保归并点有数据可读。

---

## 2. 设计原则

1. **条件激活属于边**：分支跳过 = 路径不走 = 边条件（与 BPMN/Petri 网/Airflow 一致）
2. **默认输出属于节点**：节点的"空输出"是节点自身属性（类比 monoid 的 identity element）
3. **传递性剪枝**：条件边不激活 → 检查下游节点的所有入边 → 递归传播直到遇到仍有活跃入边的节点
4. **向后兼容**：无条件边的 schema 走原有路径，行为完全不变
5. **层级执行**：保持 level-by-level 的执行结构，每层执行前评估条件并过滤

---

## 3. 剪枝传递性分析

### 3.1 核心概念

剪枝**不是**只在归并点发生，而是从条件边不激活处沿 DAG 向下做**传递性可达性分析**。

### 3.2 传播规则

```
deactivate(edge A→B):
  → 检查 B 的所有入边是否都不活跃
    → 是 → B 不可达 → 剪枝 B → 递归检查 B 的所有出边
    → 否 → B 仍可达（至少有一条活跃入边），停止传播
```

### 3.3 图示

**简单菱形（只有归并点受影响）**：

```
         ┌── payment ──────────────┐
classify ┤                          ├── merge
         └── risk_check ───────────┘
              ↑ 条件边不激活
              → risk_check 不可达，剪枝
              → merge 的有效入度从 2 变为 1
              → merge 只等 payment
```

**复杂 DAG（归并点之外的节点也受影响）**：

```
         ┌── payment ────────────────────────────┐
classify ┤                                          ├── merge
         └── risk_check ──→ risk_detail ───────────┘
              ↑ 条件边不激活
              → risk_check 不可达，剪枝
              → risk_detail 的唯一入边也断了 → 不可达，剪枝
              → merge 的有效入度从 3 变为 1
              → merge 只等 payment
```

**多条件边同时不激活（多条边独立控制）**：

```
                 ┌── payment ─────────────────────────────────┐
                 │                                            │
classify ────────┤                                            ├── merge ── end
                 │                                            │
                 ├── risk_check ──(cond: needs_risk)──→ risk_detail ──┤
                 │                                            │
                 └── intl_verify ──(cond: is_international)──┘

  假设 state:
    needs_risk = false
    is_international = false

  可达性分析：
    1. classify → 可达 ✅ (入度=0)
    2. classify → payment (无条件) → payment 可达 ✅
    3. classify → risk_check (cond=false) → 边不活跃 → risk_check 不可达 ❌
    4. classify → intl_verify (cond=false) → 边不活跃 → intl_verify 不可达 ❌
    5. risk_check → risk_detail (来源不可达) → risk_detail 不可达 ❌
    6. merge 的三条入边中，只有 payment→merge 活跃
       → merge 可达 ✅，有效入度=1

  执行结果：
    Level 0: classify → 执行
    Level 1: payment → 执行; risk_check → 跳过(写默认值); intl_verify → 跳过(写默认值)
    Level 2: risk_detail → 跳过(写默认值)
    Level 3: merge → 执行(读到 payment_result + risk_detail默认值 + intl_verify默认值)

  ★ 关键点：多条条件边可以独立控制不同分支的跳过，互不干扰
```

**菱形嵌套（分支内部也有菱形归并）**：

```
                      ┌── doc_review ────────────────────┐
                      │                                   │
       ┌── legal ─────┤                                   ├── legal_merge ──┐
       │              │                                   │                 │
       │              └── compliance ─(cond: is_regulated)─┘                 │
       │                                                                    │
start ─┤                                                                    ├── final_merge
       │                                                                    │
       └── tech_eval ─────────────────────────────────────────────────────┘

  假设 state["is_regulated"] = false

  可达性分析：
    1. start → 可达 ✅
    2. start → legal (无条件) → legal 可达 ✅
    3. start → tech_eval (无条件) → tech_eval 可达 ✅
    4. legal → doc_review (无条件) → doc_review 可达 ✅
    5. legal → compliance (cond=false) → compliance 不可达 ❌
    6. doc_review → legal_merge (来源可达) → legal_merge 可达 ✅
    7. compliance → legal_merge (来源不可达，但 doc_review→legal_merge 仍活跃)
       → legal_merge 已标记可达，无影响
    8. legal_merge → final_merge → final_merge 可达 ✅
    9. tech_eval → final_merge → 已标记可达

  执行结果：
    Level 0: start → 执行
    Level 1: legal, tech_eval → 并行执行
    Level 2: doc_review → 执行; compliance → 跳过(写默认值)
    Level 3: legal_merge → 执行(读到 doc_review_result + compliance默认值)
    Level 4: final_merge → 执行(读到 legal_merge_result + tech_eval_result)

  ★ 关键点：分支内部的条件边只影响分支内部的子图，不影响外部菱形结构
```

**扇出-扇入（一个节点扇出多个条件分支，再汇聚到同一点）**：

```
         ┌── feature_A ──(cond: enable_a)──┐
         │                                  │
  gate ──┼── feature_B ──(cond: enable_b)──┼── aggregate ── output
         │                                  │
         └── feature_C ──(cond: enable_c)──┘

  场景 1: enable_a=true, enable_b=false, enable_c=true
    → feature_A 可达 ✅, feature_B 不可达 ❌, feature_C 可达 ✅
    → aggregate 有效入度=2 (from A and C)
    → 执行: gate → {A, C} 并行 → aggregate(读到A实际结果 + B默认值 + C实际结果)

  场景 2: enable_a=false, enable_b=false, enable_c=false
    → feature_A/B/C 全部不可达 ❌
    → aggregate 的所有入边都不活跃 → aggregate 不可达 ❌
    → output 的唯一入边也不活跃 → output 不可达 ❌
    → 只有 gate 执行，其余全部跳过

  场景 3: enable_a=true, enable_b=true, enable_c=true
    → 全部可达 ✅（退化为无条件全路径执行）
    → 执行: gate → {A, B, C} 并行 → aggregate → output

  ★ 关键点：
    - 同一扇出节点的多条条件边可以有不同的激活状态
    - 当所有分支都被剪枝时，汇聚点及更下游也被连带剪枝
    - 全部条件为 true 时完全等价于无条件执行（向后兼容）
```

**双源汇聚（两个独立起点各自有条件分支，最终汇聚）**：

```
  source_A ──┬── process_A1 ──────────────────────┐
             │                                      │
             └── process_A2 ──(cond: cond_a2)──┐   │
                                              │   │
                                              ├── final
                                              │
  source_B ──┬── process_B1 ──────────────────┘
             │
             └── process_B2 ──(cond: cond_b2)──┘

  注意: source_A 和 source_B 都是入度=0的起点

  假设 state: cond_a2=false, cond_b2=true

  可达性分析：
    1. source_A (入度=0) → 可达 ✅
    2. source_B (入度=0) → 可达 ✅
    3. source_A → process_A1 (无条件) → process_A1 可达 ✅
    4. source_A → process_A2 (cond=false) → process_A2 不可达 ❌
    5. source_B → process_B1 (无条件) → process_B1 可达 ✅
    6. source_B → process_B2 (cond=true) → process_B2 可达 ✅
    7. process_A1 → final → final 可达 ✅
    8. process_A2 → final (来源不可达，但 final 已可达)
    9. process_B1 → final (已可达)
    10. process_B2 → final (已可达)

  执行结果：
    Level 0: source_A, source_B → 并行执行
    Level 1: process_A1, process_B1, process_B2 → 并行执行
             process_A2 → 跳过(写默认值)
    Level 2: final → 执行(读到 A1实际 + A2默认 + B1实际 + B2实际)

  ★ 关键点：
    - 多起点 DAG 的每个起点都独立可达
    - 不同起点的条件边互不影响
    - final 汇聚节点只需至少一条活跃入边即可到达
```

**条件边的下游又扇出到多个节点（剪枝级联扇出）**：

```
  entry ──→ pre_check ──┬── deep_scan ──(cond: needs_deep)──┬── enrich ──→ report
                         │                                   │
                         └── quick_scan ─────────────────────┘

  但 deep_scan 还有自己的扇出:
  deep_scan ──┬── vulnerability_analysis ──→ vul_merge
               │                                    ↑
               └── license_check ───────────────────┘

  完整 DAG:
                                  ┌── vuln_analysis ──┐
  entry → pre_check → deep_scan ─┤                    ├── vul_merge ──┐
                      (cond)       └── license_check ─┘                │
                                                                         │
              pre_check → quick_scan ────────────────→ enrich ────────→ report

  假设 state["needs_deep"] = false

  可达性分析：
    1. entry → 可达 ✅
    2. pre_check → 可达 ✅
    3. deep_scan (cond=false) → 不可达 ❌
    4. quick_scan → 可达 ✅
    5. vuln_analysis (唯一入边来自 deep_scan) → 不可达 ❌
    6. license_check (唯一入边来自 deep_scan) → 不可达 ❌
    7. vul_merge (两条入边都不活跃) → 不可达 ❌
    8. enrich (来自 quick_scan，可达) → 可达 ✅
    9. report (来自 enrich 和 vul_merge，enrich 活跃) → 可达 ✅

  执行结果：
    Level 0: entry → 执行
    Level 1: pre_check → 执行
    Level 2: quick_scan → 执行; deep_scan → 跳过(写默认值)
    Level 3: vuln_analysis → 跳过; license_check → 跳过
    Level 4: vul_merge → 跳过(写默认值); enrich → 执行
    Level 5: report → 执行(读到 enrich_result + vul_merge默认值)

  ★ 关键点：
    - 条件边不激活导致整棵子树 (deep_scan → vuln_analysis, license_check → vul_merge) 被剪枝
    - 剪枝的级联深度不限，沿 DAG 向下传播直到遇到有其他活跃入边的节点
    - report 虽然有来自 vul_merge 的入边（不活跃），但 enrich 的入边活跃，所以 report 仍可达
```

**同一汇聚节点的部分条件入边活跃、部分不活跃**：

```
  source ──┬── branch_A ──(cond: enable_a)──┐
            │                                 │
            ├── branch_B ──(cond: enable_b)──┼── merge ──→ output
            │                                 │
            └── branch_C ──(cond: enable_c)──┘

  场景: enable_a=true, enable_b=false, enable_c=true

  可达性分析：
    1. source → 可达 ✅
    2. source → branch_A (cond=true) → branch_A 可达 ✅
    3. source → branch_B (cond=false) → branch_B 不可达 ❌
    4. source → branch_C (cond=true) → branch_C 可达 ✅
    5. branch_A → merge → merge 可达 ✅
    6. branch_B → merge (来源不可达，但 merge 已可达)
    7. branch_C → merge (已可达)
    8. merge → output → output 可达 ✅

  merge 的状态：
    - 有效入度 = 2 (from branch_A and branch_C)
    - branch_B 的入边不活跃，不计入
    - merge 读取: branch_A_result (实际) + branch_B_result (默认值) + branch_C_result (实际)

  ★ 关键点：
    - 汇聚节点无需所有入边都活跃，只需至少一条
    - 不活跃入边对应的 upstream 的 SkipOutput 会填充默认值
    - merge 节点需要能处理部分真实数据+部分默认值的混合输入
```

### 3.4 算法

```go
// ActiveBlocks 使用 BFS 计算可达性
//
// 算法步骤：
//  1. 初始化：所有入度为0的节点入队，标记为可达
//  2. BFS 遍历：
//     a. 取出队首节点 u（已确认可达）
//     b. 遍历 u 的所有出边 e = (u → v)
//     c. 若 e 不活跃（有条件且条件不满足），跳过
//     d. 若 e 活跃，记录 v 有一条活跃入边
//     e. 若 v 的活跃入边数 == v 的原始入度，说明 v 的所有入边都已处理且至少一条活跃
//        → v 可达，入队
//  3. 返回可达集合
```

**注意**：更精确的算法应该是——当节点 v 的**至少一条**活跃入边来源可达时，v 就可达。因为 DAG 是层级的，上层节点先被处理，所以当处理到 v 时，v 的所有上游要么已标记可达，要么已确认不可达。

修正算法：

```go
ActiveBlocks(evaluator):
  reachable = {}
  queue = [所有入度为0的block]  // Level 0 节点

  for block in queue:
    reachable[block.ID] = true
    for each edge (block → downstream):
      if edge has condition AND !evaluator.IsActive(edge.Condition.StateKey):
        continue  // 边不活跃，跳过
      // 边活跃，检查 downstream 是否至少有一条活跃入边的来源可达
      if downstream 的所有入边都已处理:
        if downstream 至少有一条活跃入边的来源在 reachable 中:
          reachable[downstream.ID] = true
          add downstream to queue

  return reachable
```

由于是层级 BFS，可以用更简单的实现：

```go
ActiveBlocks(evaluator):
  reachable = {}
  activeInEdgesCount = {}  // blockID → 活跃入边数

  // 初始化：入度为0的节点
  queue = [所有入度为0的block]
  for block in queue:
    reachable[block.ID] = true

  while queue not empty:
    nextQueue = []
    for block in queue:
      for each edge (block → downstream):
        if edge has condition AND !evaluator.IsActive(edge.Condition.StateKey):
          continue  // 边不活跃
        activeInEdgesCount[downstream]++
        // 当 downstream 的活跃入边数 == 原始入度时，
        // 说明所有上游都已处理完，且至少有一条活跃边到达
        // （因为我们只在边活跃时才计数）
        // 但这不对——有些入边可能来自不可达的节点...
    // 需要更仔细的处理

  return reachable
```

实际上最简洁正确的算法是：

```go
ActiveBlocks(evaluator):
  reachable = {}

  // 从入度为0的节点开始 BFS
  queue = [所有入度为0的block]
  for block in queue:
    reachable[block.ID] = true

  while queue not empty:
    nextQueue = []
    for block in queue:
      for each edge (block → downstream):
        // 只沿活跃边传播
        if edge has condition AND !evaluator.IsActive(edge.Condition.StateKey):
          continue
        if !reachable[downstream.ID]:
          reachable[downstream.ID] = true
          nextQueue.append(downstream)
    queue = nextQueue

  return reachable
```

这个算法的含义是：**从起点出发，只沿活跃边做 BFS，能到达的节点就是可达的**。

这自然处理了传递性剪枝：
- 条件边不活跃 → 不沿该边传播 → 下游节点不在 reachable 中
- 下游节点不在 reachable 中 → 它的出边也不会被遍历 → 更下游也不可达

---

## 4. Schema 设计

### 4.1 Edge 增加 Condition

```go
// schema.go

// Edge 定义两个 Block 之间的有向数据流。
type Edge struct {
    SourceID  string          `json:"sourceId"`
    TargetID  string          `json:"targetId"`

    // Condition 定义边的激活条件。当条件不满足时，该边不激活，
    // 目标节点可能变为不可达（如果所有入边都不活跃）。
    // omitempty 表示无条件边始终激活（向后兼容）。
    Condition *EdgeCondition  `json:"condition,omitempty"`
}

// EdgeCondition 定义边激活条件。
// 当前仅支持 StateKey 模式（引用 session state 中的布尔值），
// 未来可扩展 Expression 模式。
type EdgeCondition struct {
    // StateKey 引用 session state 中的布尔值。
    // 当 state[StateKey] 为 true 时边激活；false/"false"/"0"/"no" 时不激活。
    // 必须非空。
    StateKey string `json:"stateKey"`
}
```

### 4.2 Block 增加 SkipOutput

```go
// schema.go

type Block struct {
    // ... 现有字段（ID, Name, Type, Description, OutputKey, InputKeys）...

    // SkipOutput 声明节点因条件边不激活而不可达时的默认输出。
    // 当节点被剪枝时，SkipOutput 会被写入 OutputKey 对应的 session state，
    // 确保归并点有数据可读。
    //
    // 约束：
    //   - SkipOutput 非空时，OutputKey 也必须非空
    //   - SkipOutput 通常是 JSON 字符串
    //
    // 示例：
    //   skipOutput: "{\"status\":\"auto_approved\",\"risk_level\":\"none\"}"
    SkipOutput string `json:"skipOutput,omitempty"`
}
```

### 4.3 完整 JSON 示例

```json
{
  "$schema": "https://undertreetech.github.io/adk-go/orchestration/v2",
  "version": "2",
  "metadata": {
    "name": "OrderProcessingPipeline",
    "description": "订单处理：分类 → 并行[支付 + 条件风控] → 合并完成"
  },
  "blocks": [
    {
      "id": "start",
      "name": "Start",
      "type": "start",
      "outputKey": "user_input"
    },
    {
      "id": "classify",
      "name": "ClassifyOrder",
      "type": "agent",
      "outputKey": "order_classification",
      "inputKeys": ["user_input"]
    },
    {
      "id": "payment",
      "name": "PaymentProcess",
      "type": "agent",
      "outputKey": "payment_result",
      "inputKeys": ["order_classification"]
    },
    {
      "id": "risk_check",
      "name": "RiskCheck",
      "type": "agent",
      "outputKey": "risk_result",
      "inputKeys": ["order_classification"],
      "skipOutput": "{\"status\":\"auto_approved\",\"risk_level\":\"none\",\"message\":\"小额订单自动通过风控\"}"
    },
    {
      "id": "merge",
      "name": "MergeAndComplete",
      "type": "agent",
      "outputKey": "final_report",
      "inputKeys": ["payment_result", "risk_result"]
    },
    {
      "id": "end",
      "name": "End",
      "type": "end"
    }
  ],
  "edges": [
    {"sourceId": "start",     "targetId": "classify"},
    {"sourceId": "classify",  "targetId": "payment"},
    {
      "sourceId": "classify",
      "targetId": "risk_check",
      "condition": {"stateKey": "needs_risk_check"}
    },
    {"sourceId": "payment",    "targetId": "merge"},
    {"sourceId": "risk_check", "targetId": "merge"},
    {"sourceId": "merge",      "targetId": "end"}
  ]
}
```

**语义解读**：
- `classify → payment`：无条件，始终执行
- `classify → risk_check`：条件边，仅当 `state["needs_risk_check"] == true` 时激活
- 当条件不满足：`risk_check` 不可达 → 不执行 → `risk_result` 写入 SkipOutput 默认值
- `merge` 只需等待 `payment` 完成（`risk_check` 被剪枝）

---

## 5. DAG 层设计

### 5.1 条件评估接口

```go
// executor/dag.go

// ConditionEvaluator 评估边条件是否激活。
// 由执行层提供实现，桥接 session.State。
type ConditionEvaluator interface {
    // IsActive 返回给定 stateKey 对应的条件是否为 true。
    IsActive(stateKey string) bool
}

// AlwaysActiveEvaluator 是所有边始终激活的评估器（用于无条件边的场景）。
type AlwaysActiveEvaluator struct{}

func (e *AlwaysActiveEvaluator) IsActive(stateKey string) bool { return true }
```

### 5.2 可达性分析方法

```go
// ActiveBlocks 计算在给定条件评估下，哪些 Block 是可达的。
//
// 使用 BFS 从入度为0的节点出发，只沿活跃边传播可达性。
// 不可达的节点不会被启动执行，其 SkipOutput 会被写入 session state。
//
// 当 evaluator 为 nil 时，所有边视为活跃（退化为无条件的全量可达）。
func (d *DAG) ActiveBlocks(evaluator ConditionEvaluator) map[string]bool {
    if evaluator == nil {
        evaluator = &AlwaysActiveEvaluator{}
    }

    reachable := make(map[string]bool)
    
    // 初始化：入度为0的节点入队
    var queue []string
    for id, deg := range d.inDegree {
        if deg == 0 {
            reachable[id] = true
            queue = append(queue, id)
        }
    }
    sort.Strings(queue) // 确定性顺序

    // BFS 传播
    for len(queue) > 0 {
        var nextQueue []string
        for _, id := range queue {
            for _, downstream := range d.adjacency[id] {
                if reachable[downstream] {
                    continue // 已标记可达
                }
                // 检查边 (id → downstream) 是否活跃
                edge := d.findEdge(id, downstream)
                if edge.Condition != nil && !evaluator.IsActive(edge.Condition.StateKey) {
                    continue // 边不活跃，不传播
                }
                // 边活跃，标记下游可达
                reachable[downstream] = true
                nextQueue = append(nextQueue, downstream)
            }
        }
        sort.Strings(nextQueue)
        queue = nextQueue
    }

    return reachable
}

// findEdge 查找两个 Block 之间的边。
func (d *DAG) findEdge(sourceID, targetID string) *flow.Edge {
    for i := range d.edges {
        if d.edges[i].SourceID == sourceID && d.edges[i].TargetID == targetID {
            return &d.edges[i]
        }
    }
    return nil
}

// ActiveInDegree 计算在给定条件评估下，某节点的有效入度。
func (d *DAG) ActiveInDegree(blockID string, evaluator ConditionEvaluator) int {
    if evaluator == nil {
        return d.inDegree[blockID]
    }
    count := 0
    for _, upstream := range d.reverse[blockID] {
        edge := d.findEdge(upstream, blockID)
        if edge.Condition == nil || evaluator.IsActive(edge.Condition.StateKey) {
            count++
        }
    }
    return count
}
```

---

## 6. Executor 层设计

### 6.1 FlowDAGAgent

```go
// executor/flow_agent.go

// FlowDAGAgent 实现了 adkagent.Agent 接口，
// 按拓扑层级执行 DAG，每层执行前评估边条件并剪枝不可达节点。
//
// 与 SequentialAgent + ParallelAgent 的区别：
//   - 每层执行前评估 Edge.Condition
//   - 不可达的 Block 不执行，其 SkipOutput 写入 session state
//   - 归并点只等待可达的上游分支
type FlowDAGAgent struct {
    dag      *DAG
    provider provider.AgentProvider
    schema   *flow.FlowSchema
    name     string

    // agentCache 缓存已解析的 Agent 实例
    agentCache map[string]adkagent.Agent
    mu         sync.RWMutex
}
```

### 6.2 Run 方法

```go
func (a *FlowDAGAgent) Run(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
    return func(yield func(*session.Event, error) bool) {
        levels := a.dag.Levels()

        for _, level := range levels {
            // 1. 从 session state 构建条件评估器
            evaluator := &sessionStateEvaluator{state: ctx.Session().State()}

            // 2. 计算可达性
            activeBlocks := a.dag.ActiveBlocks(evaluator)

            // 3. 分类：当前层级的活跃节点 vs 跳过节点
            var activeBlocks []flow.Block
            var skippedBlocks []flow.Block
            for _, block := range level {
                if activeBlocks[block.ID] {
                    activeBlocks = append(activeBlocks, block)
                } else {
                    skippedBlocks = append(skippedBlocks, block)
                }
            }

            // 4. 为被跳过的节点写入默认值到 session state
            for _, block := range skippedBlocks {
                if block.OutputKey != "" && block.SkipOutput != "" {
                    _ = ctx.Session().State().Set(block.OutputKey, block.SkipOutput)
                }
            }

            // 5. 执行活跃节点
            switch len(activeBlocks) {
            case 0:
                // 当前层所有节点都被剪枝，继续下一层
                continue
            case 1:
                // 单个节点直接执行
                ag := a.resolveAgent(activeBlocks[0])
                for event, err := range ag.Run(ctx) {
                    if !yield(event, err) { return }
                }
            default:
                // 多个活跃节点并行执行
                for event, err := range a.runParallel(ctx, activeBlocks) {
                    if !yield(event, err) { return }
                }
            }
        }
    }
}
```

### 6.3 并行执行

```go
// runParallel 并行执行多个活跃节点。
// 复用 ADK ParallelAgent 的并发模式（errgroup + isolated branch）。
func (a *FlowDAGAgent) runParallel(ctx adkagent.InvocationContext, blocks []flow.Block) iter.Seq2[*session.Event, error] {
    return func(yield func(*session.Event, error) bool) {
        // 构建并行 Agent
        var subAgents []adkagent.Agent
        for _, block := range blocks {
            subAgents = append(subAgents, a.resolveAgent(block))
        }

        parallelCfg := agent.Config{
            Config: adkagent.Config{
                Name:      fmt.Sprintf("Level_Parallel_%s", blocks[0].ID),
                SubAgents: subAgents,
            },
            DisableDefaultCallbacks: true,
        }
        parallelAgent, err := agent.NewParallelAgent(parallelCfg)
        if err != nil {
            yield(nil, fmt.Errorf("create parallel agent: %w", err))
            return
        }

        for event, err := range parallelAgent.Run(ctx) {
            if !yield(event, err) { return }
        }
    }
}
```

### 6.4 条件评估器

```go
// sessionStateEvaluator 桥接 session.State 到 ConditionEvaluator
type sessionStateEvaluator struct {
    state session.State
}

func (e *sessionStateEvaluator) IsActive(stateKey string) bool {
    val, err := e.state.Get(stateKey)
    if err != nil {
        return false // key 不存在，默认不激活
    }
    switch v := val.(type) {
    case bool:
        return v
    case string:
        return !(v == "false" || v == "no" || v == "0")
    default:
        return false
    }
}
```

---

## 7. Build 函数变更

### 7.1 条件边检测

```go
// executor/agent.go

func Build(schema *flow.FlowSchema, cfg BuildConfig) (adkagent.Agent, error) {
    if cfg.Provider == nil {
        return nil, fmt.Errorf("orchestration/flow/executor: BuildConfig.Provider is required")
    }

    dag, err := NewDAG(schema.Blocks, schema.Edges)
    if err != nil {
        return nil, fmt.Errorf("orchestration/flow/executor: build DAG: %w", err)
    }

    // 如果有任何条件边，使用 FlowDAGAgent（支持运行时剪枝）
    if hasConditionalEdges(schema) {
        return buildFlowDAGAgent(schema, dag, cfg)
    }

    // 否则保持原有的静态构建（零影响）
    return buildStatic(dag, schema, cfg)
}

func hasConditionalEdges(schema *flow.FlowSchema) bool {
    for _, edge := range schema.Edges {
        if edge.Condition != nil {
            return true
        }
    }
    return false
}
```

### 7.2 向后兼容保证

- `hasConditionalEdges()` 返回 false → 走 `buildStatic()`（提取自原有 Build 逻辑）
- 所有现有测试无需修改

---

## 8. Parser 校验

### 8.1 EdgeCondition 校验

```go
// parser/parser.go - Validate() 中增加

for i, edge := range schema.Edges {
    path := fmt.Sprintf("edges[%d]", i)
    
    // 现有校验...
    
    // 新增：EdgeCondition 校验
    if edge.Condition != nil {
        condPath := path + ".condition"
        if edge.Condition.StateKey == "" {
            errs.add(condPath+".stateKey", "must be non-empty")
        }
    }
}
```

### 8.2 SkipOutput 校验

```go
// parser/parser.go - Validate() 中增加

for i, block := range schema.Blocks {
    path := fmt.Sprintf("blocks[%d]", i)
    
    // 现有校验...
    
    // 新增：SkipOutput 校验
    if block.SkipOutput != "" {
        if block.OutputKey == "" {
            errs.addf(path+".skipOutput", 
                "block %q has skipOutput but no outputKey", block.ID)
        }
    }
}
```

---

## 9. 执行流程图示

### 9.1 条件满足（边激活）

```
state["needs_risk_check"] = true

Level 0: [start, classify]
  → evaluate: 所有边活跃
  → 执行 start, classify

Level 1: [payment, risk_check]
  → evaluate: edge(classify→payment) 活跃, edge(classify→risk_check) 活跃
  → ActiveBlocks = {start, classify, payment, risk_check}
  → 执行 payment, risk_check (并行)

Level 2: [merge]
  → evaluate: edge(payment→merge) 活跃, edge(risk_check→merge) 活跃
  → ActiveBlocks = {..., merge}
  → 执行 merge

Level 3: [end]
  → 执行 end
```

### 9.2 条件不满足（边不激活，剪枝）

```
state["needs_risk_check"] = false

Level 0: [start, classify]
  → evaluate: edge(classify→risk_check) 不活跃
  → ActiveBlocks = {start, classify, payment}
  → 执行 start, classify

Level 1: [payment, risk_check]
  → evaluate:
    - payment 可达 ✅ → 执行
    - risk_check 不可达 ❌ → 跳过
      → state["risk_result"] = SkipOutput 默认值
  → 只执行 payment

Level 2: [merge]
  → evaluate: edge(payment→merge) 活跃, edge(risk_check→merge) 不活跃(来源不可达)
  → ActiveBlocks = {..., merge}
  → merge 只有一个活跃入边，直接执行
  → merge 读取 state["payment_result"] (实际结果) + state["risk_result"] (默认值)

Level 3: [end]
  → 执行 end
```

### 9.3 多条件边同时不激活

```
DAG: classify → {payment, risk_check(cond:needs_risk), intl_verify(cond:is_intl)} → merge → end

state["needs_risk_check"] = false
state["is_international"] = false

Level 0: [classify]
  → evaluate:
    - edge(classify→payment) 无条件 → 活跃
    - edge(classify→risk_check) cond=false → 不活跃
    - edge(classify→intl_verify) cond=false → 不活跃
  → ActiveBlocks = {classify, payment}
  → 执行 classify

Level 1: [payment, risk_check, intl_verify]
  → payment 可达 ✅ → 执行
  → risk_check 不可达 ❌ → 跳过 → state["risk_result"] = SkipOutput
  → intl_verify 不可达 ❌ → 跳过 → state["intl_result"] = SkipOutput
  → 只执行 payment

Level 2: [merge]
  → merge 可达 (via payment) ✅
  → 执行 merge (读到 payment_result + risk默认值 + intl默认值)

Level 3: [end]
  → 执行 end
```

### 9.4 菱形嵌套：分支内部的条件边

```
DAG:
  start → {legal, tech_eval} (并行)
  legal → {doc_review, compliance(cond:is_regulated)} (legal内部菱形)
  doc_review → legal_merge, compliance → legal_merge
  legal_merge → final_merge, tech_eval → final_merge
  final_merge → end

state["is_regulated"] = false

Level 0: [start]
  → 执行 start

Level 1: [legal, tech_eval]
  → 两个都可达 → 并行执行

Level 2: [doc_review, compliance]
  → doc_review 可达 ✅ → 执行
  → compliance 不可达 ❌ → 跳过 → state["compliance_result"] = SkipOutput

Level 3: [legal_merge]
  → legal_merge 可达 (via doc_review) ✅
  → 执行 legal_merge (读到 doc_review_result + compliance默认值)

Level 4: [final_merge]
  → final_merge 可达 (via legal_merge and tech_eval) ✅
  → 执行 final_merge

Level 5: [end]
  → 执行 end

★ 分支 legal 内部的条件边只剪枝了 compliance，不影响 legal 本身的可达性
```

### 9.5 全分支剪枝导致汇聚点连带剪枝

```
DAG: gate → {feature_A(cond:enable_a), feature_B(cond:enable_b), feature_C(cond:enable_c)}
     feature_A → aggregate, feature_B → aggregate, feature_C → aggregate
     aggregate → output

state: enable_a=false, enable_b=false, enable_c=false

Level 0: [gate]
  → evaluate: 三条条件边都不活跃
  → ActiveBlocks = {gate}
  → 执行 gate

Level 1: [feature_A, feature_B, feature_C]
  → 三个都不可达 ❌ → 全部跳过
  → state["feature_a_result"] = SkipOutput_A
  → state["feature_b_result"] = SkipOutput_B
  → state["feature_c_result"] = SkipOutput_C

Level 2: [aggregate]
  → aggregate 的所有入边都不活跃 → 不可达 ❌ → 跳过
  → state["aggregate_result"] = SkipOutput_aggregate

Level 3: [output]
  → output 的唯一入边(aggregate→output)来源不可达 → 不可达 ❌ → 跳过
  → state["output_result"] = SkipOutput_output

★ 关键点：当所有分支都被剪枝，汇聚点及更下游全部连带剪枝
★ 这确保了不会出现"汇聚点等待永远不会到来的上游"的死等问题
```

### 9.6 剪枝级联扇出（条件节点的整棵子树被剪枝）

```
DAG:
  entry → pre_check → {deep_scan(cond:needs_deep), quick_scan}
  deep_scan → {vuln_analysis, license_check}
  vuln_analysis → vul_merge, license_check → vul_merge
  quick_scan → enrich
  enrich → report, vul_merge → report

state["needs_deep"] = false

Level 0: [entry]
  → 执行 entry

Level 1: [pre_check]
  → 执行 pre_check

Level 2: [deep_scan, quick_scan]
  → deep_scan 不可达 ❌ → 跳过 → state["deep_scan_result"] = SkipOutput
  → quick_scan 可达 ✅ → 执行

Level 3: [vuln_analysis, license_check]
  → 两者唯一入边都来自 deep_scan (不可达)
  → 都不可达 ❌ → 跳过
  → state["vuln_result"] = SkipOutput
  → state["license_result"] = SkipOutput

Level 4: [vul_merge, enrich]
  → vul_merge: 两条入边都不活跃 → 不可达 ❌ → 跳过 → state["vul_merge_result"] = SkipOutput
  → enrich: 来自 quick_scan (可达) → 可达 ✅ → 执行

Level 5: [report]
  → report: 来自 enrich (活跃) 和 vul_merge (不活跃)，至少一条活跃 → 可达 ✅
  → 执行 report (读到 enrich_result + vul_merge默认值)

★ 关键点：deep_scan 被剪枝后，其整棵子树 (vuln_analysis, license_check, vul_merge) 全部级联剪枝
★ report 因有来自 enrich 的另一条活跃路径而仍然可达
```

| 文件 | 变更 | 说明 |
|------|------|------|
| `flow/schema.go` | 修改 | 新增 `EdgeCondition`, `Edge.Condition`, `Block.SkipOutput` |
| `flow/schema_test.go` | 修改 | 新增 EdgeCondition/SkipOutput JSON 序列化测试 |
| `flow/executor/dag.go` | 修改 | 新增 `ConditionEvaluator`, `ActiveBlocks()`, `ActiveInDegree()`, `findEdge()` |
| `flow/executor/dag_test.go` | 修改 | 新增可达性分析测试 |
| `flow/executor/flow_agent.go` | **新增** | `FlowDAGAgent` 实现 |
| `flow/executor/flow_agent_test.go` | **新增** | FlowDAGAgent 测试 |
| `flow/executor/agent.go` | 修改 | `Build()` 条件边检测, 提取 `buildStatic()` |
| `flow/executor/agent_test.go` | 修改 | 新增条件边构建测试 |
| `flow/parser/parser.go` | 修改 | 新增 EdgeCondition/SkipOutput 校验 |
| `flow/parser/parser_test.go` | 修改 | 新增校验测试 |
| `docs/edge-condition-design.md` | **新增** | 本文档 |

---

## 11. 测试用例

### 11.1 DAG 可达性分析测试

```go
func TestDAGActiveBlocksDiamondWithCondition(t *testing.T) {
    // 菱形: classify → {payment, risk_check(condition)} → merge
    // 当 needs_risk_check = false 时，risk_check 不可达
}

func TestDAGActiveBlocksTransitiveSkip(t *testing.T) {
    // 链式: A → B(condition) → C → D
    // 当条件不满足时，B、C、D 都不可达
}

func TestDAGActiveBlocksPartialMerge(t *testing.T) {
    // 归并点有多条入边，部分不活跃
    // A → C, B(condition) → C
    // 当条件不满足时，B 不可达但 C 仍可达（通过 A）
}

func TestDAGActiveBlocksNoCondition(t *testing.T) {
    // 无条件边 → 所有节点可达（与现有行为一致）
}
```

### 11.2 FlowDAGAgent 构建测试

```go
func TestBuildDiamondWithConditionEdge(t *testing.T) {
    // 有条件边的菱形 → 返回 FlowDAGAgent
}

func TestBuildDiamondWithoutConditionEdge(t *testing.T) {
    // 无条件边的菱形 → 返回 SequentialAgent（原有行为）
}
```

### 11.3 Parser 校验测试

```go
func TestValidateEdgeConditionStateKeyRequired(t *testing.T) {
    // EdgeCondition.StateKey 为空 → 校验失败
}

func TestValidateSkipOutputRequiresOutputKey(t *testing.T) {
    // Block 有 SkipOutput 但无 OutputKey → 校验失败
}
```

### 11.4 向后兼容测试

所有现有测试不变：
- `TestBuildLinearChain`
- `TestBuildDiamond`
- `TestBuildGovServiceFlow`
- `TestDAGLinearChain`
- `TestDAGDiamond`
- `TestDAGGovServiceFlow`

---

## 12. 未来扩展

1. **Expression 条件**：`EdgeCondition.Expression` 支持表达式求值（如 `amount >= 10000`）
2. **Join 策略**：归并点配置 `all/any/quorum/dynamic` 策略，主动决策等待逻辑
3. **条件边追踪**：在 trace 中记录边条件评估结果，增强可观测性
4. **动态 DAG executor**：完全事件驱动的 DAG 执行引擎，替代 level-by-level 模型
