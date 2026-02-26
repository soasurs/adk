package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"soasurs.dev/soasurs/adk/internal/config"
	"soasurs.dev/soasurs/adk/internal/storage/postgres"
	"soasurs.dev/soasurs/adk/pkg/agent"
	"soasurs.dev/soasurs/adk/pkg/api"
	"soasurs.dev/soasurs/adk/pkg/api/handler"
	"soasurs.dev/soasurs/adk/pkg/llm/openai"
	"soasurs.dev/soasurs/adk/pkg/memory"
	"soasurs.dev/soasurs/adk/pkg/tool"
	"soasurs.dev/soasurs/adk/pkg/tool/builtin"
)

func main() {
	configFile := flag.String("config", "", "Path to config file")
	flag.Parse()

	var cfg *config.Config
	var err error

	if *configFile != "" {
		cfg, err = config.LoadFromFile(*configFile)
	} else {
		cfg, err = config.Load()
	}

	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := postgres.New(ctx, postgres.Config{DSN: cfg.Database.DSN})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	log.Println("Database migrations completed")

	store := postgres.NewStore(db)

	llmProvider, err := openai.NewProvider(openai.ToConfig(cfg.LLM.APIKey))
	if err != nil {
		log.Fatalf("Failed to create LLM provider: %v", err)
	}

	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(builtin.EchoTool())
	toolRegistry.Register(builtin.CalculatorTool())
	toolRegistry.Register(builtin.HTTPTool())

	memoryManager, err := memory.NewManager(memory.Config{
		MaxContextTokens: 8000,
		Strategy:         memory.StrategyHybrid,
		SummaryInterval:  10,
		LLM:              llmProvider,
		ModelName:        cfg.LLM.Model,
		EnableLongTerm:   false,
	})
	if err != nil {
		log.Fatalf("Failed to create memory manager: %v", err)
	}

	agentRegistry := agent.NewRegistry()

	defaultAgentConfig := agent.NewConfig(
		agent.WithLLM(llmProvider),
		agent.WithToolRegistry(toolRegistry),
		agent.WithMaxIterations(10),
		agent.WithMaxHistory(20),
		agent.WithSystemPrompt("You are a helpful AI assistant."),
		agent.WithMaxContextTokens(8000),
		agent.WithContextStrategy("hybrid"),
		agent.WithMemoryManager(memoryManager),
		agent.WithName("Default Assistant"),
		agent.WithDescription("General purpose AI assistant"),
	)

	codeAgentConfig := agent.NewConfig(
		agent.WithLLM(llmProvider),
		agent.WithToolRegistry(toolRegistry),
		agent.WithMaxIterations(15),
		agent.WithMaxHistory(30),
		agent.WithSystemPrompt("You are an expert programming assistant. Help users write, debug, and understand code."),
		agent.WithMaxContextTokens(16000),
		agent.WithContextStrategy("sliding"),
		agent.WithMemoryManager(memoryManager),
		agent.WithName("Code Assistant"),
		agent.WithDescription("Specialized in programming and code analysis"),
	)

	writerAgentConfig := agent.NewConfig(
		agent.WithLLM(llmProvider),
		agent.WithToolRegistry(toolRegistry),
		agent.WithMaxIterations(8),
		agent.WithMaxHistory(15),
		agent.WithSystemPrompt("You are a professional writing assistant. Help users write, edit, and improve their content."),
		agent.WithMaxContextTokens(4000),
		agent.WithContextStrategy("summary"),
		agent.WithMemoryManager(memoryManager),
		agent.WithName("Writing Assistant"),
		agent.WithDescription("Helps with writing, editing, and content creation"),
	)

	defaultAgent := agent.NewAgent(defaultAgentConfig, store)
	codeAgent := agent.NewAgent(codeAgentConfig, store)
	writerAgent := agent.NewAgent(writerAgentConfig, store)

	if err := agentRegistry.Register("default", defaultAgent); err != nil {
		log.Fatalf("Failed to register default agent: %v", err)
	}
	if err := agentRegistry.Register("code", codeAgent); err != nil {
		log.Fatalf("Failed to register code agent: %v", err)
	}
	if err := agentRegistry.Register("writer", writerAgent); err != nil {
		log.Fatalf("Failed to register writer agent: %v", err)
	}

	sessionHandler := handler.NewSessionHandler(store)
	messageHandler := handler.NewMessageHandler(store, agentRegistry)
	runHandler := handler.NewRunHandler(store)
	agentHandler := handler.NewAgentHandler(agentRegistry)

	router := api.NewRouter(api.HandlerConfig{
		SessionHandler: sessionHandler,
		MessageHandler: messageHandler,
		RunHandler:     runHandler,
		AgentHandler:   agentHandler,
	})

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	go func() {
		log.Printf("Starting server on %s", addr)
		if err := router.Run(addr); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	cancel()

	log.Println("Server stopped")
}
