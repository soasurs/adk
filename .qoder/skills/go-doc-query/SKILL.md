---
name: go-doc-query
description: 准确查阅 Go 包文档，包括标准库、第三方依赖和本地包。支持 go doc 命令查询类型/函数/方法，并在查询失败时系统性地回退到源码搜索或 GoDoc 网页。适用于：需要了解某个包的 API、确认类型定义、查找方法签名，或不确定符号名称时。
---

# Go 包文档查阅

## 查阅流程

### 第一步：先用 go doc 快速查阅

```bash
# 查阅包概览
go doc <pkg-path>

# 查阅特定类型
go doc <pkg-path> <TypeName>

# 查阅方法
go doc <pkg-path> <TypeName>.<MethodName>

# 示例
go doc github.com/modelcontextprotocol/go-sdk/mcp
go doc github.com/modelcontextprotocol/go-sdk/mcp ClientSession
go doc net/http Request.Header
```

### 第二步：查询失败时的回退策略

**若返回 `doc: no symbol XXX in package`，按此顺序排查：**

1. **全局搜索符号名**（不确定大小写/拼写时）：
   ```bash
   go doc <pkg-path> | grep -i <keyword>
   ```

2. **直接搜索源码文件**：
   ```bash
   # 查找类型定义
   grep -r "type <Name>" $(go env GOPATH)/pkg/mod/<pkg-path>@<version>/
   
   # 或使用 grep_code / search_symbol 工具搜索本地缓存
   ```

3. **查阅 GoDoc 网页**（最权威，适合复杂包）：
   - 格式：`https://pkg.go.dev/<pkg-path>`
   - 示例：`https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp`

4. **列出包内所有导出符号**：
   ```bash
   go doc <pkg-path> | head -100
   ```

### 第三步：查阅本地包

```bash
# 在项目根目录执行
go doc ./session
go doc ./tool/mcp MCPToolSet
go doc ./agent/llmagent LLMAgent
```

## 常见陷阱

- **符号不存在**：先用 `go doc pkg | grep -i keyword` 全局搜索，不要盲目重试
- **版本差异**：`go.mod` 中的版本号决定了实际 API，同一包不同版本接口可能不同
- **内部包**：`internal/` 下的包无法通过外部路径 `go doc` 查询，直接用 `read_file` 读源文件
- **接口方法**：优先查阅接口定义所在的包，而非实现包

## 决策树

```
需要了解某个 Go 符号
│
├─ 知道完整路径和名称 → go doc <pkg> <Symbol>
│
├─ 只知道包路径 → go doc <pkg>（查看概览）
│
├─ 名称不确定 → go doc <pkg> | grep -i <keyword>
│
├─ go doc 报错 → 查 GoDoc 网页 / grep 源码
│
└─ 本地包 → go doc ./<subpkg> 或 read_file 读源文件
```
