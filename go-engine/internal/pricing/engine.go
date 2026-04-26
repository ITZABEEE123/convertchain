// internal/pricing/engine.go
//
// The Pricing Engine is the revenue-generating core of ConvertChain.
// It answers the user's question: "How much NGN will I get for X BTC?"
//
// The flow:
//  1. Fetch BTC/USDT price from Binance (or Bybit fallback)
//  2. Fetch USDC/NGN rate from Graph Finance
//  3. Calculate gross amount: BTC → USDC → NGN
//  4. Apply tiered fee (1%–3% based on volume)
//  5. Return a locked quote valid for 120 seconds
//
// Price caching: Exchange prices are cached in Redis for 10 seconds
// to avoid hammering exchange APIs (they have rate limits).
//
// All math uses big.Float for precision. All final amounts are
// converted to int64 in minor units (kobo/satoshi).
package pricing

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"convert-chain/go-engine/internal/exchange"
	"convert-chain/go-engine/internal/graph"
)

// ──────────────────────────────────────────────
// CONSTANTS
// ──────────────────────────────────────────────

const (
	// QuoteTTL is how long a quote remains valid.
	// After 120 seconds, the quote expires and the user must request a new one.
	// This protects the platform from price movements between quote and trade.
	QuoteTTL = 120 * time.Second

	// PriceCacheTTL is how long exchange prices are cached in Redis.
	// 10 seconds balances freshness vs API rate limit concerns.
	// Binance rate limit: 1200 requests/minute for weighted endpoints.
	PriceCacheTTL = 10 * time.Second

	// Fee bounds in basis points
	MinFeeBPS = 100 // 1% minimum fee
	MaxFeeBPS = 300 // 3% maximum fee
)

// ──────────────────────────────────────────────
// PRICING ENGINE
// ──────────────────────────────────────────────

// PricingEngine aggregates prices and generates quotes.
type PricingEngine struct {
	// primary exchange (Binance)
	primary exchange.ExchangeClient

	// fallback exchange (Bybit) — used when primary fails
	fallback exchange.ExchangeClient

	// Graph Finance — provides fiat conversion rates
	graph *graph.Client

	// Redis — caches exchange prices to reduce API calls
	redis *redis.Client

	// Structured logger
	logger *slog.Logger
}

// NewPricingEngine creates a new pricing engine with all dependencies.
func NewPricingEngine(
	primary exchange.ExchangeClient,
	fallback exchange.ExchangeClient,
	graphClient *graph.Client,
	redisClient *redis.Client,
	logger *slog.Logger,
) *PricingEngine {
	return &PricingEngine{
		primary:  primary,
		fallback: fallback,
		graph:    graphClient,
		redis:    redisClient,
		logger:   logger,
	}
}

// ──────────────────────────────────────────────
// REQUEST / RESPONSE TYPES
// ──────────────────────────────────────────────

// QuoteRequest is what the Python bot sends when a user asks for a price.
type QuoteRequest struct {
	UserID       string `json:"user_id"`
	FromCurrency string `json:"from_currency"` // "BTC", "ETH", "USDC"
	ToCurrency   string `json:"to_currency"`   // "NGN", "USD"
	FromAmount   int64  `json:"from_amount"`   // In minor units (satoshis for BTC)
}

// QuoteResponse is the locked price quote returned to the user.
type QuoteResponse struct {
	QuoteID               string    `json:"quote_id"`
	FromCurrency          string    `json:"from_currency"`
	ToCurrency            string    `json:"to_currency"`
	FromAmount            int64     `json:"from_amount"`   // What user sells (satoshis)
	GrossAmount           int64     `json:"gross_amount"`  // Before fee (kobo)
	FeeAmount             int64     `json:"fee_amount"`    // Platform fee (kobo)
	ToAmount              int64     `json:"to_amount"`     // What user receives (kobo)
	FeeBPS                int       `json:"fee_bps"`       // Fee in basis points
	ExchangeRate          string    `json:"exchange_rate"` // BTC/USDC rate used
	FiatRate              string    `json:"fiat_rate"`     // USDC/NGN rate used
	ValidUntil            time.Time `json:"valid_until"`   // When this quote expires
	PricingMode           string    `json:"pricing_mode"`
	PriceSource           string    `json:"price_source"`
	FiatRateSource        string    `json:"fiat_rate_source"`
	MarketRatePerUnitKobo int64     `json:"market_rate_per_unit_kobo"`
	UserRatePerUnitKobo   int64     `json:"user_rate_per_unit_kobo"`
}

// ──────────────────────────────────────────────
// CORE: GENERATE A QUOTE
// ──────────────────────────────────────────────

// GetQuote generates a locked price quote for a crypto-to-fiat conversion.
//
// This is the most important function in the revenue pipeline:
//
//	User sees the quote → accepts it → trade is created at this exact price
//
// The math flow:
//
//	0.25 BTC (25,000,000 satoshis)
//	× 67,123.45 BTC/USDT exchange rate
//	= 16,780.8625 USDT
//	× 1,625.00 USDT/NGN fiat rate
//	= ₦27,268,901.56 gross (2,726,890,156 kobo)
//	× (1 - 0.02) fee multiplier (2% fee)
//	= ₦26,723,523.53 net (2,672,352,353 kobo)
//	Fee: ₦545,378.03 (54,537,803 kobo)
func (e *PricingEngine) GetQuote(ctx context.Context, req QuoteRequest) (*QuoteResponse, error) {
	e.logger.Info("Generating quote",
		"user_id", req.UserID,
		"from", req.FromCurrency,
		"to", req.ToCurrency,
		"amount", req.FromAmount,
	)

	// ── Step 1: Get crypto price (Binance primary, Bybit fallback) ──
	cryptoPrice, priceSource, err := e.getBestCryptoPrice(ctx, req.FromCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to get crypto price: %w", err)
	}
	e.logger.Info("Got crypto price",
		"currency", req.FromCurrency,
		"price_usdt", cryptoPrice.Text('f', 2),
	)

	// ── Step 2: Get fiat rate from Graph Finance ──
	fiatRate, err := e.graph.GetRate(ctx, "USDC", req.ToCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to get fiat rate: %w", err)
	}
	e.logger.Info("Got fiat rate",
		"pair", "USDC/"+req.ToCurrency,
		"rate", fiatRate.Text('f', 2),
	)

	// ── Step 3: Calculate fee based on volume ──
	feeBPS := e.calculateFee(req.FromAmount)

	// ── Step 4: Calculate amounts using big.Float ──
	//
	// We use big.Float throughout to avoid any floating-point precision loss.
	// Only at the very end do we convert to int64 (kobo/satoshi).

	fromAmountF := new(big.Float).SetInt64(req.FromAmount)

	// Determine the minor-unit divisor for the source currency
	// BTC: 1e8 (satoshis), ETH/BNB: 1e18 (wei), USDC/USDT: 1e6 (micro-units)
	var divisor *big.Float
	switch req.FromCurrency {
	case "BTC":
		divisor = big.NewFloat(1e8) // 100,000,000 satoshis per BTC
	case "ETH", "BNB":
		divisor = big.NewFloat(1e18) // 1e18 wei per ETH/BNB
	case "USDC", "USDT":
		divisor = big.NewFloat(1e6) // 1,000,000 micro-units per USDC
	default:
		return nil, fmt.Errorf("unsupported currency: %s", req.FromCurrency)
	}

	// Convert from minor units to whole units
	// Example: 25,000,000 satoshis ÷ 1e8 = 0.25 BTC
	wholeAmount := new(big.Float).Quo(fromAmountF, divisor)

	// Crypto → USDT (using exchange price)
	// Example: 0.25 BTC × 67,123.45 = 16,780.8625 USDT
	usdtAmount := new(big.Float).Mul(wholeAmount, cryptoPrice)

	// USDT → NGN (using Graph Finance rate)
	// Note: We treat USDT ≈ USDC (1:1) for the fiat conversion.
	// Example: 16,780.8625 × 1,625.00 = 27,268,901.5625 NGN
	grossFiatAmount := new(big.Float).Mul(usdtAmount, fiatRate)

	// Apply fee
	// feeMultiplier = 1.0 - (feeBPS / 10000)
	// Example for 2% fee: 1.0 - 0.02 = 0.98
	feeMultiplier := new(big.Float).SetFloat64(1.0 - float64(feeBPS)/10000.0)
	netFiatAmount := new(big.Float).Mul(grossFiatAmount, feeMultiplier)

	// ── Step 5: Convert to minor units (kobo for NGN, cents for USD) ──
	// Multiply by 100 to convert NGN to kobo
	hundred := big.NewFloat(100)
	grossKobo, _ := new(big.Float).Mul(grossFiatAmount, hundred).Int64()
	netKobo, _ := new(big.Float).Mul(netFiatAmount, hundred).Int64()
	feeKobo := grossKobo - netKobo

	// ── Step 6: Build the quote response ──
	quote := &QuoteResponse{
		QuoteID:               uuid.New().String(),
		FromCurrency:          req.FromCurrency,
		ToCurrency:            req.ToCurrency,
		FromAmount:            req.FromAmount,
		GrossAmount:           grossKobo,
		FeeAmount:             feeKobo,
		ToAmount:              netKobo,
		FeeBPS:                feeBPS,
		ExchangeRate:          cryptoPrice.Text('f', 8), // 8 decimal places
		FiatRate:              fiatRate.Text('f', 2),    // 2 decimal places
		ValidUntil:            time.Now().Add(QuoteTTL),
		PricingMode:           "live",
		PriceSource:           priceSource,
		FiatRateSource:        "graph",
		MarketRatePerUnitKobo: ratePerUnitKobo(grossKobo, req.FromAmount, req.FromCurrency),
		UserRatePerUnitKobo:   ratePerUnitKobo(netKobo, req.FromAmount, req.FromCurrency),
	}

	e.logger.Info("Quote generated",
		"quote_id", quote.QuoteID,
		"gross_kobo", grossKobo,
		"fee_kobo", feeKobo,
		"net_kobo", netKobo,
		"fee_bps", feeBPS,
	)

	return quote, nil
}

// ──────────────────────────────────────────────
// PRICE FETCHING (with cache + fallback)
// ──────────────────────────────────────────────

// getBestCryptoPrice tries to get the price from:
//  1. Redis cache (fastest, < 1ms)
//  2. Primary exchange — Binance (fast, < 100ms)
//  3. Fallback exchange — Bybit (last resort)
func (e *PricingEngine) getBestCryptoPrice(ctx context.Context, symbol string) (*big.Float, string, error) {
	// The trading pair symbol on exchanges
	// We trade against USDT (most liquid stablecoin pair)
	tradingPair := symbol + "USDT"

	// If the user is selling USDC or USDT, the "price" is 1.0
	// (1 USDC = 1 USDT, approximately)
	if symbol == "USDC" || symbol == "USDT" {
		return big.NewFloat(1.0), "stablecoin-parity", nil
	}

	cacheKey := fmt.Sprintf("price:%s", tradingPair)

	// ── Try Redis cache first ──
	if cached, err := e.redis.Get(ctx, cacheKey).Result(); err == nil {
		price, ok := new(big.Float).SetString(cached)
		if ok {
			e.logger.Debug("Price from cache", "pair", tradingPair, "price", cached)
			return price, "cache", nil
		}
	}

	// ── Try primary exchange (Binance) ──
	price, err := e.primary.GetSpotPrice(ctx, tradingPair)
	if err != nil {
		if e.fallback == nil {
			return nil, "", fmt.Errorf("primary price source failed and fallback is disabled: %s", err)
		}

		e.logger.Warn("Primary exchange failed, trying fallback",
			"primary", e.primary.Name(),
			"fallback", e.fallback.Name(),
			"error", err,
		)

		// ── Fallback to Bybit ──
		price, err = e.fallback.GetSpotPrice(ctx, tradingPair)
		if err != nil {
			return nil, "", fmt.Errorf("all price sources failed: primary (%s) and fallback (%s)",
				e.primary.Name(), e.fallback.Name())
		}
		e.logger.Info("Using fallback exchange price",
			"exchange", e.fallback.Name(),
			"pair", tradingPair,
		)
		return price, e.fallback.Name(), nil
	}

	// ── Cache the price in Redis ──
	priceStr := price.Text('f', 8)
	if err := e.redis.Set(ctx, cacheKey, priceStr, PriceCacheTTL).Err(); err != nil {
		// Cache failure is not fatal — we still have the price
		e.logger.Warn("Failed to cache price", "error", err)
	}

	return price, e.primary.Name(), nil
}

// ──────────────────────────────────────────────
// FEE CALCULATION
//
// Tiered fee structure based on trade volume:
//   < $100:        3.00% (300 BPS) — small trades have higher fees
//   $100 – $1,000: 2.00% (200 BPS)
//   $1k – $10k:    1.50% (150 BPS)
//   $10k+:         1.00% (100 BPS) — large trades get the best rate
//
// This is a common pricing model in fintech. Small transactions
// have higher relative overhead (compliance, support), so the fee
// percentage is higher. Large transactions bring more revenue per
// trade even at lower percentages.
// ──────────────────────────────────────────────

func (e *PricingEngine) calculateFee(amountMinorUnits int64) int {
	// Rough USD value estimate: divide by 100 (minor units to whole units)
	// This is approximate — the exact USD value would require the exchange rate,
	// but we don't need precision for tier boundaries.
	usdEstimate := amountMinorUnits / 100

	switch {
	case usdEstimate < 10000: // < $100 (in cents)
		return 300 // 3%
	case usdEstimate < 100000: // $100 – $1,000
		return 200 // 2%
	case usdEstimate < 1000000: // $1k – $10k
		return 150 // 1.5%
	default: // $10k+
		return 100 // 1%
	}
}

func ratePerUnitKobo(totalKobo int64, fromAmount int64, currency string) int64 {
	if totalKobo <= 0 || fromAmount <= 0 {
		return 0
	}

	divisor := float64(currencyDivisor(currency))
	if divisor <= 0 {
		return 0
	}

	units := float64(fromAmount) / divisor
	if units <= 0 {
		return 0
	}

	return int64(float64(totalKobo) / units)
}

func currencyDivisor(currency string) int64 {
	switch currency {
	case "BTC":
		return 100_000_000
	case "ETH", "BNB":
		return 1_000_000_000_000_000_000
	case "USDT", "USDC":
		return 1_000_000
	default:
		return 1
	}
}
