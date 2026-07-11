package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/soasurs/adk/tool"
)

type loadSkillInput struct {
	Name string `json:"name" jsonschema:"name of the skill to load"`
}

type loadSkillOutput struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Compatibility string   `json:"compatibility,omitempty"`
	Instructions  string   `json:"instructions"`
	Resources     []string `json:"resources"`
}

type readResourceInput struct {
	Skill string `json:"skill" jsonschema:"name of the loaded skill"`
	Path  string `json:"path" jsonschema:"relative resource path listed by the skill loading tool"`
}

type readResourceOutput struct {
	Skill   string `json:"skill"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// NewLoadTool creates a tool that returns one skill's complete instructions
// and bundled-resource paths.
func NewLoadTool(catalog *Catalog, configure ...Option) (tool.Tool, error) {
	if catalog == nil {
		return nil, fmt.Errorf("skill: create load tool: catalog must not be nil")
	}
	if len(catalog.skills) == 0 {
		return nil, fmt.Errorf("skill: create load tool: catalog must not be empty")
	}
	configured, err := applyOptions(configure...)
	if err != nil {
		return nil, err
	}
	inputSchema, err := schemaWithSkillEnum[loadSkillInput]("name", catalog)
	if err != nil {
		return nil, fmt.Errorf("skill: create load tool: %w", err)
	}
	return tool.NewFunc(tool.Definition{
		Name:        configured.loadToolName,
		Description: "Load the complete instructions and resource list for an available skill before performing a matching task.",
		InputSchema: inputSchema,
	}, func(_ context.Context, input loadSkillInput) (loadSkillOutput, error) {
		skill, ok := catalog.lookup(input.Name)
		if !ok {
			return loadSkillOutput{}, modelVisibleError("skill_not_found", fmt.Sprintf("skill %q is not available", input.Name))
		}
		resources := skill.Resources
		if resources == nil {
			resources = []string{}
		}
		return loadSkillOutput{
			Name:          skill.Name,
			Description:   skill.Description,
			Compatibility: skill.Compatibility,
			Instructions:  skill.Instructions,
			Resources:     resources,
		}, nil
	})
}

// NewReadResourceTool creates a tool that reads one indexed UTF-8 text
// resource from a loaded skill. It never executes scripts.
func NewReadResourceTool(catalog *Catalog, configure ...Option) (tool.Tool, error) {
	if catalog == nil {
		return nil, fmt.Errorf("skill: create read resource tool: catalog must not be nil")
	}
	if len(catalog.skills) == 0 {
		return nil, fmt.Errorf("skill: create read resource tool: catalog must not be empty")
	}
	configured, err := applyOptions(configure...)
	if err != nil {
		return nil, err
	}
	inputSchema, err := schemaWithSkillEnum[readResourceInput]("skill", catalog)
	if err != nil {
		return nil, fmt.Errorf("skill: create read resource tool: %w", err)
	}
	return tool.NewFunc(tool.Definition{
		Name:        configured.readResourceToolName,
		Description: fmt.Sprintf("Read one UTF-8 text resource listed by %s. This tool reads scripts as text and never executes them.", configured.loadToolName),
		InputSchema: inputSchema,
	}, func(ctx context.Context, input readResourceInput) (readResourceOutput, error) {
		skill, ok := catalog.lookup(input.Skill)
		if !ok {
			return readResourceOutput{}, modelVisibleError("skill_not_found", fmt.Sprintf("skill %q is not available", input.Skill))
		}
		if !fs.ValidPath(input.Path) || input.Path == "." {
			return readResourceOutput{}, modelVisibleError("invalid_resource_path", fmt.Sprintf("resource path %q is invalid", input.Path))
		}
		if skill.source == nil {
			return readResourceOutput{}, modelVisibleError("resources_unavailable", fmt.Sprintf("skill %q has no readable resource source", input.Skill))
		}
		if _, exists := skill.source.resources[input.Path]; !exists {
			return readResourceOutput{}, modelVisibleError("resource_not_found", fmt.Sprintf("resource %q is not available for skill %q", input.Path, input.Skill))
		}

		content, tooLarge, err := readResource(ctx, skill.source, input.Path, configured.maxResourceBytes)
		if err != nil {
			return readResourceOutput{}, fmt.Errorf("skill: read resource %q for %q: %w", input.Path, input.Skill, err)
		}
		if tooLarge {
			return readResourceOutput{}, modelVisibleError("resource_too_large", fmt.Sprintf("resource %q exceeds the %d-byte limit", input.Path, configured.maxResourceBytes))
		}
		if !utf8.Valid(content) {
			return readResourceOutput{}, modelVisibleError("resource_not_utf8", fmt.Sprintf("resource %q is not UTF-8 text", input.Path))
		}
		return readResourceOutput{Skill: input.Skill, Path: input.Path, Content: string(content)}, nil
	})
}

func schemaWithSkillEnum[T any](property string, catalog *Catalog) (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("build input schema: %w", err)
	}
	propertySchema, ok := schema.Properties[property]
	if !ok {
		return nil, fmt.Errorf("input schema property %q not found", property)
	}
	propertySchema.Enum = make([]any, len(catalog.skills))
	for i, skill := range catalog.skills {
		propertySchema.Enum[i] = skill.Name
	}
	return schema, nil
}

func modelVisibleError(code, message string) error {
	structured, err := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message})
	if err != nil {
		return fmt.Errorf("skill: marshal handled error: %w", err)
	}
	result := tool.NewFuncError(message)
	result.StructuredContent = structured
	return result
}

func readResource(ctx context.Context, source *source, resourcePath string, limit int64) ([]byte, bool, error) {
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	default:
	}

	name := path.Join(source.directory, resourcePath)
	info, err := fs.Lstat(source.fsys, name)
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("resource is not a regular file")
	}
	file, err := source.fsys.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(content)) > limit {
		return nil, true, nil
	}
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	default:
		return content, false, nil
	}
}
