# 并行分支汇聚 + 条件分支 Demo

## 问题描述

在 adk-go 混合编排中，当并行执行的多个分支汇聚到一个节点时，如果其中一个分支是条件分支（只在特定条件下执行），会出现以下问题：

```
        ┌── BranchA ──┐
START ──┤              ├── MergeNode ── END
        └── BranchB ──┘
```

- `BranchA` 始终执行
- `BranchB` 根据条件可能不执行
- `MergeNode` 依赖两个分支的结果

**问题**：如果 `BranchB` 不执行，`MergeNode` 无法获取 `BranchB` 的结果数据，导致：
1. 汇聚节点读取 state 时得到空值
2. LLM 收到不完整的上下文信息
3. 最终输出质量下降或出错

## 解决方案：将条件逻辑下沉到分支内部

核心思想：**让所有分支都执行**，但在分支内部通过 `BeforeAgentCallback` 判断是否真正运行 LLM。

### 工作流程

```
Sequential[
  ClassifyOrder          ← 设置 state: order_classification, needs_risk_check
  Parallel[
    PaymentProcess       ← 始终运行，设置 state: payment_result
    RiskCheck            ← BeforeAgentCallback 检查条件：
                            - needs_risk_check == true  → 正常执行 LLM
                            - needs_risk_check == false → 跳过 LLM，写入默认值
  ]
  MergeAndComplete       ← 读取 payment_result + risk_result，始终有数据
]
```

### 关键代码：`conditionalSkipCallback`

```go
func conditionalSkipCallback(conditionKey, outputKey, defaultValue string) func(adkAgent.CallbackContext) (*genai.Content, error) {
    return func(ctx adkAgent.CallbackContext) (*genai.Content, error) {
        // 1. 从 state 读取条件标志
        val, _ := ctx.ReadonlyState().Get(conditionKey)
        
        // 2. 判断是否需要跳过
        shouldSkip := !val.(bool)
        
        if !shouldSkip {
            return nil, nil  // 正常执行
        }
        
        // 3. 写入默认值到 state（确保下游有数据）
        ctx.State().Set(outputKey, defaultValue)
        
        // 4. 返回非 nil Content → 框架跳过 agent 运行
        return genai.NewContentFromText("[SKIPPED]...", genai.RoleModel), nil
    }
}
```

### 为什么这个方案有效

1. **ParallelAgent 总是启动所有分支** — 不会有"未执行的分支"
2. **BeforeAgentCallback 返回非 nil** — ADK 框架自动跳过 agent 的 LLM 调用
3. **默认值写入 state** — 下游 MergeNode 通过 `{risk_result}` 模板总能读到数据
4. **节省成本** — 条件不满足时不调用 LLM，只是快速返回

## 具体场景：订单处理系统

| Agent | 角色 | 条件 |
|-------|------|------|
| `ClassifyOrder` | 分析订单金额，判断是否需要风控 | 总是执行 |
| `PaymentProcess` | 处理支付流程 | 总是执行 |
| `RiskCheck` | 风控审查（大额订单） | 金额 ≥ 10000 时执行 |
| `MergeAndComplete` | 汇总结果，生成报告 | 总是执行 |

### 大额订单（≥ 10000）

```
ClassifyOrder → needs_risk_check = true
  ├── PaymentProcess → payment_result = {...}
  └── RiskCheck      → risk_result = {"status": "approved", ...}  ← 实际执行 LLM
MergeAndComplete → 读取两个结果，生成完整报告
```

### 小额订单（< 10000）

```
ClassifyOrder → needs_risk_check = false
  ├── PaymentProcess → payment_result = {...}
  └── RiskCheck      → risk_result = {"status": "auto_approved", ...}  ← 跳过 LLM，写入默认值
MergeAndComplete → 读取两个结果，生成完整报告（注明"自动通过"）
```

## 运行

```bash
# 大额订单（触发风控审查）
go run ./examples/parallel-conditional --run "处理订单 #12345，金额 15000 元"

# 小额订单（跳过风控审查）
go run ./examples/parallel-conditional --run "处理订单 #67890，金额 500 元"
```

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `OPENAI_API_KEY` | LLM API Key | - |
| `OPENAI_BASE_URL` | LLM Base URL | `https://api.deepseek.com` |
| `MODEL_NAME` | 模型名称 | `deepseek-v4-pro` |
| `LANGFUSE_HOST` | Langfuse 地址 | `http://10.224.151.246:3000` |

## 其他解决方案对比

| 方案 | 优点 | 缺点 |
|------|------|------|
| **方案1：条件下沉（本 demo）** | 简单、无需修改框架 | 条件分支仍会被"启动" |
| 方案2：重构拓扑 | 彻底避免问题 | 图结构复杂化 |
| 方案3：MergeNode 使用 WaitAny | 灵活 | 需要框架支持 |
| 方案4：Session State 传递标记 | 通用 | MergeNode 需要额外判断逻辑 |

**推荐**：方案1 最适合 adk-go 当前架构，利用了框架原生的 `BeforeAgentCallback` 机制，零侵入、零框架修改。
