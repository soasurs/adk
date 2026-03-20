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
	"github.com/soasurs/adk/session/compaction"
	"github.com/soasurs/adk/session/memory"
	"github.com/soasurs/adk/session/message"
	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/mcp"
)

const (
	exaMCPEndpoint = "https://mcp.exa.ai/mcp"
	defaultModel   = "gpt-4o-mini"
	sessionID      = 1001
	sepWidth       = 52
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

// truncate shortens s to at most n runes, appending "…" if cut.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// sep prints a horizontal separator line.
func sep() { fmt.Println(strings.Repeat("─", sepWidth)) }

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
	fmt.Print("connecting to Exa MCP... ")
	if err := toolSet.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: connect to Exa MCP: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("ok")
	defer toolSet.Close()

	tools, err := toolSet.Tools(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: list Exa MCP tools: %v\n", err)
		os.Exit(1)
	}

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

	// ── 5. Compaction ─────────────────────────────────────────────────────────
	// Trigger compaction when prompt tokens exceed the threshold; keep the most
	// recent conversation rounds verbatim and summarise older rounds via the LLM.
	compactCfg := compaction.Config{
		MaxTokens:        7000,
		KeepRecentRounds: 1,
	}
	compactor, err := compaction.NewSlidingWindowCompactor(compactCfg,
		func(ctx context.Context, msgs []*message.Message) (string, error) {
			var sb strings.Builder
			for _, m := range msgs {
				sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
			}
			req := &model.LLMRequest{
				Model: modelName,
				Messages: []model.Message{
					{
						Role:    model.RoleUser,
						Content: "Summarize the following conversation concisely:\n\n" + sb.String(),
					},
				},
			}
			for resp, err := range llm.GenerateContent(ctx, req, nil, false) {
				if err != nil {
					return "", fmt.Errorf("summarize: %w", err)
				}
				if !resp.Partial {
					return resp.Message.Content, nil
				}
			}
			return "", fmt.Errorf("summarize: no response")
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create compactor: %v\n", err)
		os.Exit(1)
	}

	// ── Header ────────────────────────────────────────────────────────────────
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Definition().Name
	}
	sep()
	fmt.Printf(" model   : %s\n", modelName)
	fmt.Printf(" context : max %d tokens · keep last %d rounds\n",
		compactCfg.MaxTokens, compactCfg.KeepRecentRounds)
	fmt.Printf(" tools   : %s\n", strings.Join(toolNames, ", "))
	sep()
	fmt.Println(`Type a message, or "exit" to quit.`)
	fmt.Println()

	// ── 6. Chat loop ───────────────────────────────────────────────────────────
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You › ")
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

		fmt.Println()

		// onAgentLine: we have printed "Agent › " and are mid-line (no trailing newline yet).
		// hadPartials: partial streaming events have already printed the content for this
		//              LLM call, so the subsequent complete event should not print it again.
		onAgentLine := false
		hadPartials := false

		for event, err := range r.Run(ctx, sessionID, model.Message{Content: input}) {
			if err != nil {
				if onAgentLine {
					fmt.Println()
					onAgentLine = false
				}
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				break
			}

			if event.Partial {
				if !onAgentLine {
					fmt.Print("Agent › ")
					onAgentLine = true
				}
				fmt.Print(event.Message.Content)
				hadPartials = true
				continue
			}

			// Complete event.
			switch event.Message.Role {
			case model.RoleAssistant:
				if len(event.Message.ToolCalls) > 0 {
					// Tool-call decision: end the current agent line and show each call.
					if onAgentLine {
						fmt.Println()
						onAgentLine = false
					}
					for _, tc := range event.Message.ToolCalls {
						fmt.Printf("  → %s  %s\n", tc.Name, truncate(tc.Arguments, 60))
					}
					// Reset for the next LLM call that follows tool results.
					hadPartials = false
				} else if !hadPartials && event.Message.Content != "" {
					// Non-streaming path: content was not yet printed via partials.
					if !onAgentLine {
						fmt.Print("Agent › ")
						onAgentLine = true
					}
					fmt.Print(event.Message.Content)
				}

			case model.RoleTool:
				// Show a brief summary of the tool result, then reset for the next round.
				fmt.Printf("  ← %d chars\n", len(event.Message.Content))
				onAgentLine = false
				hadPartials = false
			}
		}

		if onAgentLine {
			fmt.Println()
		}
		fmt.Println()

		// ── Token stats ───────────────────────────────────────────────────────
		if sess, err := sessionSvc.GetSession(ctx, sessionID); err == nil {
			if msgs, err := sess.ListMessages(ctx); err == nil {
				// Find the most recent prompt-token count for display.
				var promptTokens, completionTokens int64
				for i := len(msgs) - 1; i >= 0; i-- {
					if msgs[i].PromptTokens > 0 || msgs[i].CompletionTokens > 0 {
						promptTokens = msgs[i].PromptTokens
						completionTokens = msgs[i].CompletionTokens
						break
					}
				}
				if promptTokens > 0 || completionTokens > 0 {
					fmt.Printf("  tokens: prompt=%d  completion=%d  (threshold=%d)\n\n",
						promptTokens, completionTokens, compactCfg.MaxTokens)
				}

				// ── Compaction check ──────────────────────────────────────────
				if compaction.ShouldCompact(msgs, compactCfg) {
					fmt.Print("compacting context... ")
					splitID, summaryMsg, err := compactor(ctx, msgs)
					if err != nil {
						fmt.Fprintf(os.Stderr, "warn: compaction failed: %v\n", err)
					} else if summaryMsg != nil {
						// Count archived vs kept messages.
						splitIdx := len(msgs)
						if splitID > 0 {
							for i, m := range msgs {
								if m.MessageID == splitID {
									splitIdx = i
									break
								}
							}
						}
						archivedCount := splitIdx
						keptCount := len(msgs) - splitIdx

						beforeCount := len(msgs)
						if err := sess.CompactMessages(ctx, splitID, summaryMsg); err != nil {
							fmt.Fprintf(os.Stderr, "warn: CompactMessages failed: %v\n", err)
						} else {
							// Verify compaction worked by re-reading messages.
							afterMsgs, _ := sess.ListMessages(ctx)
							afterCount := len(afterMsgs)

							fmt.Println("done")
							fmt.Printf("  prompt tokens : %d (threshold: %d)\n",
								promptTokens, compactCfg.MaxTokens)
							fmt.Printf("  messages      : %d → %d (archived %d, kept %d + summary)\n",
								beforeCount, afterCount, archivedCount, keptCount)
							fmt.Printf("  summary       : %s\n",
								truncate(summaryMsg.Content, 100))
							fmt.Println()
						}
					}
				}
			}
		}

		sep()
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: read input: %v\n", err)
		os.Exit(1)
	}
}

// Ensure tool.Tool is used (avoid unused-import error when tools slice is empty).
var _ tool.Tool
