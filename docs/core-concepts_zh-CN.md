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

## Durable Turn

每次 `Runner.Run` 都会生成一个 `TurnID`，同一次调用中的 user event 和 agent events
共享该 ID。它只是关联标识，不是排序键，也不是自动恢复检查点。

对于实现了 `session.TurnStore` 的 Session，Runner 会将每次运行记录为 `running`，并
最终更新为 `completed`、`failed` 或 `interrupted`。如果 Agent 失败或调用方提前停止
消费，完整 Event 仍会保留。后续调用 LLM 前，Runner 会将失败和中断的 Turn 投影成
安全上下文：保留协议闭合的事件，删除从未匹配 tool call 开始的不安全后缀，并追加一条
只存在于请求中的状态提示。这条提示不会写回 Event 账本。

Projection 是公开、可复用的边界。`runner.NewDefaultProjector` 提供与 Runner 相同的
durable Turn 规则，`runner.WithProjector` 可以安装自定义实现。应用构建 compaction
summary 时可以直接复用默认 projector。无论使用哪个 projector，Runner 都会在投影后
执行 `runner.ValidateToolProtocol`。

终态 Turn 可以保存结构化 `TurnFailure`，包含稳定 code、可展示 message 和失败 stage。
Runner 不会持久化任意 `error.Error()`。默认 classifier 只信任实现了
`session.TurnFailureProvider` 的 typed error；应用可以通过
`runner.WithFailureClassifier` 安装自己的安全转换。默认 projector 不会把仅保证“可向
用户展示”的 failure message 注入模型上下文。

Turn 和 Event 的每次写入各自具备原子性，但没有覆盖整个 Run 的长事务。Runner 在取得
Session run lock 后，会把遗留的 `running` Turn 恢复成 `interrupted/abandoned`。未实现
`TurnStore` 的第三方 Session 继续使用原来的未完成 Turn 回滚行为。

调用 Agent 前，Runner 还会检查投影后的上下文中，每个 assistant tool call 是否都有
对应的 tool result。若投影无法安全修复，返回 `runner.ErrToolExecutionUnknown`。ADK
不会自动重试结果未知的外部副作用。

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

除非确实要取消本次运行，否则应把 sequence 消费完。使用 durable Turn 时，提前停止会
保留完整 Event，并将 Turn 标记为 `interrupted/consumer_stopped`。Partial fragment 仍然
只属于 transport，断线后允许消失。旧 Session 实现继续回滚未完成 Turn。
