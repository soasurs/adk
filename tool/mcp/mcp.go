package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/soasurs/adk/tool"
)

// ToolSet connects to an MCP server and dynamically exposes its tools as tool.Tool instances.
type ToolSet struct {
	client    *sdkmcp.Client
	transport sdkmcp.Transport
	session   *sdkmcp.ClientSession
}

// NewToolSet creates a new ToolSet with the given transport.
// Call Connect to establish the connection before calling Tools.
func NewToolSet(transport sdkmcp.Transport) *ToolSet {
	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "adk-mcp-client",
		Version: "v1.0.0",
	}, nil)
	return &ToolSet{
		client:    client,
		transport: transport,
	}
}

// Connect establishes a connection to the MCP server.
func (s *ToolSet) Connect(ctx context.Context) error {
	session, err := s.client.Connect(ctx, s.transport, nil)
	if err != nil {
		return fmt.Errorf("mcp connect: %w", err)
	}
	s.session = session
	return nil
}

// Tools discovers all tools exposed by the MCP server and wraps them as tool.Tool instances.
func (s *ToolSet) Tools(ctx context.Context) ([]tool.Tool, error) {
	if s.session == nil {
		return nil, fmt.Errorf("mcp list tools: %w", ErrNotConnected)
	}
	var tools []tool.Tool
	for t, err := range s.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("mcp list tools: %w", err)
		}
		// sdkmcp.Tool.InputSchema is typed as any (map[string]any from JSON).
		// Round-trip through JSON to convert it to *jsonschema.Schema at construction time.
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("mcp tool %q: marshal input schema: %w", t.Name, err)
		}
		schema := new(jsonschema.Schema)
		if err := json.Unmarshal(raw, schema); err != nil {
			return nil, fmt.Errorf("mcp tool %q: unmarshal input schema: %w", t.Name, err)
		}
		tools = append(tools, &toolWrapper{
			session: s.session,
			def: tool.Definition{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			},
		})
	}
	return tools, nil
}

// Close closes the connection to the MCP server.
func (s *ToolSet) Close() error {
	if s.session == nil {
		return nil
	}
	err := s.session.Close()
	s.session = nil
	if err != nil {
		return err
	}
	return nil
}

// toolWrapper wraps a single MCP tool as a tool.Tool.
type toolWrapper struct {
	session *sdkmcp.ClientSession
	def     tool.Definition
}

func (t *toolWrapper) Definition() tool.Definition {
	return t.def
}

func (t *toolWrapper) Run(ctx context.Context, _ string, arguments string) (string, error) {
	if t.session == nil {
		return "", fmt.Errorf("mcp call tool %q: %w", t.def.Name, ErrNotConnected)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("mcp tool arguments unmarshal: %w", err)
	}
	result, err := t.session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      t.def.Name,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("mcp call tool %q: %w", t.def.Name, err)
	}
	text := extractText(result)
	if result.IsError {
		return "", fmt.Errorf("mcp tool %q error: %s", t.def.Name, text)
	}
	return text, nil
}

// extractText collects all TextContent from a CallToolResult and joins them.
func extractText(result *sdkmcp.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
