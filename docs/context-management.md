# 上下文管理指南

ADK 提供了完整的上下文管理功能，包括 Token 计数、滑动窗口、对话摘要和长期记忆。

## 功能概述

### 1. Token 计数与管理 (`pkg/llm/token.go`)

自动计算消息的 token 数量，并根据限制自动截断。

```go
// 创建计数器
counter := llm.NewTokenCounter("gpt-4o")

// 计算 token 数
tokens := counter.Count(messages)

// 自动适配到限制
fitted := counter.Fit(messages, 8000)
```

**支持的模型：**
- `gpt-3.5`
- `gpt-4`
- `gpt-4o`
- `claude-3`

---

### 2. 滑动窗口策略 (`pkg/memory/sliding.go`)

保留最近的 N 条消息，自动丢弃旧消息。

```go
strategy := memory.NewSlidingWindowStrategy()
strategy.SetKeepLast(20)  // 保留最近 20 条
```

**特点：**
- 始终保留 System 消息
- 可配置保留数量
- 简单高效

---

### 3. 对话摘要 (`pkg/memory/summary.go`)

使用 LLM 生成对话摘要，保留关键信息。

```go
summarizer := memory.NewSummarizer(llmProvider, "gpt-4o")
summary, err := summarizer.Summarize(ctx, messages)
```

**使用场景：**
- 长对话压缩
- 保留关键上下文
- 减少 token 使用

---

### 4. 混合策略 (`pkg/memory/strategy.go`)

结合滑动窗口和对话摘要。

```go
strategy := memory.NewHybridStrategy(10)  // 每 10 条生成摘要
```

---

### 5. 上下文管理器 (`pkg/memory/manager.go`)

统一管理所有上下文功能。

```go
mgr, err := memory.NewManager(memory.Config{
    MaxContextTokens: 8000,
    Strategy:         memory.StrategyHybrid,
    SummaryInterval:  10,
    LLM:              llmProvider,
    ModelName:        "gpt-4o",
    EnableLongTerm:   false,
})

// 准备上下文
prepared, err := mgr.Prepare(ctx, messages, 8000)
```

**配置选项：**

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `MaxContextTokens` | 最大 token 数 | 8000 |
| `Strategy` | 策略类型 | sliding |
| `SummaryInterval` | 摘要间隔 | 10 |
| `LLM` | LLM Provider | - |
| `ModelName` | 模型名称 | gpt-4o |
| `EnableLongTerm` | 启用长期记忆 | false |

**策略类型：**
- `StrategySliding` - 滑动窗口
- `StrategySummary` - 对话摘要
- `StrategyHybrid` - 混合模式

---

### 6. 长期记忆/向量存储 (`internal/storage/postgres/vector.go`)

使用 PostgreSQL pgvector 进行语义搜索。

**数据库迁移：**
```bash
# 需要安装 pgvector 扩展
CREATE EXTENSION vector;
```

**使用示例：**
```go
vectorStore := postgres.NewVectorStore(db)

// 插入向量
err := vectorStore.Insert(ctx, sessionID, messageID, text, vector)

// 语义搜索
results, err := vectorStore.Search(ctx, sessionID, query, 5)
```

---

## 完整使用示例

### 基础配置

```go
package main

import (
    "soasurs.dev/soasurs/adk/pkg/agent"
    "soasurs.dev/soasurs/adk/pkg/memory"
    "soasurs.dev/soasurs/adk/pkg/llm/openai"
)

func main() {
    // 创建 LLM Provider
    llmProvider, _ := openai.NewProvider(openai.ToConfig("your-api-key"))
    
    // 创建上下文管理器
    memoryManager, _ := memory.NewManager(memory.Config{
        MaxContextTokens: 8000,
        Strategy:         memory.StrategyHybrid,
        SummaryInterval:  10,
        LLM:              llmProvider,
        ModelName:        "gpt-4o",
    })
    
    // 创建 Agent
    agentConfig := agent.NewConfig(
        agent.WithLLM(llmProvider),
        agent.WithMaxContextTokens(8000),
        agent.WithContextStrategy("hybrid"),
        agent.WithMemoryManager(memoryManager),
        agent.WithSystemPrompt("You are a helpful assistant."),
    )
    
    agentInstance := agent.NewAgent(agentConfig, store)
    
    // 运行
    result, _ := agentInstance.Run(ctx, sessionID, "Hello!")
}
```

### 高级配置（带向量搜索）

```go
// 启用长期记忆
vectorStore := postgres.NewVectorStore(db)

memoryManager, _ := memory.NewManager(memory.Config{
    MaxContextTokens: 8000,
    Strategy:         memory.StrategyHybrid,
    SummaryInterval:  10,
    LLM:              llmProvider,
    ModelName:        "gpt-4o",
    EnableLongTerm:   true,
    VectorStore:      vectorStore,
})

// 搜索相关记忆
related, _ := memoryManager.Search(ctx, sessionID, "previous topic", 3)
```

---

## 策略选择建议

| 场景 | 推荐策略 | 配置 |
|------|---------|------|
| 短对话 (< 10 轮) | Sliding | keepLast=20 |
| 中长对话 | Hybrid | interval=10 |
| 长对话/多轮任务 | Hybrid + Summary | interval=5 |
| 知识密集型 | Hybrid + LongTerm | enable vector |
| 低延迟要求 | Sliding | - |

---

## Token 优化技巧

1. **设置合理的限制**
   ```go
   agent.WithMaxContextTokens(4000)  // 根据模型调整
   ```

2. **定期生成摘要**
   ```go
   agent.WithSummaryInterval(5)  // 更频繁摘要
   ```

3. **移除不必要的 System 消息**
   ```go
   // 只保留一个 System 消息
   ```

4. **使用高效的策略**
   ```go
   // 滑动窗口最快，混合模式平衡效果
   ```

---

## 数据库表

```sql
-- 向量嵌入表
embeddings (
    id, session_id, message_id,
    content, vector, metadata
)

-- 对话摘要表
summaries (
    id, session_id, content,
    token_count, created_at
)
```

---

## 性能考虑

- **Token 计数**: O(n)，n 为消息总字数
- **滑动窗口**: O(1)，只保留最近 N 条
- **对话摘要**: O(m)，m 为摘要消息数，需要 LLM 调用
- **向量搜索**: O(log n)，使用 pgvector 索引

**建议：**
- 生产环境设置 `MaxContextTokens` 为模型限制的 80%
- 长对话启用摘要功能
- 向量搜索限制结果数量 (< 10)
