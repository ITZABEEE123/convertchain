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
)

type NotificationService interface {
	GetPendingNotifications(ctx context.Context, channelType string, limit int) ([]domain.PendingNotification, error)
	AckNotification(ctx context.Context, notificationID string, delivered bool, deliveryError string, claimToken string) error
	GetNotificationMetrics(ctx context.Context, channelType string) (pending int, deadLetter int, err error)
}

type NotificationHandler struct {
	svc NotificationService
}

func NewNotificationHandler(svc NotificationService) *NotificationHandler {
	return &NotificationHandler{svc: svc}
}

func (h *NotificationHandler) GetPending(c *gin.Context) {
	channelType := strings.TrimSpace(c.Query("channel"))
	if channelType == "" {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Query parameter channel is required.", nil))
		return
	}

	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Query parameter limit must be a positive integer.", nil))
			return
		}
		limit = parsed
	}

	items, err := h.svc.GetPendingNotifications(c.Request.Context(), channelType, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to fetch pending notifications", nil))
		return
	}

	response := dto.PendingNotificationsResponse{Notifications: make([]dto.NotificationEnvelope, 0, len(items))}
	for _, item := range items {
		response.Notifications = append(response.Notifications, dto.NotificationEnvelope{
			ID:          item.ID.String(),
			ChannelType: item.ChannelType,
			RecipientID: item.RecipientID,
			TradeID:     item.TradeID,
			EventType:   item.EventType,
			Payload:     item.Payload,
			ClaimToken:  item.ClaimToken,
			Attempts:    item.Attempts,
			CreatedAt:   item.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, response)
}

func (h *NotificationHandler) Metrics(c *gin.Context) {
	channelType := strings.TrimSpace(c.Query("channel"))
	if channelType == "" {
		channelType = "TELEGRAM"
	}

	pending, deadLetter, err := h.svc.GetNotificationMetrics(c.Request.Context(), channelType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to fetch notification metrics", nil))
		return
	}

	c.JSON(http.StatusOK, dto.NotificationMetricsResponse{
		ChannelType: strings.ToUpper(channelType),
		Pending:     pending,
		DeadLetter:  deadLetter,
	})
}

func (h *NotificationHandler) Ack(c *gin.Context) {
	var req dto.NotificationAckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	if err := h.svc.AckNotification(c.Request.Context(), c.Param("id"), req.Delivered, req.DeliveryError, req.ClaimToken); err != nil {
		var conflictErr interface{ Conflict() bool }
		if errors.As(err, &conflictErr) && conflictErr.Conflict() {
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeNotificationClaimConflict, "Notification claim token conflict", nil))
			return
		}

		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to acknowledge notification delivery", nil))
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
}
