package main

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"os"

	"github.com/soasurs/adk/agent/llmagent"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/runner"
	"github.com/soasurs/adk/session"
	"github.com/soasurs/adk/session/memory"
	"github.com/soasurs/adk/tool"
	adktrace "github.com/soasurs/adk/trace"
)

type demoLLM struct {
	calls int
}

func (m *demoLLM) Name() string { return "demo-model" }

func (m *demoLLM) GenerateContent(
	_ context.Context,
	_ *model.LLMRequest,
	_ *model.GenerateConfig,
	_ bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		if m.calls == 1 {
			yield(&model.LLMResponse{
				Content: model.Content{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "call-1", Name: "lookup", Arguments: []byte(`{"query":"status"}`)},
					},
				},
				FinishReason: model.FinishReasonToolCalls,
			}, nil)
			return
		}
		yield(&model.LLMResponse{
			Content:      model.Content{Role: model.RoleAssistant, Content: "The lookup tool returned ok."},
			FinishReason: model.FinishReasonStop,
			Usage: &model.TokenUsage{
				PromptTokens:     12,
				CompletionTokens: 7,
				TotalTokens:      19,
			},
		}, nil)
	}
}

type lookupInput struct {
	Query string `json:"query"`
}

type lookupOutput struct {
	Query  string `json:"query"`
	Result string `json:"result"`
}

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	lookup, err := tool.NewFunc(tool.Definition{
		Name:        "lookup",
		Description: "Return a fixed lookup result.",
	}, func(_ context.Context, input lookupInput) (lookupOutput, error) {
		return lookupOutput{Query: input.Query, Result: "ok"}, nil
	})
	if err != nil {
		panic(err)
	}

	agent := llmagent.New(llmagent.Config{
		Name:  "tracing-agent",
		Model: &demoLLM{},
		Tools: []tool.Tool{lookup},
	})
	sessions := memory.NewMemorySessionService()
	const sessionID = "tracing-session"
	_, err = sessions.CreateSession(ctx, session.CreateSessionRequest{
		SessionID: sessionID,
		AppID:     "tracing-example",
		UserID:    "user-1",
	})
	if err != nil {
		panic(err)
	}

	r, err := runner.New(agent, sessions, runner.WithTracer(adktrace.NewSlogTracer(logger)))
	if err != nil {
		panic(err)
	}

	for event, err := range r.Run(ctx, sessionID, model.Content{Content: "check status"}) {
		if err != nil {
			panic(err)
		}
		if !event.Partial && event.Content.Content != "" {
			fmt.Printf("%s: %s\n", event.Content.Role, event.Content.Content)
		}
	}
}
