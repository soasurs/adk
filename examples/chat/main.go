// Package main demonstrates a Chat agent that uses LlmAgent with Exa MCP search.
//
// Required environment variables:
//
//	OPENAI_API_KEY   — your OpenAI (or compatible) API key
//
// Optional environment variables:
//
//	OPENAI_BASE_URL  — override the default OpenAI endpoint
//	OPENAI_MODEL     — model name; defaults to "gpt-4o-mini"
//	EXA_API_KEY      — Exa API key; omit to connect without authentication
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/soasurs/adk/agent/llmagent"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/model/openai"
	"github.com/soasurs/adk/runner"
	"github.com/soasurs/adk/session/memory"
	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/mcp"
)

const (
	exaMCPEndpoint = "https://mcp.exa.ai/mcp"
	defaultModel   = "gpt-4o-mini"
	sessionID      = 1001
)

// apiKeyTransport injects an API key header into every HTTP request.
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

func main() {
	ctx := context.Background()

	// ── 1. OpenAI LLM ────────────────────────────────────────────────────────
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: OPENAI_API_KEY is required")
		os.Exit(1)
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = defaultModel
	}
	llm := openai.New(apiKey, baseURL, modelName)

	// ── 2. Exa MCP ToolSet ───────────────────────────────────────────────────
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: exaMCPEndpoint,
	}
	if exaKey := os.Getenv("EXA_API_KEY"); exaKey != "" {
		transport.HTTPClient = &http.Client{
			Transport: &apiKeyTransport{
				base:   http.DefaultTransport,
				header: "x-api-key",
				value:  exaKey,
			},
		}
	}

	toolSet := mcp.NewToolSet(transport)
	if err := toolSet.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: connect to Exa MCP: %v\n", err)
		os.Exit(1)
	}
	defer toolSet.Close()

	tools, err := toolSet.Tools(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: list Exa MCP tools: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Loaded %d tool(s) from Exa MCP:\n", len(tools))
	for _, t := range tools {
		fmt.Printf("  • %s — %s\n", t.Definition().Name, t.Definition().Description)
	}
	fmt.Println()

	// ── 3. LlmAgent ──────────────────────────────────────────────────────────
	agent := llmagent.New(llmagent.Config{
		Name:        "qa-agent",
		Description: "A Chat agent that searches the web via Exa",
		Model:       llm,
		Tools:       tools,
		Instruction: "You are a helpful research assistant. " +
			"When answering questions, use the available Exa search tools " +
			"to find up-to-date information. Always cite the sources you used.",
		Stream: true,
	})

	// ── 4. Runner + in-memory Session ────────────────────────────────────────
	sessionSvc := memory.NewMemorySessionService()
	if _, err := sessionSvc.CreateSession(ctx, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "error: create session: %v\n", err)
		os.Exit(1)
	}

	r, err := runner.New(agent, sessionSvc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create runner: %v\n", err)
		os.Exit(1)
	}

	// ── 5. Chat loop ───────────────────────────────────────────────────────────
	fmt.Printf("Chat Agent ready (model: %s). Type your question, or \"exit\" to quit.\n\n", modelName)
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			fmt.Println("Bye!")
			break
		}

		fmt.Print("Agent: ")
		for event, err := range r.Run(ctx, sessionID, input) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
				break
			}
			if event.Partial {
				// Stream partial content to stdout in real time.
				fmt.Print(event.Message.Content)
				continue
			}
			// Complete event.
			switch event.Message.Role {
			case model.RoleAssistant:
				if len(event.Message.ToolCalls) > 0 {
					// Show which tools the agent is calling.
					for _, tc := range event.Message.ToolCalls {
						fmt.Printf("\n  [calling tool: %s]\n  Agent: ", tc.Name)
					}
				} else if event.Message.Content != "" {
					// Non-streaming: print the complete answer.
					fmt.Println(event.Message.Content)
				}
			case model.RoleTool:
				// Tool results are processed silently; the agent will summarise them.
			}
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: read input: %v\n", err)
		os.Exit(1)
	}
}

// Ensure tool.Tool is used (avoid unused-import error when tools slice is empty).
var _ tool.Tool
