package handlers

import (
	"context"
	"net/http"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"

	"github.com/gin-gonic/gin"
)

type DisputeService interface {
	RaiseDispute(ctx context.Context, req dto.DisputeRequest) (*domain.DisputeRecord, error)
}

type DisputeHandler struct{ svc DisputeService }

func NewDisputeHandler(svc DisputeService) *DisputeHandler { return &DisputeHandler{svc: svc} }

func (h *DisputeHandler) RaiseDispute(c *gin.Context) {
	var req dto.DisputeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	dispute, err := h.svc.RaiseDispute(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to raise dispute", nil))
		return
	}

	c.JSON(http.StatusCreated, dto.DisputeResponse{
		DisputeID: dispute.ID,
		TradeID:   dispute.TradeID,
		Status:    "OPEN",
		CreatedAt: dispute.CreatedAt.Format("2006-01-02T15:04:05Z"),
		TicketRef: dispute.TicketRef,
	})
}
