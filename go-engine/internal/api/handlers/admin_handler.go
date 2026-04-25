package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	ListAMLCases(ctx context.Context, status string, limit int) ([]*domain.AMLReviewCase, error)
	GetAMLCase(ctx context.Context, identifier string) (*domain.AMLReviewCase, error)
	DispositionAMLCase(ctx context.Context, identifier string, status string, disposition string, note string, strReferralMetadata map[string]interface{}, actor string) (*domain.AMLReviewCase, error)
	RecordLegalLaunchApproval(ctx context.Context, environment string, approvalStatus string, approvedBy string, legalMemoRef string, notes string, evidence map[string]interface{}, signedAt *time.Time) (*domain.LegalLaunchApproval, error)
	RecordDataProtectionEvent(ctx context.Context, userID string, eventType string, status string, reference string, details map[string]interface{}, createdBy string) (*domain.DataProtectionEvent, error)
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

func (h *AdminHandler) ListAMLCases(c *gin.Context) {
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

	cases, err := h.svc.ListAMLCases(c.Request.Context(), status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to list AML cases", nil))
		return
	}

	response := dto.AMLCaseListResponse{Cases: make([]dto.AMLCaseResponse, 0, len(cases))}
	for _, item := range cases {
		response.Cases = append(response.Cases, mapAMLCase(item))
	}
	c.JSON(http.StatusOK, response)
}

func (h *AdminHandler) GetAMLCase(c *gin.Context) {
	item, err := h.svc.GetAMLCase(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to fetch AML case", nil))
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "AML case not found", nil))
		return
	}

	c.JSON(http.StatusOK, mapAMLCase(item))
}

func (h *AdminHandler) DispositionAMLCase(c *gin.Context) {
	var req dto.AMLCaseDispositionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	item, err := h.svc.DispositionAMLCase(
		c.Request.Context(),
		c.Param("id"),
		req.Status,
		req.Disposition,
		req.DispositionNote,
		req.STRReferralMetadata,
		req.Actor,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "AML case not found", nil))
		case strings.EqualFold(err.Error(), "invalid_case_status"):
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid AML case status", nil))
		default:
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to disposition AML case", nil))
		}
		return
	}

	c.JSON(http.StatusOK, mapAMLCase(item))
}

func (h *AdminHandler) RecordLegalLaunchApproval(c *gin.Context) {
	var req dto.LegalLaunchApprovalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	var signedAt *time.Time
	if strings.TrimSpace(req.SignedAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(req.SignedAt))
		if err != nil {
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "signed_at must be an RFC3339 timestamp", nil))
			return
		}
		signedAt = &parsed
	}

	item, err := h.svc.RecordLegalLaunchApproval(
		c.Request.Context(),
		req.Environment,
		req.ApprovalStatus,
		req.ApprovedBy,
		req.LegalMemoRef,
		req.Notes,
		req.Evidence,
		signedAt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to record legal launch approval", nil))
		return
	}

	c.JSON(http.StatusOK, mapLegalApproval(item))
}

func (h *AdminHandler) RecordDataProtectionEvent(c *gin.Context) {
	var req dto.DataProtectionEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	item, err := h.svc.RecordDataProtectionEvent(
		c.Request.Context(),
		req.UserID,
		req.EventType,
		req.Status,
		req.Reference,
		req.Details,
		req.CreatedBy,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to record data protection event", nil))
		return
	}

	c.JSON(http.StatusCreated, mapDataProtectionEvent(item))
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

func mapAMLCase(item *domain.AMLReviewCase) dto.AMLCaseResponse {
	response := dto.AMLCaseResponse{}
	if item == nil {
		return response
	}
	response.CaseID = item.ID.String()
	response.CaseRef = item.CaseRef
	if item.UserID != nil {
		response.UserID = item.UserID.String()
	}
	if item.TradeID != nil {
		response.TradeID = item.TradeID.String()
	}
	if item.QuoteID != nil {
		response.QuoteID = item.QuoteID.String()
	}
	response.CaseType = item.CaseType
	response.Severity = item.Severity
	response.Status = item.Status
	response.Reason = item.Reason
	response.Evidence = item.Evidence
	if item.Disposition != nil {
		response.Disposition = *item.Disposition
	}
	if item.DispositionNote != nil {
		response.DispositionNote = *item.DispositionNote
	}
	response.STRReferralMetadata = item.STRReferralMetadata
	if item.AssignedTo != nil {
		response.AssignedTo = *item.AssignedTo
	}
	response.CreatedAt = item.CreatedAt.UTC().Format(time.RFC3339)
	response.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339)
	if item.ResolvedAt != nil {
		response.ResolvedAt = item.ResolvedAt.UTC().Format(time.RFC3339)
	}
	return response
}

func mapLegalApproval(item *domain.LegalLaunchApproval) dto.LegalLaunchApprovalResponse {
	response := dto.LegalLaunchApprovalResponse{}
	if item == nil {
		return response
	}
	response.ApprovalID = item.ID.String()
	response.Environment = item.Environment
	response.ApprovalStatus = item.ApprovalStatus
	if item.ApprovedBy != nil {
		response.ApprovedBy = *item.ApprovedBy
	}
	if item.LegalMemoRef != nil {
		response.LegalMemoRef = *item.LegalMemoRef
	}
	if item.Notes != nil {
		response.Notes = *item.Notes
	}
	response.Evidence = item.Evidence
	if item.SignedAt != nil {
		response.SignedAt = item.SignedAt.UTC().Format(time.RFC3339)
	}
	response.CreatedAt = item.CreatedAt.UTC().Format(time.RFC3339)
	response.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339)
	return response
}

func mapDataProtectionEvent(item *domain.DataProtectionEvent) dto.DataProtectionEventResponse {
	response := dto.DataProtectionEventResponse{}
	if item == nil {
		return response
	}
	response.EventID = item.ID.String()
	if item.UserID != nil {
		response.UserID = item.UserID.String()
	}
	response.EventType = item.EventType
	response.Status = item.Status
	if item.Reference != nil {
		response.Reference = *item.Reference
	}
	response.Details = item.Details
	if item.CreatedBy != nil {
		response.CreatedBy = *item.CreatedBy
	}
	response.CreatedAt = item.CreatedAt.UTC().Format(time.RFC3339)
	response.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339)
	return response
}
