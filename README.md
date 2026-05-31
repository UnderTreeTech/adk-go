# adk-go

[![Go](https://img.shields.io/badge/Go-1.25+-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache%202.0-green.svg)](LICENSE)

**adk-go** 是基于 [Google ADK (Agent Development Kit)](https://google.golang.org/adk) 的增强扩展库，为构建生产级 AI Agent 应用提供开箱即用的多模型接入、可观测性、会话管理、记忆持久化与上下文窗口管理能力。

## ✨ 核心特性

- **多模型适配** — 内置 OpenAI 兼容协议与 Anthropic Claude 的 LLM 适配器，支持 DeepSeek、Ollama 等任意 OpenAI 兼容服务
- **统一 Agent 工厂** — 一站式创建 LLMAgent、SequentialAgent、ParallelAgent、LoopAgent，自动整合插件配置
- **上下文窗口守护 (ContextGuard)** — 自动防止对话超出模型上下文窗口，支持滑动窗口与 Token 阈值两种压缩策略
- **可观测性插件** — 集成 Jaeger 与 Langfuse 的 OpenTelemetry 链路追踪，记录 LLM 请求/响应、Token 用量
- **会话持久化** — Redis 会话服务实现，支持 App/User/Session 三级状态隔离与 TTL 过期管理
- **长期记忆** — PostgreSQL + pgvector 向量记忆服务，支持语义搜索与全文检索双模式
- **实用工具集** — 文件生成工具、记忆 CRUD 工具集、安全 Toolset 包装器
- **Artifact 存储** — 支持本地磁盘与 S3 兼容对象存储的文件产物管理

## 📦 项目结构

```
adk-go/
├── agent/                     # 统一 Agent 工厂（LLM/Sequential/Parallel/Loop）
├── artifact/                  # Artifact 存储配置与服务
│   ├── diskstorage/           # 本地磁盘存储实现
│   └── s3/                    # S3 兼容对象存储实现
├── genai/                     # LLM 模型适配器
│   ├── openai/                # OpenAI 兼容协议适配器
│   └── anthropic/             # Anthropic Claude SDK 适配器
├── plugin/                    # ADK 插件
│   ├── contextguard/          # 上下文窗口管理插件
│   └── trace/                 # 链路追踪基础框架
│       ├── jaeger/            # Jaeger 集成
│       └── langfuse/          # Langfuse 集成
├── session/                   # 会话服务
│   ├── redis/                 # Redis 会话持久化
│   └── mongo/                 # MongoDB 会话持久化
├── memory/                    # 长期记忆服务
│   └── postgres/              # PostgreSQL + pgvector 实现
├── tools/                     # 实用工具
│   ├── memory/                # 记忆 CRUD 工具集
│   ├── filegentool/           # 文件生成工具
│   ├── safetoolset/           # 安全 Toolset 包装器
│   └── appendfiletool/        # 文件追加工具
├── prompt/                    # 全局提示词模板
├── examples/                  # 示例应用
│   ├── todo-agent/            # Todo Agent 完整示例
│   ├── anthropic-client/      # Anthropic 客户端示例
│   ├── openai-client/         # OpenAI 客户端示例
│   ├── context-guard/         # ContextGuard 示例
│   ├── session-memory/        # 会话记忆示例
│   ├── long-term-memory/      # 长期记忆示例
│   ├── full-memory/           # 完整记忆系统示例
│   ├── llmagent-jaeger/       # Jaeger 追踪示例
│   ├── llmagent-langfuse/     # Langfuse 追踪示例
│   └── agents/                # 多 Agent 编排示例
│       └── sequentialagent/   # 顺序执行 Agent 示例
└── docs/                      # 设计文档
```

## 🚀 快速开始

### 安装

```bash
go get github.com/UnderTreeTech/adk-go
```

### 基本用法 — 创建 LLM Agent

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/UnderTreeTech/adk-go/agent"
    "github.com/UnderTreeTech/adk-go/genai/openai"
    adkAgent "google.golang.org/adk/agent"
    "google.golang.org/adk/agent/llmagent"
    "google.golang.org/adk/cmd/launcher"
    "google.golang.org/adk/cmd/launcher/full"
)

func main() {
    ctx := context.Background()

    // 1. 创建 LLM 模型
    llm := openai.New(&openai.Config{
        APIKey:    os.Getenv("OPENAI_API_KEY"),
        BaseURL:   os.Getenv("OPENAI_BASE_URL"),
        ModelName: "gpt-4o",
    })

    // 2. 创建 Agent（自动集成插件）
    a, err := agent.NewLLMAgent(agent.Config{
        LLMAgentConfig: llmagent.Config{
            Name:        "my_agent",
            Model:       llm,
            Description: "A helpful assistant",
            Instruction: "You are a helpful assistant.",
        },
        ContextGuard: &agent.ContextGuardConfig{
            Strategy: agent.StrategySlidingWindow,
            MaxTurns: 30,
        },
    })
    if err != nil {
        panic(err)
    }

    // 3. 启动
    l := full.NewLauncher()
    config := &launcher.Config{
        AgentLoader:  adkAgent.NewSingleLoader(a.Agent),
        PluginConfig: a.PluginConfig,
    }
    if err = l.Execute(ctx, config, os.Args[1:]); err != nil {
        fmt.Printf("Run failed: %v\n", err)
    }
}
```

## 🔌 多模型支持

### OpenAI 兼容协议

支持任何 OpenAI Chat Completions API 兼容的服务（OpenAI、DeepSeek、Ollama、vLLM 等）：

```go
llm := openai.New(&openai.Config{
    APIKey:    "your-api-key",
    BaseURL:   "https://api.deepseek.com",
    ModelName: "deepseek-v4",
    ExtraBody: map[string]any{
        "extra_body": map[string]any{
            "thinking": map[string]any{"type": "disabled"},
        },
    },
})
```

特性：
- 流式/非流式响应
- 函数调用 (Function Calling)
- 多模态输入（图片、视频、音频、PDF）
- 推理内容 (Reasoning Content) 透传
- Token 用量统计
- 自定义 HTTP 客户端

### Anthropic Claude

使用官方 Anthropic SDK，支持完整的 Claude API 能力：

```go
model := anthropic.New(anthropic.Config{
    APIKey:               "your-api-key",
    ModelName:            "claude-sonnet-4-5-20250929",
    MaxOutputTokens:      8192,
    ThinkingBudgetTokens: 4096, // 启用 Extended Thinking
})
```

特性：
- 流式/非流式响应
- 函数调用
- 图片、PDF、文本文档输入
- Extended Thinking 支持
- 消息历史自动修复（孤立 tool_use 清理）

## 🛡️ ContextGuard — 上下文窗口管理

ContextGuard 插件在每次模型调用前自动检测并压缩对话历史，防止超出上下文窗口限制。

### 滑动窗口策略

当对话轮次超过阈值时触发压缩：

```go
agent.Config{
    ContextGuard: &agent.ContextGuardConfig{
        Strategy: agent.StrategySlidingWindow,
        MaxTurns: 30, // 超过 30 轮时压缩
    },
}
```

### Token 阈值策略

当估算 Token 数接近模型上下文窗口时触发压缩：

```go
agent.Config{
    ContextGuard: &agent.ContextGuardConfig{
        Strategy:  agent.StrategyThreshold,
        MaxTokens: 128000, // 可选，默认从 ModelRegistry 查询
    },
}
```

## 📡 可观测性

### Jaeger 集成

```go
import "github.com/UnderTreeTech/adk-go/plugin/trace/jaeger"

jaegerCfg, shutdown, err := jaeger.Setup(&jaeger.Config{
    Endpoint:    "http://localhost:4318/v1/traces",
    ServiceName: "my-agent",
    Environment: "production",
    Insecure:    true,
})
defer shutdown(ctx)

a, _ := agent.NewLLMAgent(agent.Config{
    // ...
    JaegerPluginConfig: &jaegerCfg,
})
```

### Langfuse 集成

```go
import "github.com/UnderTreeTech/adk-go/plugin/trace/langfuse"

langfuseCfg, shutdown, err := langfuse.Setup(&langfuse.Config{
    PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
    SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
    Host:      "https://cloud.langfuse.com",
})
defer shutdown(ctx)

a, _ := agent.NewLLMAgent(agent.Config{
    // ...
    LangfusePluginConfig: &langfuseCfg,
})
```

两种追踪后端均自动捕获：
- Agent 输入/输出
- LLM 请求/响应完整内容
- 模型名称与 Token 用量
- 工具调用参数与结果
- 多 Agent 编排的完整调用链

## 💾 会话管理

### Redis 会话服务

```go
import "github.com/UnderTreeTech/adk-go/session/redis"

sessionSvc, err := redis.NewRedisSessionService(redis.RedisSessionServiceConfig{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,
    TTL:      24 * time.Hour,
})
```

特性：
- App / User / Session 三级状态隔离
- 独立 TTL 配置
- 事件持久化与过滤查询
- `temp:` 前缀的临时状态自动清理

## 🧠 长期记忆

### PostgreSQL + pgvector

```go
import "github.com/UnderTreeTech/adk-go/memory/postgres"

memorySvc, err := postgres.NewPostgresMemoryService(ctx, postgres.PostgresMemoryServiceConfig{
    ConnString:     "postgres://user:pass@localhost:5432/mydb?sslmode=disable",
    EmbeddingModel: myEmbeddingModel, // 可选，支持语义搜索
})
```

### 记忆工具集

将记忆能力作为工具暴露给 Agent：

```go
import memtools "github.com/UnderTreeTech/adk-go/tools/memory"

toolset, _ := memtools.NewToolset(memtools.ToolsetConfig{
    MemoryService: memorySvc,
    AppName:       "my-app",
})

// 包含以下工具：
// - search_memory:    搜索长期记忆
// - save_to_memory:   保存信息到长期记忆
// - update_memory:    更新记忆条目
// - delete_memory:    删除记忆条目
```

## 🤖 多 Agent 编排

### 顺序执行 Agent

```go
a, _ := agent.NewSequentialAgent(agent.Config{
    Config: adkAgent.Config{
        Name:      "pipeline",
        SubAgents: []adkAgent.Agent{agentA, agentB, agentC},
    },
})
```

### 并行执行 Agent

```go
a, _ := agent.NewParallelAgent(agent.Config{
    Config: adkAgent.Config{
        Name:      "parallel_pipeline",
        SubAgents: []adkAgent.Agent{agentA, agentB},
    },
})
```

### 循环执行 Agent

```go
a, _ := agent.NewLoopAgent(agent.Config{
    Config: adkAgent.Config{
        Name:      "iterative_pipeline",
        SubAgents: []adkAgent.Agent{agentA, agentB},
    },
    MaxIterations: 5,
})
```

## 📂 示例

| 示例 | 说明 |
|------|------|
| [`examples/todo-agent`](examples/todo-agent/main.go) | Todo Agent — 完整的任务规划与执行示例 |
| [`examples/openai-client`](examples/openai-client/main.go) | OpenAI 兼容客户端基础用法 |
| [`examples/anthropic-client`](examples/anthropic-client/main.go) | Anthropic Claude 基础用法 |
| [`examples/context-guard`](examples/context-guard/main.go) | ContextGuard 上下文窗口管理 |
| [`examples/session-memory`](examples/session-memory/main.go) | Redis 会话记忆 |
| [`examples/long-term-memory`](examples/long-term-memory/main.go) | PostgreSQL 长期记忆 |
| [`examples/full-memory`](examples/full-memory/main.go) | 完整记忆系统（会话 + 长期） |
| [`examples/llmagent-jaeger`](examples/llmagent-jaeger/main.go) | Jaeger 链路追踪 |
| [`examples/llmagent-langfuse`](examples/llmagent-langfuse/main.go) | Langfuse 追踪与评估 |
| [`examples/agents/sequentialagent`](examples/agents/sequentialagent/main.go) | 多 Agent 顺序编排 |

## 📚 文档

详细设计文档请参阅 [`docs/`](docs/) 目录：

- [Agent 架构设计](docs/AGENTS.md)
- [ContextGuard 设计](docs/COMPACTION_TOKEN_THRESHOLD_STRATEGY.md)
- [Langfuse 插件](docs/LANGFUSE_PLUGIN.md)
- [状态管理](docs/state.md)

## 🔧 依赖

- **Go 1.25+**
- [google.golang.org/adk](https://google.golang.org/adk) — Google ADK 核心框架
- [google.golang.org/genai](https://google.golang.org/genai) — Google GenAI SDK
- [github.com/anthropics/anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) — Anthropic 官方 Go SDK
- [github.com/redis/go-redis/v9](https://github.com/redis/go-redis) — Redis 客户端
- [github.com/lib/pq](https://github.com/lib/pq) — PostgreSQL 驱动
- [go.opentelemetry.io/otel](https://opentelemetry.io/docs/languages/go/) — OpenTelemetry SDK

## 📄 许可证

本项目基于 [Apache License 2.0](LICENSE) 许可证开源。
