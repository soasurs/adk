package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
	"soasurs.dev/soasurs/adk/pkg/api/dto"
)

type RunHandler struct {
	store storage.Store
}

func NewRunHandler(store storage.Store) *RunHandler {
	return &RunHandler{store: store}
}

func (h *RunHandler) GetRun(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse("invalid run id"))
		return
	}

	run, err := h.store.GetRun(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse("run not found"))
		return
	}

	toolCalls := make([]dto.ToolCallResponse, 0, len(run.ToolCalls))
	for _, tc := range run.ToolCalls {
		toolCalls = append(toolCalls, dto.ToolCallResponse{
			ID:     tc.ID,
			Name:   tc.Name,
			Args:   tc.Args,
			Result: tc.Result,
			Error:  tc.Error,
		})
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(dto.RunResponse{
		ID:          run.ID,
		SessionID:   run.SessionID,
		Status:      string(run.Status),
		Input:       run.Input,
		Output:      run.Output,
		Error:       run.Error,
		ToolCalls:   toolCalls,
		StartedAt:   run.StartedAt,
		CompletedAt: run.CompletedAt,
		CreatedAt:   run.CreatedAt,
	}))
}

func (h *RunHandler) ListRuns(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("session_id"))
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

	runs, err := h.store.GetRunsBySession(c.Request.Context(), sessionID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse(err.Error()))
		return
	}

	result := make([]dto.RunResponse, 0, len(runs))
	for _, run := range runs {
		toolCalls := make([]dto.ToolCallResponse, 0, len(run.ToolCalls))
		for _, tc := range run.ToolCalls {
			toolCalls = append(toolCalls, dto.ToolCallResponse{
				ID:     tc.ID,
				Name:   tc.Name,
				Args:   tc.Args,
				Result: tc.Result,
				Error:  tc.Error,
			})
		}

		result = append(result, dto.RunResponse{
			ID:          run.ID,
			SessionID:   run.SessionID,
			Status:      string(run.Status),
			Input:       run.Input,
			Output:      run.Output,
			Error:       run.Error,
			ToolCalls:   toolCalls,
			StartedAt:   run.StartedAt,
			CompletedAt: run.CompletedAt,
			CreatedAt:   run.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, dto.SuccessResponse(result))
}
