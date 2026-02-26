package api

import (
	"github.com/gin-gonic/gin"
	"soasurs.dev/soasurs/adk/pkg/api/handler"
	"soasurs.dev/soasurs/adk/pkg/api/middleware"
)

type Router struct {
	engine *gin.Engine
}

type HandlerConfig struct {
	SessionHandler *handler.SessionHandler
	MessageHandler *handler.MessageHandler
	RunHandler     *handler.RunHandler
	AgentHandler   *handler.AgentHandler
}

func NewRouter(cfg HandlerConfig, middlewares ...gin.HandlerFunc) *Router {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	if len(middlewares) == 0 {
		engine.Use(middleware.Logger(), middleware.Recovery())
	} else {
		engine.Use(middlewares...)
	}

	router := &Router{engine: engine}
	router.setupRoutes(cfg)
	return router
}

func (r *Router) setupRoutes(cfg HandlerConfig) {
	v1 := r.engine.Group("/api/v1")

	sessions := v1.Group("/sessions")
	{
		sessions.POST("", cfg.SessionHandler.CreateSession)
		sessions.GET("/:id", cfg.SessionHandler.GetSession)
		sessions.PUT("/:id", cfg.SessionHandler.UpdateSession)
		sessions.DELETE("/:id", cfg.SessionHandler.DeleteSession)
		sessions.GET("/:id/messages", cfg.MessageHandler.GetConversation)
		sessions.POST("/:id/messages", cfg.MessageHandler.SendMessage)
		sessions.POST("/:id/stream", cfg.MessageHandler.StreamMessage)
	}

	runs := v1.Group("/runs")
	{
		runs.GET("/:id", cfg.RunHandler.GetRun)
		runs.GET("/session/:session_id", cfg.RunHandler.ListRuns)
	}

	if cfg.AgentHandler != nil {
		agents := v1.Group("/agents")
		{
			agents.GET("", cfg.AgentHandler.ListAgents)
			agents.GET("/:id", cfg.AgentHandler.GetAgent)
		}
	}

	r.engine.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
}

func (r *Router) Engine() *gin.Engine {
	return r.engine
}

func (r *Router) Run(addr string) error {
	return r.engine.Run(addr)
}

func (r *Router) RunTLS(addr, certFile, keyFile string) error {
	return r.engine.RunTLS(addr, certFile, keyFile)
}
