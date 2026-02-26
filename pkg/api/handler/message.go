package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
	"soasurs.dev/soasurs/adk/pkg/agent"
	"soasurs.dev/soasurs/adk/pkg/api/dto"
)

type MessageHandler struct {
	store    storage.Store
	registry agent.Registry
}

func NewMessageHandler(store storage.Store, registry agent.Registry) *MessageHandler {
	return &MessageHandler{
		store:    store,
		registry: registry,
	}
}

func (h *MessageHandler) SendMessage(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("invalid session id"))
		return
	}

	var req dto.SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse(err.Error()))
		return
	}

	if req.Content == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("content is required"))
		return
	}

	ctx := c.Request.Context()

	session, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse("session not found"))
		return
	}

	agentID := session.AgentID
	if agentID == "" {
		agentID = "default"
	}

	agent, err := h.registry.Get(agentID)
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("agent not found: "+agentID))
		return
	}

	userMsg := &storage.Message{
		ID:        uuid.New(),
		SessionID: sessionID,
		Role:      "user",
		Content:   req.Content,
		CreatedAt: time.Now(),
	}

	if err := h.store.SaveMessage(ctx, userMsg); err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	result, err := agent.Run(ctx, sessionID, req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	assistantMsg := &storage.Message{
		ID:        uuid.New(),
		SessionID: sessionID,
		Role:      "assistant",
		Content:   result.Output,
		CreatedAt: time.Now(),
	}

	if err := h.store.SaveMessage(ctx, assistantMsg); err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(dto.RunResponse{
		ID:        result.RunID,
		SessionID: sessionID,
		Status:    "completed",
		Input:     req.Content,
		Output:    result.Output,
		CreatedAt: time.Now(),
	}))
}

func (h *MessageHandler) GetConversation(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("invalid session id"))
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if _, err := fmt.Sscanf(l, "%d", &limit); err != nil {
			limit = 50
		}
	}

	messages, err := h.store.GetConversation(c.Request.Context(), sessionID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	result := make([]dto.MessageResponse, 0, len(messages))
	for _, msg := range messages {
		result = append(result, dto.MessageResponse{
			ID:        msg.ID,
			SessionID: msg.SessionID,
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: msg.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(result))
}

func (h *MessageHandler) StreamMessage(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("invalid session id"))
		return
	}

	var req dto.StreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse(err.Error()))
		return
	}

	if req.Content == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("content is required"))
		return
	}

	ctx := c.Request.Context()

	session, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		h.sendStreamError(c, "session not found")
		return
	}

	agentID := session.AgentID
	if agentID == "" {
		agentID = "default"
	}

	agent, err := h.registry.Get(agentID)
	if err != nil {
		h.sendStreamError(c, "agent not found: "+agentID)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	userMsg := &storage.Message{
		ID:        uuid.New(),
		SessionID: sessionID,
		Role:      "user",
		Content:   req.Content,
		CreatedAt: time.Now(),
	}

	if err := h.store.SaveMessage(ctx, userMsg); err != nil {
		h.sendStreamError(c, err.Error())
		return
	}

	result, err := agent.Run(ctx, sessionID, req.Content)
	if err != nil {
		h.sendStreamError(c, err.Error())
		return
	}

	h.sendStreamChunk(c, dto.StreamChunk{
		Type:    "content",
		Content: result.Output,
	})

	h.sendStreamChunk(c, dto.StreamChunk{
		Done: true,
	})

	assistantMsg := &storage.Message{
		ID:        uuid.New(),
		SessionID: sessionID,
		Role:      "assistant",
		Content:   result.Output,
		CreatedAt: time.Now(),
	}

	h.store.SaveMessage(ctx, assistantMsg)
}

func (h *MessageHandler) sendStreamChunk(c *gin.Context, chunk dto.StreamChunk) {
	c.SSEvent("message", chunk)
	c.Writer.Flush()
}

func (h *MessageHandler) sendStreamError(c *gin.Context, errMsg string) {
	c.SSEvent("error", dto.StreamChunk{
		Error: errMsg,
		Done:  true,
	})
}
