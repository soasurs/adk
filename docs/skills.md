# Agent Skills

The `skill` package implements the open [Agent Skills specification](https://agentskills.io/specification) without coupling skills to `LlmAgent`. It parses and discovers `SKILL.md` directories, renders a metadata-only catalog, and provides optional tools for loading full instructions and text resources on demand.

## Progressive disclosure

Skills are exposed in three stages:

1. `Catalog.Instruction` includes only each skill's name and description.
2. `load_skill` returns one selected skill's complete instructions and resource paths.
3. `read_skill_resource` reads one selected UTF-8 text resource.

The package does not select skills, mutate `llmagent.Config`, execute scripts, or grant permissions from the experimental `allowed-tools` field. Applications decide how to inject the catalog and which tools to register.

## Skill layout

A skill is a directory containing `SKILL.md` and optional bundled files:

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

`SKILL.md` starts with YAML frontmatter:

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

`name` and `description` are required. `Load` also enforces that `name` matches the parent directory name. The package parses `allowed-tools` for interoperability but does not treat it as a permission grant.

## Loading and discovery

Use `Parse` for an in-memory `SKILL.md` with no resource source, `Load` for one known directory, or `Discover` for the direct child directories of one or more roots:

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

Discovery ignores child directories without `SKILL.md`. Invalid skills and duplicate names are errors because the package cannot infer application-specific scope precedence.

## Injecting the catalog

Catalog instructions can be rendered as compact text (the default) or JSON. Skills are always sorted by name.

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

For an `LlmAgent`, applications may capture the rendered string in an `InstructionProvider` and register the optional tools themselves:

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

Reuse the same options for rendering and tool construction when overriding tool names. Do not register the skill tools when the catalog is empty.

## Tool results

`load_skill` accepts a skill name and returns JSON:

```json
{
  "name": "pdf-processing",
  "description": "Extract and process PDF documents.",
  "compatibility": "Requires Python 3",
  "instructions": "# PDF processing\n\n...",
  "resources": ["references/guide.md", "scripts/extract.py"]
}
```

`read_skill_resource` reads one listed resource:

```json
{
  "skill": "pdf-processing",
  "path": "references/guide.md"
}
```

It returns:

```json
{
  "skill": "pdf-processing",
  "path": "references/guide.md",
  "content": "# Guide\n\n..."
}
```

Unknown names, invalid paths, oversized files, and non-UTF-8 resources are handled model-visible failures. Filesystem and framework failures are terminal Go errors under the normal tool failure contract.

## Resource safety

The resource tool only accepts regular files indexed while loading the skill. It rejects absolute paths, traversal, unindexed paths, and symbolic links. The default response limit is 256 KiB and can be changed with `WithMaxResourceBytes`.

Resources under `scripts/` are returned as text and never executed. Binary assets are not supported by the first version. A generic `fs.FS` is not a complete security sandbox, so applications must still decide whether project- or user-provided skill roots are trusted.
