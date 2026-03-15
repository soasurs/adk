package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/mcp"
)

const exaMCPEndpoint = "https://mcp.exa.ai/mcp"

// apiKeyTransport injects an API key header into every request.
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

func newExaHTTPClient(apiKey string) *http.Client {
	return &http.Client{
		Transport: &apiKeyTransport{
			base:   http.DefaultTransport,
			header: "x-api-key",
			value:  apiKey,
		},
	}
}

func TestToolSet_Exa(t *testing.T) {
	apiKey := os.Getenv("EXA_API_KEY")

	ctx := context.Background()

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: exaMCPEndpoint,
	}
	if apiKey != "" {
		transport.HTTPClient = newExaHTTPClient(apiKey)
	}

	ts := mcp.NewToolSet(transport)
	require.NoError(t, ts.Connect(ctx))
	defer ts.Close()

	t.Run("ListTools", func(t *testing.T) {
		tools, err := ts.Tools(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, tools, "expected at least one tool from Exa MCP server")

		for _, to := range tools {
			def := to.Definition()
			t.Logf("tool: %s — %s", def.Name, def.Description)
			assert.NotNil(t, def.InputSchema)
		}
	})

	t.Run("RunSearchTool", func(t *testing.T) {
		tools, err := ts.Tools(ctx)
		require.NoError(t, err)

		// Find a search-related tool
		var searchTool tool.Tool
		for _, to := range tools {
			if to.Definition().Name == "web_search_exa" || to.Definition().Name == "search" {
				searchTool = to
				break
			}
		}
		if searchTool == nil {
			t.Logf("no search tool found, available tools:")
			for _, to := range tools {
				t.Logf("  - %s", to.Definition().Name)
			}
			t.Skip("no search tool found")
		}

		args, _ := json.Marshal(map[string]any{
			"query": "Go programming language",
		})
		result, err := searchTool.Run(ctx, "test-call-id", string(args))
		require.NoError(t, err)
		assert.NotEmpty(t, result)
		fmt.Printf("\n[Exa search result]\n%s\n", result)
	})
}
