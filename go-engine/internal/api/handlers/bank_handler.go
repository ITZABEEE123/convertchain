package handlers

import (
	"context"
	"net/http"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"github.com/gin-gonic/gin"
)

type BankService interface {
	AddBankAccount(ctx context.Context, req dto.AddBankAccountRequest) (*domain.BankAccount, error)
	ListBankAccounts(ctx context.Context, userID string) ([]*domain.BankAccount, error)
	GetUserKYCStatus(ctx context.Context, userID string) (string, error)
}

type BankHandler struct{ svc BankService }

func NewBankHandler(svc BankService) *BankHandler { return &BankHandler{svc: svc} }

func (h *BankHandler) AddBankAccount(c *gin.Context) {
	var req dto.AddBankAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	kycStatus, err := h.svc.GetUserKYCStatus(c.Request.Context(), req.UserID)
	if err != nil || kycStatus != "APPROVED" {
		c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeKYCNotApproved, "KYC approval required to add bank accounts", nil))
		return
	}

	account, err := h.svc.AddBankAccount(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to add bank account", nil))
		return
	}

	c.JSON(http.StatusCreated, bankAccountToResponse(account))
}

func (h *BankHandler) ListBankAccounts(c *gin.Context) {
	accounts, err := h.svc.ListBankAccounts(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to list bank accounts", nil))
		return
	}
	responses := make([]dto.BankAccountResponse, len(accounts))
	for i, acc := range accounts {
		responses[i] = bankAccountToResponse(acc)
	}
	c.JSON(http.StatusOK, dto.ListBankAccountsResponse{UserID: c.Param("user_id"), Accounts: responses})
}

func bankAccountToResponse(acc *domain.BankAccount) dto.BankAccountResponse {
	masked := acc.AccountNumber
	if len(masked) > 4 {
		masked = "******" + masked[len(masked)-4:]
	}

	bankName := ""
	if acc.BankName != nil {
		bankName = *acc.BankName
	}

	return dto.BankAccountResponse{
		ID:            acc.ID.String(),
		UserID:        acc.UserID.String(),
		BankCode:      acc.BankCode,
		BankName:      bankName,
		AccountNumber: masked,
		AccountName:   acc.AccountName,
		IsVerified:    acc.IsVerified,
		CreatedAt:     acc.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
