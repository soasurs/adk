# ADK - Agent Development Kit

一个基于 Go 的无状态 Agent 框架，使用 PostgreSQL 进行消息持久化，支持 HTTP API 访问。

## 架构特点

- **无状态 Agent**: Agent 不持有任何状态，所有上下文从 PostgreSQL 加载
- **多 Agent 编排**: 支持多个专业 Agent 协同工作，内置工作流引擎
- **PostgreSQL 持久化**: 所有会话、消息、运行记录、工作流定义都存储在数据库中
- **HTTP API**: RESTful API + SSE 流式支持
- **工具系统**: 可扩展的工具注册和执行机制
- **ReAct 模式**: 内置 ReAct 循环支持工具调用
- **Agent 注册表**: 集中管理多个专业 Agent（默认、代码、写作等）
- **工作流引擎**: 支持顺序、并行、条件执行等多种编排模式

## 项目结构

```
adk/
├── cmd/
│   ├── api/              # HTTP API 服务入口
│   └── worker/           # 后台 Worker 入口
├── pkg/
│   ├── agent/            # 多 Agent 编排核心（注册表、配置）
│   ├── orchestration/    # 工作流引擎（顺序/并行/条件执行）
│   ├── api/              # HTTP API 层
│   │   ├── handler/      # HTTP Handlers
│   │   ├── middleware/   # 中间件
│   │   └── dto/          # 请求/响应 DTO
│   ├── llm/              # LLM 抽象层
│   │   └── openai/       # OpenAI 实现
│   └── tool/             # 工具系统
│       └── builtin/      # 内置工具
├── internal/
│   ├── storage/          # 存储层
│   │   └── postgres/     # PostgreSQL 实现
│   └── config/           # 配置管理
└── migrations/           # 数据库迁移
```

## 快速开始

### 1. 环境要求

- Go 1.21+
- PostgreSQL 15+

### 2. 配置

通过环境变量或配置文件配置：

```bash
# 服务器配置
export SERVER_HOST=0.0.0.0
export SERVER_PORT=8080

# 数据库配置
export DB_HOST=localhost
export DB_PORT=5432
export DB_USER=postgres
export DB_PASSWORD=postgres
export DB_NAME=adk
export DB_SSLMODE=disable

# LLM 配置
export LLM_PROVIDER=openai
export LLM_API_KEY=your-api-key
export LLM_MODEL=gpt-4o
```

### 3. 运行

```bash
# 启动 API 服务
go run ./cmd/api
```

### 4. API 使用

```bash
# 查看可用 Agent
curl http://localhost:8080/api/v1/agents

# 创建会话（指定专业 Agent）
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "code"}'

# 发送消息（自动路由到会话指定的 Agent）
curl -X POST http://localhost:8080/api/v1/sessions/{session_id}/messages \
  -H "Content-Type: application/json" \
  -d '{"content": "Write a Go function to calculate factorial"}'

# 获取对话历史
curl http://localhost:8080/api/v1/sessions/{session_id}/messages

# 流式消息 (SSE)
curl -X POST http://localhost:8080/api/v1/sessions/{session_id}/stream \
  -H "Content-Type: application/json" \
  -d '{"content": "Hello!"}'

# 获取 Agent 详情
curl http://localhost:8080/api/v1/agents/code
```

## API 端点

| Method | Endpoint | 说明 |
|--------|----------|------|
| POST | `/api/v1/sessions` | 创建会话（指定 `agent_id`） |
| GET | `/api/v1/sessions/:id` | 获取会话 |
| PUT | `/api/v1/sessions/:id` | 更新会话 |
| DELETE | `/api/v1/sessions/:id` | 删除会话 |
| GET | `/api/v1/sessions/:id/messages` | 获取消息历史 |
| POST | `/api/v1/sessions/:id/messages` | 发送消息（同步，自动路由到对应 Agent） |
| POST | `/api/v1/sessions/:id/stream` | 发送消息（SSE，自动路由到对应 Agent） |
| GET | `/api/v1/runs/:id` | 获取运行记录 |
| GET | `/api/v1/runs/session/:session_id` | 获取会话的运行列表 |
| **Agent 管理** | | |
| GET | `/api/v1/agents` | 获取所有注册的 Agent |
| GET | `/api/v1/agents/:id` | 获取特定 Agent 的详细信息 |

## 内置工具

- `echo`: 回显消息（测试用）
- `calculator`: 基本算术计算
- `http_request`: HTTP 请求
- `shell`: Shell 命令执行（谨慎使用）

## 添加自定义工具

```go
import "soasurs.dev/soasurs/adk/pkg/tool"

myTool := tool.NewTool(
    "my_tool",
    "Description of my tool",
    map[string]any{
        "type": "object",
        "properties": map[string]any{
            "param1": map[string]any{
                "type": "string",
                "description": "Parameter 1",
            },
        },
        "required": []string{"param1"},
    },
    func(ctx context.Context, args map[string]any) (any, error) {
        // 实现工具逻辑
        return "result", nil
    },
)

registry.Register(myTool)
```

## 数据库表

- `sessions`: 会话表
- `messages`: 消息表
- `runs`: 运行记录表
- `tool_calls`: 工具调用记录表
- `jobs`: 任务队列表（预留）

## 许可证

MIT License
