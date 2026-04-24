package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type AdminService interface {
	ListTradeDisputes(ctx context.Context, status string, limit int) ([]*domain.TradeDispute, error)
	GetTradeDispute(ctx context.Context, identifier string) (*domain.TradeDispute, error)
	ResolveTradeDispute(ctx context.Context, identifier string, mode string, note string, resolver string) (*domain.TradeDispute, *domain.Trade, error)
	GetProviderReadiness(ctx context.Context) (*domain.ProviderReadinessReport, error)
}

type AdminHandler struct {
	svc AdminService
}

func NewAdminHandler(svc AdminService) *AdminHandler {
	return &AdminHandler{svc: svc}
}

func (h *AdminHandler) ListDisputes(c *gin.Context) {
	status := strings.TrimSpace(c.Query("status"))
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Query parameter limit must be a positive integer.", nil))
			return
		}
		limit = parsed
	}

	disputes, err := h.svc.ListTradeDisputes(c.Request.Context(), status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to list disputes", nil))
		return
	}

	response := dto.AdminDisputeListResponse{Disputes: make([]dto.AdminDisputeResponse, 0, len(disputes))}
	for _, dispute := range disputes {
		response.Disputes = append(response.Disputes, mapDispute(dispute))
	}

	c.JSON(http.StatusOK, response)
}

func (h *AdminHandler) GetDispute(c *gin.Context) {
	dispute, err := h.svc.GetTradeDispute(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to fetch dispute", nil))
		return
	}
	if dispute == nil {
		c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Dispute not found", nil))
		return
	}

	c.JSON(http.StatusOK, mapDispute(dispute))
}

func (h *AdminHandler) ResolveDispute(c *gin.Context) {
	var req dto.ResolveDisputeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	dispute, _, err := h.svc.ResolveTradeDispute(c.Request.Context(), c.Param("id"), req.ResolutionMode, req.ResolutionNote, req.Resolver)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Dispute not found", nil))
		case strings.EqualFold(err.Error(), "dispute_already_closed"):
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeConflict, "Dispute is already closed", nil))
		case strings.EqualFold(err.Error(), "unsupported_resolution_mode"):
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Unsupported resolution mode", nil))
		default:
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to resolve dispute", nil))
		}
		return
	}

	c.JSON(http.StatusOK, mapDispute(dispute))
}

func (h *AdminHandler) GetProviderReadiness(c *gin.Context) {
	report, err := h.svc.GetProviderReadiness(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to load provider readiness", nil))
		return
	}
	if report == nil {
		c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Provider readiness report not available", nil))
		return
	}

	c.JSON(http.StatusOK, dto.ProviderReadinessResponse{
		GeneratedAt:    report.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z"),
		OverallHealthy: report.OverallHealthy,
		Graph:          mapReadinessCheck(report.Graph),
		Binance:        mapReadinessCheck(report.Binance),
		Bybit:          mapReadinessCheck(report.Bybit),
		SmileID:        mapReadinessCheck(report.SmileID),
		Sumsub:         mapReadinessCheck(report.Sumsub),
	})
}

func mapDispute(dispute *domain.TradeDispute) dto.AdminDisputeResponse {
	if dispute == nil {
		return dto.AdminDisputeResponse{}
	}

	response := dto.AdminDisputeResponse{
		DisputeID: dispute.ID.String(),
		TicketRef: dispute.TicketRef,
		TradeID:   dispute.TradeID.String(),
		TradeRef:  dispute.TradeRef,
		UserID:    dispute.UserID.String(),
		Source:    dispute.Source,
		Status:    dispute.Status,
		Reason:    dispute.Reason,
		CreatedAt: dispute.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt: dispute.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if dispute.ResolutionMode != nil {
		response.ResolutionMode = *dispute.ResolutionMode
	}
	if dispute.ResolutionNote != nil {
		response.ResolutionNote = *dispute.ResolutionNote
	}
	if dispute.Resolver != nil {
		response.Resolver = *dispute.Resolver
	}
	if dispute.ResolvedAt != nil {
		response.ResolvedAt = dispute.ResolvedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return response
}

func mapReadinessCheck(check domain.ProviderReadinessCheck) dto.ProviderReadinessCheckResponse {
	return dto.ProviderReadinessCheckResponse{
		Enabled: check.Enabled,
		Healthy: check.Healthy,
		Summary: check.Summary,
		Details: check.Details,
	}
}
