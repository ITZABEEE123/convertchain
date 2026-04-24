package service

import (
	"context"
	"errors"

	"convert-chain/go-engine/internal/domain"

	"github.com/jackc/pgx/v5"
)

func (s *ApplicationService) GetTradeReceipt(ctx context.Context, tradeID string) (*domain.TradeReceipt, error) {
	trade, err := s.getTradeByID(ctx, tradeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if trade == nil {
		return nil, nil
	}

	receipt := &domain.TradeReceipt{
		TradeID:          trade.ID.String(),
		TradeRef:         trade.TradeRef,
		Status:           trade.Status,
		PricingMode:      "sandbox_live_rates",
		PayoutAmountKobo: trade.ToAmountExpected,
		FeeAmountKobo:    trade.FeeAmount,
		CreatedAt:        trade.CreatedAt.UTC(),
		PayoutCompletedAt: trade.CompletedAt,
	}
	if trade.ToAmountActual != nil {
		receipt.PayoutAmountKobo = *trade.ToAmountActual
	}
	if trade.GraphPayoutID != nil {
		receipt.PayoutRef = *trade.GraphPayoutID
	}

	if trade.BankAccID != nil {
		account, err := s.getBankAccountByIDOrArchived(ctx, trade.BankAccID.String())
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if account != nil {
			receipt.BankName = bankNameOrDefault(account.BankName)
			receipt.MaskedAccountNumber = maskAccountNumber(account.AccountNumber)
		}
	}

	return receipt, nil
}

func bankNameOrDefault(value *string) string {
	if value == nil || *value == "" {
		return "Bank"
	}
	return *value
}
