package tool

import (
	"context"
	"encoding/json"
)

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, args map[string]any) (any, error)
}

type BaseTool struct {
	NameVal        string
	DescriptionVal string
	ParametersVal  map[string]any
	ExecuteFunc    func(ctx context.Context, args map[string]any) (any, error)
}

func (b *BaseTool) Name() string {
	return b.NameVal
}

func (b *BaseTool) Description() string {
	return b.DescriptionVal
}

func (b *BaseTool) Parameters() map[string]any {
	return b.ParametersVal
}

func (b *BaseTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	return b.ExecuteFunc(ctx, args)
}

func NewTool(name, description string, params map[string]any, fn func(ctx context.Context, args map[string]any) (any, error)) Tool {
	return &BaseTool{
		NameVal:        name,
		DescriptionVal: description,
		ParametersVal:  params,
		ExecuteFunc:    fn,
	}
}

type ToolResult struct {
	Content any    `json:"content"`
	Error   string `json:"error,omitempty"`
}

func (r *ToolResult) ToJSON() string {
	b, _ := json.Marshal(r)
	return string(b)
}

func SuccessResult(content any) *ToolResult {
	return &ToolResult{Content: content}
}

func ErrorResult(err string) *ToolResult {
	return &ToolResult{Error: err}
}
