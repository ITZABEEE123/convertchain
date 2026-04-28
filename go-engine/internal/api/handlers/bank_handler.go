package handlers

import (
	"context"
	"errors"
	"net/http"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"github.com/gin-gonic/gin"
)

type BankService interface {
	AddBankAccount(ctx context.Context, req dto.AddBankAccountRequest) (*domain.BankAccount, error)
	ListBankAccounts(ctx context.Context, userID string) ([]*domain.BankAccount, error)
	GetUserKYCStatus(ctx context.Context, userID string) (string, error)
	ListBanks(ctx context.Context) ([]*domain.BankDirectoryEntry, error)
	ResolveBankAccount(ctx context.Context, req dto.ResolveBankAccountRequest) (*domain.BankAccountResolution, error)
}

type BankHandler struct{ svc BankService }

func NewBankHandler(svc BankService) *BankHandler { return &BankHandler{svc: svc} }

func (h *BankHandler) ListBanks(c *gin.Context) {
	banks, err := h.svc.ListBanks(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to list banks", err.Error()))
		return
	}

	response := dto.ListBanksResponse{Banks: make([]dto.BankDirectoryResponse, len(banks))}
	for i, bank := range banks {
		response.Banks[i] = dto.BankDirectoryResponse{
			BankID:          bank.BankID,
			ProviderBankID:  bank.ProviderBankID,
			BankCode:        bank.BankCode,
			BankName:        bank.BankName,
			Slug:            bank.Slug,
			NIPCode:         bank.NIPCode,
			ShortCode:       bank.ShortCode,
			Country:         bank.Country,
			Currency:        bank.Currency,
			ResolveBankCode: bank.ResolveBankCode,
		}
	}

	c.JSON(http.StatusOK, response)
}

func (h *BankHandler) ResolveBankAccount(c *gin.Context) {
	var req dto.ResolveBankAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	kycStatus, err := h.svc.GetUserKYCStatus(c.Request.Context(), req.UserID)
	if err != nil || kycStatus != "APPROVED" {
		c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeKYCNotApproved, "KYC approval required to verify bank accounts", nil))
		return
	}

	resolved, err := h.svc.ResolveBankAccount(c.Request.Context(), req)
	if err != nil {
		writeBankResolveError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.ResolveBankAccountResponse{
		BankID:        resolved.BankID,
		BankCode:      resolved.BankCode,
		BankName:      resolved.BankName,
		AccountNumber: maskAccountNumber(resolved.AccountNumber),
		AccountName:   resolved.AccountName,
	})
}

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
		var bankErr safeBankError
		if errors.As(err, &bankErr) {
			writeBankResolveError(c, err)
			return
		}
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Failed to add bank account", err.Error()))
		return
	}

	c.JSON(http.StatusCreated, bankAccountToResponse(account))
}

func (h *BankHandler) ListBankAccounts(c *gin.Context) {
	accounts, err := h.svc.ListBankAccounts(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to list bank accounts", err.Error()))
		return
	}
	responses := make([]dto.BankAccountResponse, len(accounts))
	for i, acc := range accounts {
		responses[i] = bankAccountToResponse(acc)
	}
	c.JSON(http.StatusOK, dto.ListBankAccountsResponse{UserID: c.Param("user_id"), Accounts: responses})
}

func bankAccountToResponse(acc *domain.BankAccount) dto.BankAccountResponse {
	bankName := ""
	if acc.BankName != nil {
		bankName = *acc.BankName
	}

	return dto.BankAccountResponse{
		BankAccountID: acc.ID.String(),
		UserID:        acc.UserID.String(),
		BankCode:      acc.BankCode,
		BankName:      bankName,
		AccountNumber: maskAccountNumber(acc.AccountNumber),
		AccountName:   acc.AccountName,
		IsVerified:    acc.IsVerified,
		IsPrimary:     acc.IsPrimary,
		CreatedAt:     acc.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

type safeBankError interface {
	BankErrorCode() string
	HTTPStatusCode() int
	UserMessage() string
	SafeDetails() map[string]any
}

func writeBankResolveError(c *gin.Context, err error) {
	var bankErr safeBankError
	if errors.As(err, &bankErr) {
		c.JSON(bankErr.HTTPStatusCode(), dto.NewError(bankErr.BankErrorCode(), bankErr.UserMessage(), bankErr.SafeDetails()))
		return
	}
	c.JSON(http.StatusBadRequest, dto.NewError("provider_error", "Bank verification failed. Please try again.", nil))
}

func maskAccountNumber(accountNumber string) string {
	if len(accountNumber) <= 4 {
		return accountNumber
	}
	return "******" + accountNumber[len(accountNumber)-4:]
}
