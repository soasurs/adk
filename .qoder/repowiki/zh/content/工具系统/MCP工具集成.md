# MCP工具集成

<cite>
**本文档引用的文件**
- [mcp.go](file://tool/mcp/mcp.go)
- [mcp_test.go](file://tool/mcp/mcp_test.go)
- [tool.go](file://tool/tool.go)
- [echo.go](file://tool/builtin/echo.go)
- [main.go](file://examples/chat/main.go)
- [README.md](file://README.md)
</cite>

## 更新摘要
**变更内容**
- 补充了详细的API参考文档，包括所有公共方法和字段的完整说明
- 新增了连接配置和传输层的详细说明
- 增加了工具调用流程的完整API文档
- 补充了错误处理和故障排除的详细指南
- 添加了实际使用示例和最佳实践

## 目录
1. [简介](#简介)
2. [项目结构](#项目结构)
3. [核心组件](#核心组件)
4. [架构概览](#架构概览)
5. [详细组件分析](#详细组件分析)
6. [API参考文档](#api参考文档)
7. [连接配置指南](#连接配置指南)
8. [工具调用流程](#工具调用流程)
9. [依赖关系分析](#依赖关系分析)
10. [性能考虑](#性能考虑)
11. [故障排除指南](#故障排除指南)
12. [结论](#结论)

## 简介

ADK框架中的MCP（Model Context Protocol）工具集成机制为AI代理提供了强大的工具扩展能力。该机制允许开发者连接任意MCP服务器，动态发现和使用服务器提供的工具集，实现与本地工具的统一处理方式。

MCP协议是一个开放标准，旨在标准化模型与工具之间的交互。通过ADK的MCP集成，开发者可以轻松地将外部工具服务无缝集成到AI代理中，实现功能强大的智能助手应用。

## 项目结构

ADK框架采用模块化设计，MCP工具集成位于专门的`tool/mcp`包中，与本地工具和其他组件保持清晰的分离：

```mermaid
graph TB
subgraph "工具系统"
ToolInterface["tool.Tool 接口"]
Definition["Definition 定义"]
subgraph "本地工具"
EchoTool["echo 工具"]
OtherBuiltin["其他内置工具"]
end
subgraph "MCP工具"
MCPToolSet["ToolSet 工具集"]
MCPWrapper["toolWrapper 包装器"]
end
end
subgraph "MCP SDK"
MCPSDK["modelcontextprotocol/go-sdk"]
Transport["传输层"]
Session["会话管理"]
end
ToolInterface --> MCPToolSet
ToolInterface --> EchoTool
MCPToolSet --> MCPWrapper
MCPWrapper --> MCPSDK
MCPSDK --> Transport
MCPSDK --> Session
```

**图表来源**
- [mcp.go:15-80](file://tool/mcp/mcp.go#L15-L80)
- [tool.go:17-23](file://tool/tool.go#L17-L23)

**章节来源**
- [mcp.go:1-121](file://tool/mcp/mcp.go#L1-L121)
- [tool.go:1-24](file://tool/tool.go#L1-L24)

## 核心组件

### ToolSet 工具集

ToolSet是MCP工具集成的核心组件，负责管理与MCP服务器的连接和工具发现过程：

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
- [mcp.go:15-86](file://tool/mcp/mcp.go#L15-L86)
- [tool.go:9-15](file://tool/tool.go#L9-L15)

### 工具接口抽象

ADK通过统一的`tool.Tool`接口抽象，实现了本地工具与MCP工具的无缝集成：

| 组件 | 职责 | 实现方式 |
|------|------|----------|
| ToolSet | 连接MCP服务器 | 使用MCP SDK客户端建立连接 |
| toolWrapper | 包装MCP工具 | 将MCP工具转换为统一接口 |
| Definition | 工具元数据 | 名称、描述、输入模式 |
| JSON Schema | 参数验证 | 类型安全的参数处理 |

**章节来源**
- [mcp.go:22-80](file://tool/mcp/mcp.go#L22-L80)
- [tool.go:9-23](file://tool/tool.go#L9-L23)

## 架构概览

MCP工具集成遵循分层架构设计，确保了良好的可扩展性和维护性：

```mermaid
sequenceDiagram
participant App as 应用程序
participant ToolSet as ToolSet
participant SDK as MCP SDK
participant Server as MCP服务器
participant Wrapper as toolWrapper
App->>ToolSet : 创建工具集
App->>ToolSet : Connect(ctx)
ToolSet->>SDK : 建立连接
SDK->>Server : 握手协议
Server-->>SDK : 连接确认
SDK-->>ToolSet : 会话对象
ToolSet-->>App : 连接成功
App->>ToolSet : Tools(ctx)
ToolSet->>SDK : 请求工具列表
SDK->>Server : 发送工具查询
Server-->>SDK : 返回工具定义
SDK-->>ToolSet : 工具元数据
ToolSet->>Wrapper : 包装工具
ToolSet-->>App : 工具实例数组
App->>Wrapper : 执行工具
Wrapper->>SDK : 调用工具
SDK->>Server : 执行工具请求
Server-->>SDK : 工具执行结果
SDK-->>Wrapper : 结果内容
Wrapper-->>App : 处理后的结果
```

**图表来源**
- [mcp.go:35-109](file://tool/mcp/mcp.go#L35-L109)

## 详细组件分析

### MCP工具发现机制

MCP工具发现过程通过迭代MCP服务器提供的工具列表实现，每个工具都会被转换为统一的`tool.Tool`接口实例：

```mermaid
flowchart TD
Start([开始工具发现]) --> GetTools["从会话获取工具列表"]
GetTools --> IterateTools{"遍历工具"}
IterateTools --> |有工具| ConvertSchema["转换输入模式"]
ConvertSchema --> CreateWrapper["创建toolWrapper"]
CreateWrapper --> AddToList["添加到工具列表"]
AddToList --> IterateTools
IterateTools --> |无工具| ReturnTools["返回工具数组"]
ReturnTools --> End([结束])
```

**图表来源**
- [mcp.go:45-72](file://tool/mcp/mcp.go#L45-L72)

### 工具参数映射

MCP工具的参数映射过程确保了类型安全和错误处理：

```mermaid
flowchart TD
Start([工具调用]) --> ParseArgs["解析JSON参数"]
ParseArgs --> ValidateArgs{"参数验证"}
ValidateArgs --> |验证失败| ReturnError["返回错误"]
ValidateArgs --> |验证通过| CallTool["调用MCP工具"]
CallTool --> ProcessResult["处理返回结果"]
ProcessResult --> ExtractText["提取文本内容"]
ExtractText --> CheckError{"检查错误状态"}
CheckError --> |有错误| ReturnError
CheckError --> |无错误| ReturnSuccess["返回成功结果"]
ReturnError --> End([结束])
ReturnSuccess --> End
```

**图表来源**
- [mcp.go:92-109](file://tool/mcp/mcp.go#L92-L109)

### 传输层配置

ADK支持多种MCP传输方式，包括HTTP流式传输和STDIO传输：

| 传输方式 | 配置选项 | 使用场景 |
|----------|----------|----------|
| StreamableClientTransport | Endpoint, HTTPClient | HTTP端点连接 |
| STDIO传输 | 可执行文件路径, 参数 | 本地进程通信 |
| 自定义传输 | RoundTrip函数 | 特殊认证需求 |

**章节来源**
- [mcp.go:35-43](file://tool/mcp/mcp.go#L35-L43)
- [mcp_test.go:21-42](file://tool/mcp/mcp_test.go#L21-L42)

### 动态加载和卸载

MCP工具的生命周期管理提供了完整的动态加载和卸载能力：

```mermaid
stateDiagram-v2
[*] --> 初始化
初始化 --> 连接中 : NewToolSet()
连接中 --> 已连接 : Connect()
已连接 --> 工具发现 : Tools()
工具发现 --> 工具就绪 : 返回工具列表
工具就绪 --> 执行中 : Run()
执行中 --> 工具就绪 : 执行完成
工具就绪 --> 关闭中 : Close()
关闭中 --> [*] : 连接关闭
```

**图表来源**
- [mcp.go:35-80](file://tool/mcp/mcp.go#L35-L80)

**章节来源**
- [mcp.go:74-80](file://tool/mcp/mcp.go#L74-L80)

## API参考文档

### ToolSet 结构体

ToolSet是MCP工具集成的核心结构体，负责管理与MCP服务器的连接和工具发现。

**字段说明**

| 字段名 | 类型 | 描述 | 默认值 |
|--------|------|------|--------|
| client | *sdkmcp.Client | MCP客户端实例 | nil |
| transport | sdkmcp.Transport | 传输层接口 | nil |
| session | *sdkmcp.ClientSession | MCP会话实例 | nil |

**构造函数**

```go
func NewToolSet(transport sdkmcp.Transport) *ToolSet
```

**方法说明**

| 方法签名 | 返回值 | 描述 |
|----------|--------|------|
| NewToolSet(transport) | *ToolSet | 创建新的ToolSet实例 |
| Connect(ctx) | error | 建立与MCP服务器的连接 |
| Tools(ctx) | ([]tool.Tool, error) | 发现并返回可用工具列表 |
| Close() | error | 关闭MCP连接 |

**使用示例**

```go
// 创建传输层
transport := &sdkmcp.StreamableClientTransport{
    Endpoint: "https://mcp.example.com/mcp",
}

// 创建ToolSet
ts := mcp.NewToolSet(transport)

// 建立连接
err := ts.Connect(ctx)
if err != nil {
    log.Fatal(err)
}

// 获取工具列表
tools, err := ts.Tools(ctx)
if err != nil {
    log.Fatal(err)
}

// 关闭连接
ts.Close()
```

**章节来源**
- [mcp.go:15-43](file://tool/mcp/mcp.go#L15-L43)

### toolWrapper 结构体

toolWrapper是MCP工具的包装器，实现了统一的`tool.Tool`接口。

**字段说明**

| 字段名 | 类型 | 描述 |
|--------|------|------|
| session | *sdkmcp.ClientSession | MCP会话实例 |
| def | tool.Definition | 工具定义信息 |

**方法说明**

| 方法签名 | 返回值 | 描述 |
|----------|--------|------|
| Definition() | tool.Definition | 返回工具定义信息 |
| Run(ctx, toolCallID, arguments) | (string, error) | 执行工具调用 |

**章节来源**
- [mcp.go:82-109](file://tool/mcp/mcp.go#L82-L109)

### Definition 结构体

Definition包含工具的元数据信息，用于LLM理解和调用工具。

**字段说明**

| 字段名 | 类型 | 描述 |
|--------|------|------|
| Name | string | 工具名称 |
| Description | string | 工具描述 |
| InputSchema | *jsonschema.Schema | 输入参数的JSON Schema |

**章节来源**
- [tool.go:9-15](file://tool/tool.go#L9-L15)

### Tool 接口

Tool接口定义了工具的标准行为，支持本地工具和MCP工具的统一处理。

**方法说明**

| 方法签名 | 返回值 | 描述 |
|----------|--------|------|
| Definition() | Definition | 返回工具元数据 |
| Run(ctx, toolCallID, arguments) | (string, error) | 执行工具调用 |

**章节来源**
- [tool.go:17-23](file://tool/tool.go#L17-L23)

## 连接配置指南

### 传输层配置

ADK支持多种MCP传输方式，每种都有特定的配置要求：

#### HTTP流式传输

```go
transport := &sdkmcp.StreamableClientTransport{
    Endpoint: "https://mcp.example.com/mcp",
    HTTPClient: &http.Client{
        Timeout: 30 * time.Second,
        Transport: &apiKeyTransport{
            base: http.DefaultTransport,
            header: "Authorization",
            value: "Bearer YOUR_TOKEN",
        },
    },
}
```

#### STDIO传输

```go
transport := &sdkmcp.STDIOTransport{
    Command: "mcp-server",
    Args: []string{"--flag", "value"},
    Env: map[string]string{
        "ENV_VAR": "value",
    },
}
```

#### 自定义传输

```go
type CustomTransport struct {
    RoundTrip func(req *http.Request) (*http.Response, error)
}

func (t *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    // 自定义逻辑
    return t.RoundTrip(req)
}
```

### 认证配置

#### API密钥认证

```go
type apiKeyTransport struct {
    base   http.RoundTripper
    header string
    value  string
}

func (t *apiKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    clone := req.Clone(req.Context())
    clone.Header.Set(t.header, t.value)
    return t.base.RoundTrip(clone)
}
```

#### Bearer Token认证

```go
transport := &sdkmcp.StreamableClientTransport{
    Endpoint: endpoint,
    HTTPClient: &http.Client{
        Transport: &bearerAuthTransport{
            base: http.DefaultTransport,
            token: "YOUR_BEARER_TOKEN",
        },
    },
}
```

### 超时和重连配置

```go
// 设置连接超时
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

// 实现重连机制
for i := 0; i < maxRetries; i++ {
    if err := ts.Connect(ctx); err == nil {
        break
    }
    time.Sleep(backoffDelay(i))
}
```

**章节来源**
- [mcp_test.go:21-42](file://tool/mcp/mcp_test.go#L21-L42)
- [main.go:39-80](file://examples/chat/main.go#L39-L80)

## 工具调用流程

### 完整调用流程

```mermaid
sequenceDiagram
participant Client as 客户端
participant ToolSet as ToolSet
participant Session as MCP会话
participant Server as MCP服务器
participant Wrapper as toolWrapper
Client->>ToolSet : Tools(ctx)
ToolSet->>Session : session.Tools(ctx)
Session->>Server : ListTools请求
Server-->>Session : 工具定义列表
Session-->>ToolSet : 工具定义
ToolSet->>Wrapper : 创建toolWrapper实例
ToolSet-->>Client : 工具实例数组
Client->>Wrapper : Run(ctx, toolCallID, arguments)
Wrapper->>Session : CallTool请求
Session->>Server : 执行工具调用
Server-->>Session : 工具执行结果
Session-->>Wrapper : CallToolResult
Wrapper->>Wrapper : extractText()
Wrapper-->>Client : 处理后的结果
```

**图表来源**
- [mcp.go:45-109](file://tool/mcp/mcp.go#L45-L109)

### 参数处理流程

```mermaid
flowchart TD
Start([工具调用开始]) --> ParseJSON["解析JSON参数字符串"]
ParseJSON --> ValidateSchema["使用JSON Schema验证"]
ValidateSchema --> Valid{参数有效?}
Valid --> |否| Error["返回参数验证错误"]
Valid --> |是| CallMCP["调用MCP工具"]
CallMCP --> ProcessResult["处理MCP返回结果"]
ProcessResult --> ExtractText["提取文本内容"]
ExtractText --> CheckError{检查错误状态}
CheckError --> |有错误| ReturnError["返回工具执行错误"]
CheckError --> |无错误| Success["返回成功结果"]
Error --> End([结束])
ReturnError --> End
Success --> End
```

**图表来源**
- [mcp.go:92-120](file://tool/mcp/mcp.go#L92-L120)

### 错误处理机制

MCP工具调用包含多层错误处理：

1. **连接错误**：连接MCP服务器失败
2. **工具发现错误**：无法获取工具列表
3. **参数验证错误**：JSON参数不符合Schema
4. **工具执行错误**：MCP服务器返回错误状态

**章节来源**
- [mcp.go:35-120](file://tool/mcp/mcp.go#L35-L120)

## 依赖关系分析

MCP工具集成的依赖关系体现了清晰的分层架构：

```mermaid
graph TB
subgraph "应用层"
Agent["LlmAgent"]
Runner["Runner"]
end
subgraph "工具层"
ToolInterface["tool.Tool 接口"]
ToolSet["ToolSet"]
LocalTools["本地工具"]
end
subgraph "MCP层"
MCPSDK["modelcontextprotocol/go-sdk"]
Transport["传输层"]
Session["会话管理"]
end
subgraph "外部服务"
MCPService["MCP服务器"]
end
Agent --> ToolInterface
Runner --> ToolInterface
ToolInterface --> ToolSet
ToolInterface --> LocalTools
ToolSet --> MCPSDK
MCPSDK --> Transport
MCPSDK --> Session
Transport --> MCPService
Session --> MCPService
```

**图表来源**
- [mcp.go:10-12](file://tool/mcp/mcp.go#L10-L12)
- [tool.go:6](file://tool/tool.go#L6)

### 外部依赖

MCP工具集成主要依赖以下外部库：

| 依赖库 | 版本 | 用途 |
|--------|------|------|
| modelcontextprotocol/go-sdk | 最新版本 | MCP协议实现 |
| google/jsonschema-go | 最新版本 | JSON Schema处理 |
| testify | 测试断言库 | 单元测试 |

**章节来源**
- [mcp.go:3-12](file://tool/mcp/mcp.go#L3-L12)

## 性能考虑

### 连接池管理

MCP工具集采用单连接管理模式，适用于大多数使用场景。对于高并发需求，建议：

- 实现连接复用机制
- 添加连接池大小限制
- 实现自动重连策略

### 内存优化

工具定义转换过程涉及JSON序列化和反序列化操作，需要注意：

- 输入模式转换的缓存策略
- 工具实例的生命周期管理
- 大量工具时的内存占用控制

### 错误处理优化

MCP工具调用的错误处理需要考虑：

- 网络超时的合理设置
- 重试机制的指数退避策略
- 错误信息的用户友好化

## 故障排除指南

### 常见连接问题

| 问题症状 | 可能原因 | 解决方案 |
|----------|----------|----------|
| 连接超时 | 网络延迟或服务器负载 | 检查网络连接，增加超时时间 |
| 认证失败 | API密钥无效或过期 | 验证环境变量设置 |
| 工具发现失败 | 服务器不支持工具接口 | 检查MCP服务器版本兼容性 |
| 参数验证错误 | JSON格式不正确 | 使用工具定义的JSON Schema验证 |

### 调试技巧

1. **启用详细日志**：在开发环境中启用MCP SDK的日志输出
2. **工具列表验证**：使用`Tools()`方法验证工具发现是否正常
3. **参数调试**：打印工具调用的参数和返回值
4. **连接状态监控**：定期检查连接状态和会话有效性

**章节来源**
- [mcp.go:92-109](file://tool/mcp/mcp.go#L92-L109)
- [mcp_test.go:44-100](file://tool/mcp/mcp_test.go#L44-L100)

## 结论

ADK框架的MCP工具集成机制为AI代理开发提供了强大而灵活的工具扩展能力。通过统一的接口抽象和清晰的架构设计，开发者可以轻松地集成各种外部工具服务，实现功能丰富的智能助手应用。

该实现的主要优势包括：

- **统一接口**：本地工具与MCP工具的无缝集成
- **动态发现**：运行时自动发现和加载工具
- **类型安全**：基于JSON Schema的参数验证
- **可扩展性**：支持多种传输方式和自定义配置
- **错误处理**：完善的错误处理和恢复机制

未来的发展方向可能包括：

- 更高级的连接管理和重连机制
- 工具缓存和预加载优化
- 更丰富的传输方式支持
- 增强的监控和诊断功能

通过持续的优化和改进，ADK的MCP工具集成机制将继续为AI代理开发提供强有力的支持。

**章节来源**
- [README.md:270-291](file://README.md#L270-L291)
- [examples/chat/main.go:52-177](file://examples/chat/main.go#L52-L177)