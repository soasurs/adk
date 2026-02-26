package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
	"soasurs.dev/soasurs/adk/pkg/api/dto"
)

type SessionHandler struct {
	store storage.Store
}

func NewSessionHandler(store storage.Store) *SessionHandler {
	return &SessionHandler{store: store}
}

func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req dto.CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse(err.Error()))
		return
	}

	if req.AgentID == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("agent_id is required"))
		return
	}

	session := &storage.Session{
		ID:        uuid.New(),
		AgentID:   req.AgentID,
		Metadata:  req.Metadata,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.store.CreateSession(c.Request.Context(), session); err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	c.JSON(http.StatusCreated, dto.SuccessResponse(dto.SessionResponse{
		ID:        session.ID,
		AgentID:   session.AgentID,
		Metadata:  session.Metadata,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
	}))
}

func (h *SessionHandler) GetSession(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("invalid session id"))
		return
	}

	session, err := h.store.GetSession(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse("session not found"))
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(dto.SessionResponse{
		ID:        session.ID,
		AgentID:   session.AgentID,
		Metadata:  session.Metadata,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
	}))
}

func (h *SessionHandler) UpdateSession(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("invalid session id"))
		return
	}

	var req dto.CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse(err.Error()))
		return
	}

	session, err := h.store.GetSession(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse("session not found"))
		return
	}

	if req.AgentID != "" {
		session.AgentID = req.AgentID
	}
	if req.Metadata != nil {
		session.Metadata = req.Metadata
	}
	session.UpdatedAt = time.Now()

	if err := h.store.UpdateSession(c.Request.Context(), session); err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(dto.SessionResponse{
		ID:        session.ID,
		AgentID:   session.AgentID,
		Metadata:  session.Metadata,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
	}))
}

func (h *SessionHandler) DeleteSession(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("invalid session id"))
		return
	}

	if err := h.store.DeleteSession(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(nil))
}

func (h *SessionHandler) ListSessions(c *gin.Context) {
	agentID := c.Query("agent_id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("agent_id query parameter is required"))
		return
	}

	sessions, err := h.store.(interface {
		GetSessionByAgent(ctx context.Context, agentID string, limit int) ([]storage.Session, error)
	}).GetSessionByAgent(c.Request.Context(), agentID, 50)

	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	result := make([]dto.SessionResponse, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, dto.SessionResponse{
			ID:        s.ID,
			AgentID:   s.AgentID,
			Metadata:  s.Metadata,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(result))
}
