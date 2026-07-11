# 动态 System Instruction

`llmagent.InstructionProvider` 在每次 LLM 调用前动态生成一条临时的 system
instruction。当指令需要融入运行时才能确定的上下文，但又不想改变 agent
的角色定义时，它是对静态 `Config.Instruction` 的补充。

```go
import "github.com/soasurs/adk/agent/llmagent"

type InstructionProvider func(ctx context.Context, input InstructionInput) (string, error)
```

## Provider 接收到的信息

`InstructionInput` 包含三个字段：

| 字段           | 说明                                                              |
|----------------|-------------------------------------------------------------------|
| `AgentName`    | 当前 agent 的逻辑名称。                                            |
| `Iteration`    | 当次 `Run` 调用中的 LLM 调用序号，从 1 开始。                      |
| `Conversation` | 排除所有 system message 的对话历史**深拷贝**。Provider 可以随意修改此副本。 |

`Iteration` 每次 `Run` 调用重置为 1，计数范围仅限当次 tool-call loop，不跨
multi-turn session。

`Conversation` 包含 user、assistant、tool 消息，但**永远不包含 system 消息**。
SDK 不会把 Provider 之前的输出传回给它；如有需要，实现仍可以自行管理外部状态。

## 输出如何使用

动态输出位于静态 instruction 之后：

```
[静态 Instruction] + [动态输出]
```

两者以 `\n\n` 拼接，空值自动跳过。应用维护的其它 system context 独立于
Provider 契约。

拼装结果**仅影响当次请求**，不会被 yield 为 event、不会被持久化到 session
history、SDK 也不会缓存。Provider 返回 error 时 agent run 立即终止，不会调用
LLM。

## 与其它机制的选择

| 机制                     | 适用场景                                                       |
|--------------------------|--------------------------------------------------------------|
| `Config.Instruction`     | 指令在 agent 创建时即可确定。                                  |
| `InstructionProvider`    | 指令需要运行时上下文，但只需修改 system message。              |
| `BeforeLLMCalls` hooks   | 需要修改整个请求（tools、model、generation config），或跳过 LLM 调用。 |

## Prompt cache 行为

动态指令位于静态 instruction 之后，靠近每个请求的开头。一旦内容发生变化，
prompt-cache 复用会从首个不同 token 开始降低；在此之前的稳定前缀仍可能被复用。

应尽量保持动态输出稳定，避免不会影响模型行为的时间戳、随机值等内容；不需要动态
指导时返回空字符串。

### 一次 Run 内稳定的上下文

一种实用方式是从 `context.Context` 读取一次 `Run` 内保持稳定、但不同并发 Run
可以不同的元数据：

```go
type runtimeInstruction struct {
    Project     string
    Environment string
}

type runtimeInstructionKey struct{}

InstructionProvider: func(ctx context.Context, _ llmagent.InstructionInput) (string, error) {
    runtime, _ := ctx.Value(runtimeInstructionKey{}).(runtimeInstruction)
    if runtime == (runtimeInstruction{}) {
        return "", nil
    }
    return fmt.Sprintf("项目：%s，环境：%s。",
        runtime.Project, runtime.Environment), nil
},
```

调用方应在调用 `Run` 前把元数据放入 context，并在需要缓存复用的期间保持其稳定。
Provider 输入本身不包含 session ID，因此 session 级元数据需要由调用方提供。

## 上下文管理

`InstructionProvider` 不是 context store。稳定事实通常应通过 user、assistant 或
tool events 表达，使其保留在 canonical conversation 中。

对话历史较长时，应用可以选择调用 `session.ArchiveEventsBefore` 归档旧 events。摘要的
生成和存储仍由应用负责；需要时可以通过 `InstructionProvider` 注入，但不是必须的。

## 反模式

**逐轮变化。** 避免在每次迭代中注入不同的时间戳（秒级精度）、随机值或不同的
指令。每次变化都会从首个不同 token 开始降低 cache 复用。

```go
// 缓存灾难：每轮都不同。
InstructionProvider: func(ctx context.Context, input llmagent.InstructionInput) (string, error) {
    return fmt.Sprintf("这是第 %d 轮。", input.Iteration), nil
},
```

如果确实需要逐轮引导（如"这是最后一次尝试"），请有意识地接受缓存代价。

**生成几乎不变的长指令。** `InstructionProvider` 每次 LLM 调用都会执行。如果
输出在绝大多数时候都相同，应提前计算一次后设到 `Config.Instruction`。

**当作会话日志或状态存储。** Provider 的输出是临时的，不能依赖它在迭代间传递
状态——用 hook 回调或外部存储来实现。

## 示例

```go
package example

import (
    "context"
    "fmt"
    "strings"

    "github.com/soasurs/adk/agent"
    "github.com/soasurs/adk/agent/llmagent"
    "github.com/soasurs/adk/model"
)

type runtimeInstruction struct {
    Project     string
    Environment string
}

type runtimeInstructionKey struct{}

func withRuntimeInstruction(ctx context.Context, instruction runtimeInstruction) context.Context {
    return context.WithValue(ctx, runtimeInstructionKey{}, instruction)
}

func newSupportAgent(llm model.LLM) agent.Agent {
    return llmagent.New(llmagent.Config{
        Name:        "assistant",
        Model:       llm,
        Instruction: "你是一个技术支持助手。",
        InstructionProvider: func(ctx context.Context, _ llmagent.InstructionInput) (string, error) {
            runtime, _ := ctx.Value(runtimeInstructionKey{}).(runtimeInstruction)
            var parts []string

            if runtime.Project != "" {
                parts = append(parts, fmt.Sprintf("项目：%s。", runtime.Project))
            }
            if runtime.Environment != "" {
                parts = append(parts, fmt.Sprintf("环境：%s。", runtime.Environment))
            }

            return strings.Join(parts, " "), nil
        },
    })
}
```

调用 `Run` 时通过 context 传入本次运行的元数据：

```go
ctx = withRuntimeInstruction(ctx, runtimeInstruction{
    Project:     "payments",
    Environment: "staging",
})
```
