// internal/exchange/exchange.go
//
// This file defines the shared interface that all exchange adapters
// must implement. The Pricing Engine programs against this interface,
// not against specific exchanges — so swapping Binance for Coinbase
// in the future requires zero changes to the pricing logic.
package exchange

import (
	"context"
	"math/big"
)

// ExchangeClient defines the contract that any cryptocurrency exchange
// adapter must fulfill. Both Binance and Bybit implement this interface.
type ExchangeClient interface {
	// GetSpotPrice returns the current spot price for a trading pair.
	// Example: GetSpotPrice(ctx, "BTCUSDT") returns the BTC price in USDT.
	//
	// The price is returned as *big.Float for arbitrary-precision math.
	// We NEVER use float64 for financial calculations.
	GetSpotPrice(ctx context.Context, symbol string) (*big.Float, error)

	// PlaceMarketOrder executes a market order on the exchange.
	// A market order means "sell immediately at the best available price."
	//
	// Parameters:
	//   - symbol: trading pair like "BTCUSDT"
	//   - side: "SELL" (we sell crypto, receive USDT)
	//   - quantity: amount of crypto to sell (e.g., "0.25000000" for 0.25 BTC)
	//
	// Returns the order result with execution details.
	PlaceMarketOrder(ctx context.Context, symbol, side, quantity string) (*OrderResult, error)

	// GetBalance returns the available balance for a specific asset.
	// Example: GetBalance(ctx, "USDT") returns how much USDT you hold.
	GetBalance(ctx context.Context, asset string) (string, error)

	// Name returns the exchange name for logging purposes.
	Name() string
}

// OrderResult is the standardized result from placing an order.
// Both Binance and Bybit responses are converted into this format.
type OrderResult struct {
	OrderID     string // Exchange-specific order ID
	Symbol      string // Trading pair
	Side        string // "SELL" or "BUY"
	Status      string // "FILLED", "PARTIALLY_FILLED", etc.
	ExecutedQty string // How much was actually sold
	Price       string // Average execution price
	QuoteQty    string // Total value received (USDT)
}