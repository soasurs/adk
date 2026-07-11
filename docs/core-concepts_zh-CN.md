# 核心概念

[English](core-concepts.md) · [文档索引](../README_zh-CN.md#文档)

ADK 将会话状态与 Agent 行为分开：`Runner` 负责 turn 生命周期和持久化，
`Agent` 只接收当前调用所需的历史。

## Content 与 Event

`model.Content` 是面向 provider 的载荷，可以包含 role、文本、多模态 parts、推理文本、
tool calls 以及 tool result 关联。

```go
content := model.Content{
    Role:    model.RoleUser,
    Content: "这张图片里有什么？",
    Parts: []model.ContentPart{
        {Type: model.ContentPartTypeImageURL, ImageURL: "https://example.com/photo.jpg"},
    },
}
```

`model.Event` 在 Content 外增加 author、session、turn、finish reason、usage 和时间戳等
运行时元数据。Event 既是运行输出，也是 session history 的存储单位。

完整 Event 组成持久化事件账本。Partial Event 是临时的流式片段：`Runner` 会转发给
调用方，但不会持久化。`model.TokenUsage` 记录 provider 返回的 token 总数，以及可选的
缓存、推理、音频和 prediction token 等细分数据。

## 无状态 Agent

```go
type Agent interface {
    Name() string
    Description() string
    Run(ctx context.Context, events []model.Event) iter.Seq2[*model.Event, error]
}
```

Agent 不保存会话状态。每次调用 `Runner.Run` 时，Runner 会加载 session 中的 active
events、追加 user event，再把完整历史传给 Agent。

LLM Agent 会把 Event 历史投影成 `model.LLMRequest.Contents`。动态 system instruction
等请求级行为在投影后加入，不会写回事务账本。详见
[动态 System Instruction](dynamic-instruction_zh-CN.md)。

## Turn 与回滚

每次 `Runner.Run` 都会生成一个 `TurnID`，同一次调用中的 user event 和 agent events
共享该 ID。它只是关联标识，不是排序键，也不是自动恢复检查点。

Agent 运行期间，`Runner` 会随时持久化完整 Event。如果 Agent 失败，或调用方提前停止
消费 sequence，Runner 会删除当前未完成 turn 已保存的 Event，避免把不完整的 tool
协议当成有效历史重放。

开始新 turn 前，Runner 还会检查每个持久化 assistant tool call 是否都有对应的持久化
tool result。如果缺失，返回 `runner.ErrToolExecutionUnknown`，保持 session 不变，且不
调用 Agent。ADK 不会自动重试，因为外部副作用可能已经发生。

## 流式输出

所有流式 API 都使用 `iter.Seq2[V, error]`：

```go
for event, err := range r.Run(ctx, sessionID, input) {
    if err != nil {
        return err
    }
    if event.Partial {
        fmt.Print(event.Content.Content)
        continue
    }
    // 完整 Event 已经进入当前 turn 的持久化账本。
}
```

除非确实要取消本次运行，否则应把 sequence 消费完；提前停止会触发未完成 turn 清理。
