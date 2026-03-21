package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/tool"
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
	if apiKey == "" {
		t.Skip("EXA_API_KEY not set")
	}

	ctx := t.Context()

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: exaMCPEndpoint,
	}
	if apiKey != "" {
		transport.HTTPClient = newExaHTTPClient(apiKey)
	}

	ts := NewToolSet(transport)
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

func TestToolSet_Tools_NotConnected(t *testing.T) {
	ts := NewToolSet(nil)

	tools, err := ts.Tools(t.Context())

	assert.Nil(t, tools)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotConnected)
}

func TestToolWrapper_Run_NotConnected(t *testing.T) {
	tw := &toolWrapper{
		def: tool.Definition{Name: "search"},
	}

	result, err := tw.Run(t.Context(), "call-1", `{}`)

	assert.Empty(t, result)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotConnected)
	var target error = ErrNotConnected
	assert.True(t, errors.Is(err, target))
}
