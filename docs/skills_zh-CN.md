# Agent Skills

`skill` 包实现开放的 [Agent Skills 规范](https://agentskills.io/specification)，同时不与 `LlmAgent` 耦合。它负责解析和发现 `SKILL.md` 目录、渲染仅含元数据的 catalog，并提供按需加载完整 instruction 和文本资源的可选工具。

## 渐进披露

Skill 分三个阶段提供给模型：

1. `Catalog.Instruction` 只包含每个 skill 的名称和描述。
2. `load_skill` 返回选定 skill 的完整 instruction 和资源路径。
3. `read_skill_resource` 读取一个选定的 UTF-8 文本资源。

该包不会自行选择 skill、修改 `llmagent.Config`、执行脚本，也不会根据实验性的 `allowed-tools` 字段授予权限。应用负责决定如何注入 catalog 以及注册哪些工具。

## Skill 目录

一个 skill 目录包含 `SKILL.md` 和可选的附带文件：

```text
pdf-processing/
├── SKILL.md
├── references/
│   └── guide.md
├── scripts/
│   └── extract.py
└── assets/
    └── template.txt
```

`SKILL.md` 以 YAML frontmatter 开始：

```markdown
---
name: pdf-processing
description: Extract and process PDF documents. Use when handling PDFs.
license: Apache-2.0
compatibility: Requires Python 3
metadata:
  author: example
  version: "1.0"
allowed-tools: Read Bash(python:*)
---

# PDF processing

Follow these instructions when working with PDFs.
```

`name` 和 `description` 必填。`Load` 还会校验 `name` 与父目录名称一致。包会解析 `allowed-tools` 以便兼容规范，但不会把它视为权限授予。

## 加载与发现

`Parse` 用于解析没有资源来源的内存 `SKILL.md`，`Load` 用于加载一个已知目录，`Discover` 用于扫描一个或多个 root 的直接子目录：

```go
catalog, err := skill.Discover(
    os.DirFS("."),
    ".agents/skills",
    ".myapp/skills",
)
if err != nil {
    return err
}
```

发现过程会忽略没有 `SKILL.md` 的子目录。无效 skill 和重名会返回错误，因为包无法推断应用自己的 scope 优先级。

## 注入 Catalog

Catalog instruction 可以渲染为紧凑纯文本（默认）或 JSON。Skill 始终按名称排序。

```go
skillOptions := []skill.Option{
    skill.WithInstructionFormat(skill.InstructionFormatJSON),
    skill.WithLoadToolName("load_skill"),
    skill.WithReadResourceToolName("read_skill_resource"),
}

instruction, err := catalog.Instruction(skillOptions...)
if err != nil {
    return err
}
```

使用 `LlmAgent` 时，应用可以在 `InstructionProvider` 中返回预先渲染的字符串，并自行注册可选工具：

```go
loadSkill, err := skill.NewLoadTool(catalog, skillOptions...)
if err != nil {
    return err
}
readSkillResource, err := skill.NewReadResourceTool(catalog, skillOptions...)
if err != nil {
    return err
}

assistant := llmagent.New(llmagent.Config{
    Name:  "assistant",
    Model: llm,
    Tools: []tool.Tool{loadSkill, readSkillResource},
    InstructionProvider: func(
        context.Context,
        llmagent.InstructionInput,
    ) (string, error) {
        return instruction, nil
    },
})
```

覆盖工具名时，应给 instruction 渲染和工具构造复用同一组选项。Catalog 为空时不要注册 skill 工具。

## 工具结果

`load_skill` 接受一个 skill 名称并返回 JSON：

```json
{
  "name": "pdf-processing",
  "description": "Extract and process PDF documents.",
  "compatibility": "Requires Python 3",
  "instructions": "# PDF processing\n\n...",
  "resources": ["references/guide.md", "scripts/extract.py"]
}
```

`read_skill_resource` 读取一个已列出的资源：

```json
{
  "skill": "pdf-processing",
  "path": "references/guide.md"
}
```

返回：

```json
{
  "skill": "pdf-processing",
  "path": "references/guide.md",
  "content": "# Guide\n\n..."
}
```

未知名称、非法路径、超大文件和非 UTF-8 资源属于模型可见的已处理失败。文件系统和框架错误遵循普通 tool failure 契约，以非 nil Go error 终止执行。

## 资源安全

资源工具只接受加载 skill 时已建立索引的普通文件，并拒绝绝对路径、路径穿越、未索引路径和符号链接。默认响应上限为 256 KiB，可通过 `WithMaxResourceBytes` 修改。

`scripts/` 下的资源只会以文本返回，绝不会被执行。第一版不支持二进制 asset。通用 `fs.FS` 并不是完整的安全沙箱，因此应用仍需决定是否信任项目级或用户级 skill root。
