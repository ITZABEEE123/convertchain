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

	// GetTradeByDepositTxHash returns a trade that already recorded this
	// blockchain transaction hash, if any.
	GetTradeByDepositTxHash(ctx context.Context, txHash string) (*domain.Trade, error)

	// UpdateTradeStatus persists a new status + optional metadata to the database.
	// metadata is a free-form map that is translated into the relevant trade fields.
	UpdateTradeStatus(ctx context.Context, tradeID string, status string, metadata map[string]interface{}) error

	// GetPendingPayouts returns trades in CONVERSION_COMPLETED status that have
	// not yet had a payout initiated.
	GetPendingPayouts(ctx context.Context) ([]*domain.Trade, error)

	// MarkPayoutComplete records the Graph payout reference and marks the trade
	// as PAYOUT_COMPLETED.
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

	// Network is the normalized chain/network where this transaction was seen.
	Network string

	// Address is the matched destination address observed on chain.
	Address string

	// ReorgRisk indicates the tx is seen but not yet final on its chain.
	ReorgRisk bool

	// Reversed indicates the tx was removed/reversed (for example chain reorg).
	Reversed bool

	// Replaced indicates replacement-by-fee or equivalent replacement risk.
	Replaced bool
}

// ConversionExecutor defines the exchange-side conversion operations.
type ConversionExecutor interface {
	// ConvertToStable executes the sandbox/testnet market conversion for the
	// received trade asset and returns provider execution metadata.
	ConvertToStable(ctx context.Context, asset string, fromAmount int64) (*ConversionResult, error)
}

// ConversionResult is the normalized result returned by the conversion layer.
type ConversionResult struct {
	Exchange    string
	OrderID     string
	Symbol      string
	Status      string
	ExecutedQty string
	QuoteQty    string
}

// GraphFinanceClient defines the payout operations.
type GraphFinanceClient interface {
	// ConvertAndPay creates the Graph payout request for a trade and returns the
	// provider payout reference once the payout has been accepted for processing.
	ConvertAndPay(ctx context.Context, bankAccountID string, payoutAmount int64) (string, error)

	// GetPayoutStatus fetches the latest provider-side status for a payout.
	GetPayoutStatus(ctx context.Context, payoutRef string) (string, error)
}
