package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"soasurs.dev/soasurs/adk/pkg/agent"
	"soasurs.dev/soasurs/adk/pkg/api/dto"
)

type AgentHandler struct {
	registry agent.Registry
}

func NewAgentHandler(registry agent.Registry) *AgentHandler {
	return &AgentHandler{
		registry: registry,
	}
}

func (h *AgentHandler) ListAgents(c *gin.Context) {
	agents := h.registry.List()

	response := make([]dto.AgentResponse, 0, len(agents))
	for _, info := range agents {
		response = append(response, dto.AgentResponse{
			ID:          info.ID,
			Name:        info.Name,
			Description: info.Description,
		})
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(response))
}

func (h *AgentHandler) GetAgent(c *gin.Context) {
	agentID := c.Param("id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("agent id is required"))
		return
	}

	info, err := h.registry.GetInfo(agentID)
	if err != nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse("agent not found"))
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(dto.AgentResponse{
		ID:          info.ID,
		Name:        info.Name,
		Description: info.Description,
	}))
}
