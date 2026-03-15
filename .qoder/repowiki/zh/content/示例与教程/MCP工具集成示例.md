# MCP工具集成示例

<cite>
**本文档引用的文件**
- [tool/mcp/mcp.go](file://tool/mcp/mcp.go)
- [tool/mcp/mcp_test.go](file://tool/mcp/mcp_test.go)
- [tool/tool.go](file://tool/tool.go)
- [examples/chat/main.go](file://examples/chat/main.go)
- [README.md](file://README.md)
- [go.mod](file://go.mod)
</cite>

## 目录
1. [简介](#简介)
2. [项目结构](#项目结构)
3. [核心组件](#核心组件)
4. [架构概览](#架构概览)
5. [详细组件分析](#详细组件分析)
6. [依赖关系分析](#依赖关系分析)
7. [性能考虑](#性能考虑)
8. [故障排除指南](#故障排除指南)
9. [结论](#结论)
10. [附录](#附录)

## 简介

本文件是关于MCP（Model Context Protocol）工具集成的专门文档，重点介绍如何连接和使用Model Context Protocol服务器。文档详细解释了apiKeyTransport中间件的实现原理和API密钥注入机制，展示了Exa MCP工具集的连接过程，包括认证处理、工具列表获取和动态工具注册。同时提供了不同MCP服务器的连接示例，包括认证方式、错误处理和连接重试策略，并解释了工具定义的结构和参数验证，演示了工具调用的完整生命周期。

## 项目结构

该项目采用模块化设计，MCP工具集成位于`tool/mcp`包中，与主项目结构保持一致：

```mermaid
graph TB
subgraph "项目根目录"
A[README.md]
B[go.mod]
C[go.sum]
end
subgraph "核心包"
D[tool/]
E[model/]
F[agent/]
G[session/]
H[runner/]
end
subgraph "工具包"
I[tool/mcp/]
J[tool/builtin/]
K[tool/tool.go]
end
subgraph "示例"
L[examples/chat/]
end
I --> K
L --> I
D --> I
D --> J
D --> K
```

**图表来源**
- [README.md:67-89](file://README.md#L67-L89)
- [go.mod:1-47](file://go.mod#L1-L47)

**章节来源**
- [README.md:67-89](file://README.md#L67-L89)
- [go.mod:1-47](file://go.mod#L1-L47)

## 核心组件

### MCP工具集接口

MCP工具集通过`ToolSet`结构体实现，提供连接MCP服务器、发现工具和管理会话的能力：

```mermaid
classDiagram
class ToolSet {
-client : sdkmcp.Client
-transport : sdkmcp.Transport
-session : sdkmcp.ClientSession
+NewToolSet(transport) ToolSet
+Connect(ctx) error
+Tools(ctx) []Tool, error
+Close() error
}
class toolWrapper {
-session : sdkmcp.ClientSession
-def : Definition
+Definition() Definition
+Run(ctx, toolCallID, arguments) string, error
}
class Definition {
+Name : string
+Description : string
+InputSchema : jsonschema.Schema
}
ToolSet --> toolWrapper : "创建"
toolWrapper --> Definition : "使用"
```

**图表来源**
- [tool/mcp/mcp.go:15-90](file://tool/mcp/mcp.go#L15-L90)
- [tool/tool.go:9-23](file://tool/tool.go#L9-L23)

### API密钥传输中间件

`apiKeyTransport`实现了HTTP传输层的中间件模式，用于在每个请求中注入API密钥：

```mermaid
classDiagram
class apiKeyTransport {
-base : http.RoundTripper
-header : string
-value : string
+RoundTrip(req) *http.Response, error
}
class http.Client {
+Transport : http.RoundTripper
}
apiKeyTransport ..|> http.RoundTripper : "实现"
http.Client --> apiKeyTransport : "使用"
```

**图表来源**
- [tool/mcp/mcp_test.go:21-42](file://tool/mcp/mcp_test.go#L21-L42)
- [examples/chat/main.go:39-50](file://examples/chat/main.go#L39-L50)

**章节来源**
- [tool/mcp/mcp.go:15-90](file://tool/mcp/mcp.go#L15-L90)
- [tool/mcp/mcp_test.go:21-42](file://tool/mcp/mcp_test.go#L21-L42)
- [examples/chat/main.go:39-50](file://examples/chat/main.go#L39-L50)

## 架构概览

MCP工具集成的整体架构遵循ADK的设计原则，实现了松耦合的工具抽象：

```mermaid
graph TB
subgraph "应用层"
A[Agent]
B[LlmAgent]
C[Runner]
end
subgraph "工具层"
D[Tool Interface]
E[MCP ToolSet]
F[Built-in Tools]
end
subgraph "MCP服务器"
G[MCP Client SDK]
H[Exa MCP Server]
I[其他MCP服务器]
end
subgraph "传输层"
J[HTTP Transport]
K[Streamable Transport]
L[API Key Middleware]
end
A --> D
B --> D
C --> A
D --> E
D --> F
E --> G
G --> H
G --> I
J --> K
L --> J
```

**图表来源**
- [README.md:37-60](file://README.md#L37-L60)
- [tool/mcp/mcp.go:22-43](file://tool/mcp/mcp.go#L22-L43)

## 详细组件分析

### MCP工具集实现

#### 连接建立流程

```mermaid
sequenceDiagram
participant App as 应用程序
participant TS as ToolSet
participant Client as MCP客户端
participant Session as 客户端会话
participant Server as MCP服务器
App->>TS : NewToolSet(transport)
App->>TS : Connect(ctx)
TS->>Client : NewClient(implementation)
Client->>Server : Connect(transport)
Server-->>Client : Session
Client-->>TS : Session
TS->>TS : 设置session字段
TS-->>App : 连接成功
```

**图表来源**
- [tool/mcp/mcp.go:22-43](file://tool/mcp/mcp.go#L22-L43)

#### 工具发现和包装

工具发现过程涉及从MCP服务器获取工具定义，并将其转换为统一的工具接口：

```mermaid
flowchart TD
Start([开始工具发现]) --> ListTools["调用session.Tools(ctx, nil)"]
ListTools --> IterateTools{"遍历工具"}
IterateTools --> |有工具| ConvertSchema["转换输入模式<br/>JSON -> jsonschema.Schema"]
ConvertSchema --> CreateWrapper["创建toolWrapper实例"]
CreateWrapper --> AddToSlice["添加到工具切片"]
AddToSlice --> IterateTools
IterateTools --> |无工具| ReturnTools["返回工具列表"]
ReturnTools --> End([结束])
```

**图表来源**
- [tool/mcp/mcp.go:45-72](file://tool/mcp/mcp.go#L45-L72)

#### 工具执行生命周期

```mermaid
sequenceDiagram
participant Agent as Agent
participant Tool as MCP工具
participant Session as 客户端会话
participant Server as MCP服务器
Agent->>Tool : Run(ctx, toolCallID, arguments)
Tool->>Tool : 解析JSON参数
Tool->>Session : CallTool(params)
Session->>Server : 发送工具调用请求
Server-->>Session : 返回结果
Session-->>Tool : CallToolResult
Tool->>Tool : 提取文本内容
Tool->>Tool : 检查是否错误
Tool-->>Agent : 返回结果或错误
```

**图表来源**
- [tool/mcp/mcp.go:92-109](file://tool/mcp/mcp.go#L92-L109)

**章节来源**
- [tool/mcp/mcp.go:35-120](file://tool/mcp/mcp.go#L35-L120)

### API密钥传输中间件详解

#### 实现原理

`apiKeyTransport`实现了`http.RoundTripper`接口，通过装饰器模式在HTTP请求上注入API密钥：

```mermaid
classDiagram
class RoundTripper {
<<interface>>
+RoundTrip(req) *http.Response, error
}
class apiKeyTransport {
-base : http.RoundTripper
-header : string
-value : string
+RoundTrip(req) *http.Response, error
}
class http.Client {
+Transport : http.RoundTripper
}
RoundTripper <|.. apiKeyTransport : "实现"
apiKeyTransport --> http.RoundTripper : "委托"
http.Client --> apiKeyTransport : "配置"
```

**图表来源**
- [tool/mcp/mcp_test.go:21-32](file://tool/mcp/mcp_test.go#L21-L32)

#### 认证处理流程

```mermaid
flowchart TD
Request[HTTP请求] --> CloneReq["克隆请求对象"]
CloneReq --> SetHeader["设置API密钥头<br/>x-api-key: <密钥>"]
SetHeader --> Delegate["委托给基础传输器"]
Delegate --> Response[HTTP响应]
subgraph "Exa MCP认证示例"
A[检查EXA_API_KEY环境变量]
B{密钥存在?}
B --> |是| C[创建带API密钥的HTTP客户端]
B --> |否| D[使用默认HTTP客户端]
C --> E[配置StreamableClientTransport]
D --> E
E --> F[建立MCP连接]
end
```

**图表来源**
- [tool/mcp/mcp_test.go:34-42](file://tool/mcp/mcp_test.go#L34-L42)
- [examples/chat/main.go:68-80](file://examples/chat/main.go#L68-L80)

**章节来源**
- [tool/mcp/mcp_test.go:21-42](file://tool/mcp/mcp_test.go#L21-L42)
- [examples/chat/main.go:39-80](file://examples/chat/main.go#L39-L80)

### Exa MCP工具集集成

#### 连接过程

Exa MCP工具集的连接过程展示了完整的MCP集成模式：

```mermaid
sequenceDiagram
participant Main as 主程序
participant Transport as 传输层
participant HTTPClient as HTTP客户端
participant APIKeyTransport as API密钥传输
participant ToolSet as 工具集
participant Session as 客户端会话
participant ExaServer as Exa MCP服务器
Main->>Transport : 创建StreamableClientTransport
Main->>HTTPClient : 创建HTTP客户端
HTTPClient->>APIKeyTransport : 配置API密钥传输
Transport->>HTTPClient : 设置HTTP客户端
Main->>ToolSet : NewToolSet(transport)
Main->>ToolSet : Connect(ctx)
ToolSet->>Session : client.Connect(transport)
Session->>ExaServer : 建立WebSocket连接
ExaServer-->>Session : 握手完成
Session-->>ToolSet : 返回会话
ToolSet-->>Main : 连接成功
```

**图表来源**
- [examples/chat/main.go:68-87](file://examples/chat/main.go#L68-L87)

#### 工具列表获取

工具列表获取过程体现了MCP协议的动态特性：

```mermaid
flowchart TD
Start([获取工具列表]) --> CallTools["调用session.Tools(ctx, nil)"]
CallTools --> IterateResults{"遍历结果"}
IterateResults --> |有结果| ExtractTool["提取工具定义"]
ExtractTool --> ConvertSchema["转换输入模式为JSON Schema"]
ConvertSchema --> CreateTool["创建toolWrapper实例"]
CreateTool --> AddToList["添加到工具列表"]
AddToList --> IterateResults
IterateResults --> |无结果| ReturnList["返回工具列表"]
ReturnList --> End([完成])
```

**图表来源**
- [tool/mcp/mcp.go:45-72](file://tool/mcp/mcp.go#L45-L72)

**章节来源**
- [examples/chat/main.go:68-99](file://examples/chat/main.go#L68-L99)
- [tool/mcp/mcp.go:45-72](file://tool/mcp/mcp.go#L45-L72)

### 工具定义和参数验证

#### 工具定义结构

工具定义通过`Definition`结构体提供标准化的元数据描述：

```mermaid
classDiagram
class Definition {
+Name : string
+Description : string
+InputSchema : jsonschema.Schema
}
class Tool {
<<interface>>
+Definition() Definition
+Run(ctx, toolCallID, arguments) string, error
}
class toolWrapper {
-session : sdkmcp.ClientSession
-def : Definition
+Definition() Definition
+Run(ctx, toolCallID, arguments) string, error
}
Tool <|.. toolWrapper : "实现"
toolWrapper --> Definition : "持有"
```

**图表来源**
- [tool/tool.go:9-23](file://tool/tool.go#L9-L23)
- [tool/mcp/mcp.go:82-90](file://tool/mcp/mcp.go#L82-L90)

#### 参数验证机制

MCP工具通过JSON Schema进行参数验证，确保调用参数的正确性：

```mermaid
flowchart TD
Input[工具调用参数] --> ParseJSON["解析JSON字符串"]
ParseJSON --> ValidateSchema["使用JSON Schema验证"]
ValidateSchema --> Valid{验证通过?}
Valid --> |是| ExecuteTool["执行工具逻辑"]
Valid --> |否| ReturnError["返回验证错误"]
ExecuteTool --> ExtractResult["提取工具结果"]
ExtractResult --> ReturnSuccess["返回成功结果"]
ReturnError --> End([结束])
ReturnSuccess --> End
```

**图表来源**
- [tool/mcp/mcp.go:92-109](file://tool/mcp/mcp.go#L92-L109)

**章节来源**
- [tool/tool.go:9-23](file://tool/tool.go#L9-L23)
- [tool/mcp/mcp.go:82-120](file://tool/mcp/mcp.go#L82-L120)

## 依赖关系分析

### 外部依赖

项目依赖于多个关键库来实现MCP功能：

```mermaid
graph TB
subgraph "核心依赖"
A[github.com/modelcontextprotocol/go-sdk]
B[github.com/google/jsonschema-go]
C[github.com/stretchr/testify]
end
subgraph "模型提供商"
D[github.com/openai/openai-go/v3]
E[github.com/anthropics/anthropic-sdk-go]
F[google.golang.org/genai]
end
subgraph "存储和工具"
G[github.com/jmoiron/sqlx]
H[github.com/mattn/go-sqlite3]
I[github.com/bwmarrin/snowflake]
end
subgraph "项目模块"
J[soasurs.dev/soasurs/adk]
end
J --> A
J --> B
J --> D
J --> E
J --> F
J --> G
J --> H
J --> I
```

**图表来源**
- [go.mod:5-15](file://go.mod#L5-L15)

### 内部依赖关系

```mermaid
graph LR
subgraph "工具包"
A[tool/tool.go]
B[tool/mcp/mcp.go]
C[tool/builtin/echo.go]
end
subgraph "示例"
D[examples/chat/main.go]
end
subgraph "测试"
E[tool/mcp/mcp_test.go]
end
B --> A
D --> B
E --> B
D --> A
E --> A
```

**图表来源**
- [tool/tool.go:1-24](file://tool/tool.go#L1-L24)
- [tool/mcp/mcp.go:1-13](file://tool/mcp/mcp.go#L1-L13)
- [examples/chat/main.go:14-31](file://examples/chat/main.go#L14-L31)

**章节来源**
- [go.mod:1-47](file://go.mod#L1-L47)

## 性能考虑

### 连接优化

- **延迟最小化**：MCP连接使用流式传输，减少握手延迟
- **会话复用**：单个会话可承载多个工具调用，避免重复连接
- **缓存策略**：工具定义转换后的JSON Schema进行缓存，避免重复解析

### 错误处理策略

- **连接失败重试**：建议实现指数退避重试机制
- **超时控制**：为工具调用设置合理的超时时间
- **资源清理**：确保连接关闭时释放所有资源

## 故障排除指南

### 常见连接问题

#### API密钥认证失败

**症状**：连接MCP服务器时返回认证错误

**解决方案**：
1. 验证API密钥环境变量是否正确设置
2. 检查HTTP传输层配置是否正确
3. 确认MCP服务器支持的认证方式

#### 工具发现失败

**症状**：无法获取工具列表或工具列表为空

**解决方案**：
1. 检查MCP服务器状态和可达性
2. 验证工具定义的JSON Schema格式
3. 确认网络连接和防火墙设置

#### 工具调用错误

**症状**：工具执行过程中出现参数验证错误

**解决方案**：
1. 检查传入参数的JSON格式
2. 验证JSON Schema定义的约束条件
3. 查看服务器返回的具体错误信息

### 调试技巧

#### 日志记录

启用详细的日志记录来跟踪MCP通信：

```go
// 在连接前启用调试日志
transport.Debug = true
```

#### 网络诊断

使用网络诊断工具检查MCP服务器的连通性：

```bash
# 检查MCP服务器端点
curl -I https://mcp.exa.ai/mcp

# 检查API密钥认证
curl -H "x-api-key: YOUR_API_KEY" https://mcp.exa.ai/mcp
```

#### 错误追踪

实现错误追踪机制来捕获和分析MCP调用中的异常：

```go
// 包装错误以保留上下文
return fmt.Errorf("mcp tool %q: %w", t.def.Name, err)
```

**章节来源**
- [tool/mcp/mcp.go:35-43](file://tool/mcp/mcp.go#L35-L43)
- [tool/mcp/mcp.go:45-51](file://tool/mcp/mcp.go#L45-L51)
- [tool/mcp/mcp.go:92-109](file://tool/mcp/mcp.go#L92-L109)

## 结论

本MCP工具集成示例展示了如何在ADK框架中无缝集成外部MCP服务器。通过`apiKeyTransport`中间件实现API密钥注入，通过`ToolSet`结构体提供统一的工具抽象接口，以及通过`toolWrapper`实现MCP工具到通用工具接口的转换。

该实现具有以下特点：
- **模块化设计**：清晰的职责分离和接口抽象
- **认证灵活**：支持多种认证方式和自定义传输层
- **类型安全**：通过JSON Schema实现参数验证
- **易于扩展**：支持新的MCP服务器和工具类型

开发者可以基于此示例快速集成各种MCP服务器，构建功能丰富的AI代理应用。

## 附录

### 支持的MCP服务器类型

| 服务器类型 | 传输方式 | 认证方式 | 示例用途 |
|------------|----------|----------|----------|
| Exa MCP | StreamableClientTransport | API密钥头 | 搜索工具 |
| 标准MCP | StreamableClientTransport | WebSocket | 通用工具 |
| 本地MCP | StdioTransport | 无 | 开发测试 |

### 最佳实践

1. **错误处理**：始终实现适当的错误处理和重试机制
2. **资源管理**：确保正确关闭MCP连接和会话
3. **参数验证**：利用JSON Schema进行严格的参数验证
4. **监控**：实现连接状态监控和性能指标收集
5. **安全**：妥善管理API密钥和敏感信息