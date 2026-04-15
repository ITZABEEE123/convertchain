package workers

import (
	"context"
	"time"

	"convert-chain/go-engine/internal/domain"
)

// TradeRepository defines the database operations our workers need.
// We only list the methods we actually use - this is the interface segregation
// principle: keep interfaces small and focused.
type TradeRepository interface {
	// GetTradesByStatus returns all trades that have the given status string.
	// For example, GetTradesByStatus(ctx, "PENDING_DEPOSIT") returns every
	// trade waiting for a blockchain deposit.
	GetTradesByStatus(ctx context.Context, status string) ([]*domain.Trade, error)

	// GetTradeByID fetches a single trade by its UUID.
	GetTradeByID(ctx context.Context, tradeID string) (*domain.Trade, error)

	// UpdateTradeStatus persists a new status + optional metadata to the database.
	// metadata is a free-form map (stored as JSONB in Postgres).
	UpdateTradeStatus(ctx context.Context, tradeID string, status string, metadata map[string]interface{}) error

	// GetPendingPayouts returns trades in DEPOSIT_CONFIRMED status that have not
	// yet been paid out.
	GetPendingPayouts(ctx context.Context) ([]*domain.Trade, error)

	// MarkPayoutComplete records the Graph Finance payout reference and marks the
	// trade COMPLETED.
	MarkPayoutComplete(ctx context.Context, tradeID string, payoutRef string) error
}

// QuoteRepository defines the database operations the quote expiry worker needs.
type QuoteRepository interface {
	// GetExpiredPendingQuotes returns all quotes that are still in PENDING status
	// but whose ExpiresAt timestamp is in the past.
	GetExpiredPendingQuotes(ctx context.Context, now time.Time) ([]*domain.Quote, error)

	// ExpireQuote sets a quote's status to EXPIRED.
	ExpireQuote(ctx context.Context, quoteID string) error
}

// BlockchainClient defines the external blockchain-checking operations.
// The real implementation will call Binance or Bybit APIs; the test
// implementation returns fake data.
type BlockchainClient interface {
	// CheckDeposit looks up the deposit address for a trade on the blockchain.
	// expectedAmount is in the trade's stored minor units so worker logic can
	// stay aligned with the domain model and avoid float rounding.
	CheckDeposit(ctx context.Context, currency string, address string, expectedAmount int64) (*DepositResult, error)
}

// DepositResult is the data returned from a blockchain check.
type DepositResult struct {
	// Found is true if any transaction was detected at the address.
	Found bool

	// AmountReceived is the amount actually received in the trade currency's
	// minor units (for example satoshis for BTC).
	AmountReceived int64

	// Confirmations is the number of block confirmations the transaction has.
	Confirmations int

	// TxHash is the blockchain transaction hash, used for audit trails.
	TxHash string
}

// GraphFinanceClient defines the payout operations.
type GraphFinanceClient interface {
	// ConvertAndPay converts platform funds and initiates a payout to the user's
	// registered bank account. payoutAmount is expressed in the trade's stored
	// payout minor units (for NGN this is kobo).
	ConvertAndPay(ctx context.Context, bankAccountID string, payoutAmount int64) (string, error)
}
