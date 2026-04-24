package service

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/exchange"
	graphclient "convert-chain/go-engine/internal/graph"
	"convert-chain/go-engine/internal/workers"
)

type SandboxBlockchainClient struct {
	mu       sync.Mutex
	checksBy map[string]int
}

func NewSandboxBlockchainClient() *SandboxBlockchainClient {
	return &SandboxBlockchainClient{checksBy: make(map[string]int)}
}

func (s *SandboxBlockchainClient) CheckDeposit(_ context.Context, currency string, address string, expectedAmount int64) (*workers.DepositResult, error) {
	if !strings.HasPrefix(address, "sandbox://") {
		return &workers.DepositResult{Found: false}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.checksBy[address]++
	checks := s.checksBy[address]
	required := sandboxRequiredConfirmations(currency)

	switch checks {
	case 1:
		return &workers.DepositResult{Found: false}, nil
	case 2:
		return &workers.DepositResult{
			Found:          true,
			AmountReceived: expectedAmount,
			Confirmations:  1,
			TxHash:         fmt.Sprintf("sandbox_tx_%s_%d", strings.ToLower(currency), checks),
		}, nil
	default:
		return &workers.DepositResult{
			Found:          true,
			AmountReceived: expectedAmount,
			Confirmations:  required,
			TxHash:         fmt.Sprintf("sandbox_tx_%s_%d", strings.ToLower(currency), checks),
		}, nil
	}
}

type ExchangeSandboxConversionClient struct {
	primary  exchange.ExchangeClient
	fallback exchange.ExchangeClient
	logger   *slog.Logger
	mu       sync.Mutex
	disabled map[string]time.Time
}

func NewExchangeSandboxConversionClient(primary, fallback exchange.ExchangeClient, logger *slog.Logger) *ExchangeSandboxConversionClient {
	return &ExchangeSandboxConversionClient{
		primary:  primary,
		fallback: fallback,
		logger:   logger,
		disabled: make(map[string]time.Time),
	}
}

func (c *ExchangeSandboxConversionClient) ConvertToStable(ctx context.Context, asset string, fromAmount int64) (*workers.ConversionResult, error) {
	normalizedAsset := strings.ToUpper(strings.TrimSpace(asset))
	if normalizedAsset == "USDT" || normalizedAsset == "USDC" {
		quantity := minorUnitsToDecimalString(fromAmount, normalizedAsset)
		return &workers.ConversionResult{
			Exchange:    "noop",
			OrderID:     fmt.Sprintf("noop-%s-%d", strings.ToLower(normalizedAsset), time.Now().UnixNano()),
			Symbol:      normalizedAsset,
			Status:      "NOOP",
			ExecutedQty: quantity,
			QuoteQty:    quantity,
		}, nil
	}

	symbol, err := conversionSymbolForAsset(normalizedAsset)
	if err != nil {
		return nil, err
	}

	quantity := formatExchangeQuantity(fromAmount, normalizedAsset)
	if quantity == "" || quantity == "0" || quantity == "0.0" {
		return nil, fmt.Errorf("conversion quantity resolved to zero")
	}

	clients := make([]exchange.ExchangeClient, 0, 2)
	if c.primary != nil {
		clients = append(clients, c.primary)
	}
	if c.fallback != nil {
		if c.primary == nil || c.fallback.Name() != c.primary.Name() {
			clients = append(clients, c.fallback)
		}
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("no exchange clients configured")
	}

	attemptErrors := make([]string, 0, len(clients))
	for _, client := range clients {
		if until, disabled := c.disabledUntil(client.Name()); disabled {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: temporarily disabled until %s", client.Name(), until.UTC().Format(time.RFC3339)))
			continue
		}

		if err := c.preflightBalance(ctx, client, normalizedAsset, quantity); err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: %v", client.Name(), err))
			if isTemporaryExchangeFailure(err) {
				c.disableClient(client.Name(), 10*time.Minute)
			}
			continue
		}

		side := "SELL"
		if strings.EqualFold(client.Name(), "bybit") {
			side = "Sell"
		}

		order, err := client.PlaceMarketOrder(ctx, symbol, side, quantity)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: %v", client.Name(), err))
			if isTemporaryExchangeFailure(err) {
				c.disableClient(client.Name(), 10*time.Minute)
			}
			continue
		}

		orderID := strings.TrimSpace(order.OrderID)
		if orderID == "" {
			orderID = fmt.Sprintf("%s-%d", strings.ToLower(client.Name()), time.Now().UnixNano())
		}

		result := &workers.ConversionResult{
			Exchange:    client.Name(),
			OrderID:     orderID,
			Symbol:      firstNonEmptyValue(strings.TrimSpace(order.Symbol), symbol),
			Status:      firstNonEmptyValue(strings.TrimSpace(order.Status), "FILLED"),
			ExecutedQty: firstNonEmptyValue(strings.TrimSpace(order.ExecutedQty), quantity),
			QuoteQty:    strings.TrimSpace(order.QuoteQty),
		}

		c.logger.Info("sandbox exchange conversion executed", "exchange", result.Exchange, "symbol", result.Symbol, "order_id", result.OrderID, "status", result.Status, "executed_qty", result.ExecutedQty, "quote_qty", result.QuoteQty)
		return result, nil
	}

	return nil, fmt.Errorf("all exchange conversion attempts failed: %s", strings.Join(attemptErrors, "; "))
}

func (c *ExchangeSandboxConversionClient) disabledUntil(name string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(strings.TrimSpace(name))
	until, ok := c.disabled[key]
	if !ok {
		return time.Time{}, false
	}
	if time.Now().After(until) {
		delete(c.disabled, key)
		return time.Time{}, false
	}
	return until, true
}

func (c *ExchangeSandboxConversionClient) disableClient(name string, duration time.Duration) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || duration <= 0 {
		return
	}

	c.mu.Lock()
	c.disabled[key] = time.Now().Add(duration)
	c.mu.Unlock()

	c.logger.Warn("temporarily disabling exchange client after transient failure", "exchange", key, "duration", duration)
}

func (c *ExchangeSandboxConversionClient) preflightBalance(ctx context.Context, client exchange.ExchangeClient, asset string, quantity string) error {
	balance, err := client.GetBalance(ctx, asset)
	if err != nil {
		return fmt.Errorf("balance preflight failed: %w", err)
	}

	if decimalStringLessThan(strings.TrimSpace(balance), strings.TrimSpace(quantity)) {
		return fmt.Errorf("insufficient %s balance: have %s, need %s", asset, strings.TrimSpace(balance), strings.TrimSpace(quantity))
	}

	return nil
}

type GraphSandboxPayoutClient struct {
	app    *ApplicationService
	graph  *graphclient.Client
	logger *slog.Logger
}

func NewGraphSandboxPayoutClient(app *ApplicationService, graph *graphclient.Client, logger *slog.Logger) *GraphSandboxPayoutClient {
	return &GraphSandboxPayoutClient{app: app, graph: graph, logger: logger}
}

func (g *GraphSandboxPayoutClient) ConvertAndPay(ctx context.Context, bankAccountID string, payoutAmount int64) (string, error) {
	if g.graph == nil {
		return "", fmt.Errorf("graph client is not configured")
	}

	bankAccount, err := g.app.getBankAccountByID(ctx, bankAccountID)
	if err != nil {
		return "", fmt.Errorf("load bank account: %w", err)
	}
	if bankAccount.GraphDestID != nil && isSyntheticSandboxDestinationID(strings.TrimSpace(*bankAccount.GraphDestID)) {
		payoutID := fmt.Sprintf("%s%d", sandboxSyntheticPayoutPrefix, time.Now().UnixNano())
		g.logger.Info("local sandbox payout completed without provider payout call", "bank_account_id", bankAccountID, "payout_ref", payoutID, "amount", payoutAmount)
		return payoutID, nil
	}

	walletAccount, err := g.graph.GetWalletAccountByCurrency(ctx, "NGN")
	if err != nil {
		return "", fmt.Errorf("load NGN wallet account: %w", err)
	}

	destinationID, err := g.ensurePayoutDestination(ctx, bankAccount, walletAccount.ID)
	if err != nil {
		return "", err
	}

	if g.graph.IsSandbox() {
		if err := g.seedSandboxFunds(ctx, payoutAmount); err != nil {
			return "", err
		}
	}

	reference := fmt.Sprintf("sandbox-payout-%d", time.Now().UnixNano())
	payout, err := g.graph.CreatePayout(ctx, graphclient.CreatePayoutRequest{
		AccountID:     walletAccount.ID,
		SourceType:    "wallet_account",
		DestinationID: destinationID,
		Currency:      "NGN",
		Amount:        payoutAmount,
		Reference:     reference,
		Remarks:       fmt.Sprintf("ConvertChain payout %s", bankAccountID),
	})
	if err != nil {
		return "", fmt.Errorf("create payout: %w", err)
	}

	payoutID := strings.TrimSpace(payout.ID)
	if payoutID == "" {
		return "", fmt.Errorf("payout response did not include an id")
	}

	g.logger.Info("graph payout initiated", "payout_id", payoutID, "bank_account_id", bankAccountID, "amount", payoutAmount, "status", payout.Status)
	return payoutID, nil
}

func (g *GraphSandboxPayoutClient) GetPayoutStatus(ctx context.Context, payoutID string) (string, error) {
	if isSyntheticSandboxPayoutID(payoutID) {
		return "completed", nil
	}
	if g.graph == nil {
		return "", fmt.Errorf("graph client is not configured")
	}
	payout, err := g.graph.FetchPayout(ctx, payoutID)
	if err != nil {
		return "", fmt.Errorf("fetch payout status: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(payout.Status)), nil
}

func (g *GraphSandboxPayoutClient) ensurePayoutDestination(ctx context.Context, bankAccount *domain.BankAccount, walletAccountID string) (string, error) {
	if bankAccount.GraphDestID != nil && strings.TrimSpace(*bankAccount.GraphDestID) != "" {
		return strings.TrimSpace(*bankAccount.GraphDestID), nil
	}
	if g.graph != nil && g.graph.IsSandbox() {
		destinationID := makeSandboxDestinationID(bankAccount.BankCode, bankAccount.AccountNumber)
		_, err := g.app.db.Exec(ctx, `
            UPDATE bank_accounts
            SET graph_dest_id = $2
            WHERE id = $1::uuid
        `, bankAccount.ID.String(), destinationID)
		if err != nil {
			return "", fmt.Errorf("persist sandbox payout destination: %w", err)
		}
		return destinationID, nil
	}

	destination, err := g.graph.CreatePayoutDestination(ctx, graphclient.CreatePayoutDestinationRequest{
		AccountID:       walletAccountID,
		SourceType:      "wallet_account",
		Label:           fmt.Sprintf("convertchain-%s-%s", bankAccount.UserID.String()[:8], bankAccount.AccountNumber),
		Type:            "nip",
		AccountType:     "personal",
		BankCode:        bankAccount.BankCode,
		AccountNumber:   bankAccount.AccountNumber,
		BeneficiaryName: bankAccount.AccountName,
	})
	if err != nil {
		return "", fmt.Errorf("create payout destination: %w", err)
	}

	if destination.AccountName != "" || destination.BankName != "" {
		_, err = g.app.db.Exec(ctx, `
            UPDATE bank_accounts
            SET graph_dest_id = $2,
                account_name = COALESCE(NULLIF($3, ''), account_name),
                bank_name = COALESCE(NULLIF($4, ''), bank_name)
            WHERE id = $1::uuid
        `, bankAccount.ID.String(), destination.ID, destination.AccountName, destination.BankName)
	} else {
		_, err = g.app.db.Exec(ctx, `
            UPDATE bank_accounts
            SET graph_dest_id = $2
            WHERE id = $1::uuid
        `, bankAccount.ID.String(), destination.ID)
	}
	if err != nil {
		return "", fmt.Errorf("persist payout destination: %w", err)
	}

	return destination.ID, nil
}

func (g *GraphSandboxPayoutClient) seedSandboxFunds(ctx context.Context, payoutAmount int64) error {
	fundingAccount, err := g.graph.GetFundingBankAccountByCurrency(ctx, "NGN")
	if err != nil {
		return fmt.Errorf("load sandbox funding bank account: %w", err)
	}

	seedAmount := payoutAmount
	if seedAmount < graphSandboxMinMockDepositKobo {
		seedAmount = graphSandboxMinMockDepositKobo
	}

	chunksRemaining := seedAmount
	chunkCount := int64(0)
	for chunksRemaining > 0 {
		chunkAmount := chunksRemaining
		if chunkAmount > graphSandboxMaxMockDepositKobo {
			chunkAmount = graphSandboxMaxMockDepositKobo
		}

		_, err = g.graph.MockDeposit(ctx, graphclient.MockDepositRequest{
			AccountID:  fundingAccount.ID,
			SourceType: "bank_account",
			Currency:   "NGN",
			Amount:     chunkAmount,
			Reference:  fmt.Sprintf("sandbox-funding-%d-%d", time.Now().UnixNano(), chunkCount+1),
		})
		if err != nil {
			return fmt.Errorf("seed sandbox funds: %w", err)
		}

		chunksRemaining -= chunkAmount
		chunkCount++
	}

	g.logger.Info("seeded graph sandbox funds", "amount", seedAmount, "funding_account_id", fundingAccount.ID, "chunks", chunkCount)
	return nil
}

func sandboxRequiredConfirmations(currency string) int {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "ETH", "USDC":
		return 12
	case "BTC", "USDT", "BNB":
		return 2
	default:
		return 2
	}
}

func conversionSymbolForAsset(asset string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(asset)) {
	case "BTC", "ETH", "BNB":
		return strings.ToUpper(strings.TrimSpace(asset)) + "USDT", nil
	default:
		return "", fmt.Errorf("unsupported conversion asset %s", asset)
	}
}

func formatExchangeQuantity(fromAmount int64, asset string) string {
	quantity := minorUnitsToDecimalString(fromAmount, asset)
	maxDecimals := exchangeQuantityPrecision(asset)
	if maxDecimals <= 0 || !strings.Contains(quantity, ".") {
		return quantity
	}

	parts := strings.SplitN(quantity, ".", 2)
	fraction := parts[1]
	if len(fraction) > maxDecimals {
		fraction = fraction[:maxDecimals]
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return parts[0]
	}
	return parts[0] + "." + fraction
}

func exchangeQuantityPrecision(asset string) int {
	switch strings.ToUpper(strings.TrimSpace(asset)) {
	case "BTC":
		return 6
	case "ETH":
		return 5
	case "BNB":
		return 3
	case "USDT", "USDC":
		return 2
	default:
		return 6
	}
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func decimalStringLessThan(left string, right string) bool {
	leftValue, ok := new(big.Float).SetString(strings.TrimSpace(left))
	if !ok {
		return false
	}

	rightValue, ok := new(big.Float).SetString(strings.TrimSpace(right))
	if !ok {
		return false
	}

	return leftValue.Cmp(rightValue) < 0
}

func isTemporaryExchangeFailure(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such host") ||
		strings.Contains(message, "dial tcp") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "timeout") ||
		strings.Contains(message, "temporarily unavailable")
}
