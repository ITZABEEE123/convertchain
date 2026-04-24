package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/exchange"
)

const (
	graphSandboxMaxMockDepositKobo int64 = 1_000_000_000
	graphSandboxMinMockDepositKobo int64 = 10_000
	graphSandboxMaxSeedChunks      int64 = 50
)

func (s *ApplicationService) preflightTradeConfirmation(ctx context.Context, quote *domain.Quote, bankAccount *domain.BankAccount) error {
	if quote == nil {
		return &TradePreflightError{
			Message: "Trade quote could not be loaded for provider preflight.",
			Details: map[string]interface{}{"provider": "internal", "check": "quote_lookup"},
		}
	}

	if err := s.preflightGraphPayout(ctx, quote.NetAmount, bankAccount); err != nil {
		return err
	}

	asset := strings.ToUpper(strings.TrimSpace(quote.FromCurrency))
	switch asset {
	case "USDT", "USDC":
		return nil
	default:
		return s.preflightExchangeBalance(ctx, asset, quote.FromAmount)
	}
}

func (s *ApplicationService) preflightGraphPayout(ctx context.Context, payoutAmount int64, bankAccount *domain.BankAccount) error {
	if s.graph == nil {
		return &TradePreflightError{
			Message: "Trade cannot start because the Graph payout provider is not configured.",
			Details: map[string]interface{}{
				"provider": "graph",
				"check":    "configuration",
			},
		}
	}
	if bankAccount == nil || strings.TrimSpace(stringValuePointer(bankAccount.GraphDestID)) == "" {
		return &TradePreflightError{
			Message: "Trade cannot start because the selected bank account is not linked to a payout destination yet.",
			Details: map[string]interface{}{
				"provider": "graph",
				"check":    "bank_destination",
			},
		}
	}

	if _, err := s.graph.GetWalletAccountByCurrency(ctx, "NGN"); err != nil {
		return &TradePreflightError{
			Message: fmt.Sprintf("Trade cannot start because the Graph NGN wallet account is unavailable: %v", err),
			Details: map[string]interface{}{
				"provider": "graph",
				"check":    "wallet_account",
				"reason":   err.Error(),
			},
		}
	}

	if !s.graph.IsSandbox() {
		return nil
	}

	if _, err := s.graph.GetFundingBankAccountByCurrency(ctx, "NGN"); err != nil {
		return &TradePreflightError{
			Message: fmt.Sprintf("Trade cannot start because the Graph sandbox funding account is unavailable: %v", err),
			Details: map[string]interface{}{
				"provider": "graph",
				"check":    "sandbox_funding_account",
				"reason":   err.Error(),
			},
		}
	}

	requiredChunks := graphSandboxSeedChunkCount(payoutAmount)
	if requiredChunks > graphSandboxMaxSeedChunks {
		return &TradePreflightError{
			Message: fmt.Sprintf(
				"Trade cannot start because the Graph sandbox funding limit would require %d seed deposits, which exceeds the supported sandbox chunk limit of %d.",
				requiredChunks,
				graphSandboxMaxSeedChunks,
			),
			Details: map[string]interface{}{
				"provider":           "graph",
				"check":              "sandbox_capability",
				"payout_amount_kobo": payoutAmount,
				"required_chunks":    requiredChunks,
				"max_chunks":         graphSandboxMaxSeedChunks,
				"chunk_cap_kobo":     graphSandboxMaxMockDepositKobo,
			},
		}
	}

	return nil
}

func (s *ApplicationService) preflightExchangeBalance(ctx context.Context, asset string, fromAmount int64) error {
	quantity := formatExchangeQuantity(fromAmount, asset)
	if quantity == "" || quantity == "0" {
		return &TradePreflightError{
			Message: "Trade cannot start because the exchange quantity resolved to zero.",
			Details: map[string]interface{}{
				"provider": "exchange",
				"asset":    asset,
				"check":    "quantity_format",
			},
		}
	}

	clients := s.enabledExchangeClients()
	if len(clients) == 0 {
		return &TradePreflightError{
			Message: "Trade cannot start because no exchange client is configured for conversion.",
			Details: map[string]interface{}{
				"provider": "exchange",
				"asset":    asset,
				"check":    "configuration",
			},
		}
	}

	attemptErrors := make([]string, 0, len(clients))
	for _, client := range clients {
		balance, err := client.GetBalance(ctx, asset)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: balance check failed: %v", client.Name(), err))
			continue
		}
		if decimalStringLessThan(strings.TrimSpace(balance), strings.TrimSpace(quantity)) {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: insufficient %s balance: have %s, need %s", client.Name(), asset, strings.TrimSpace(balance), strings.TrimSpace(quantity)))
			continue
		}
		return nil
	}

	return &TradePreflightError{
		Message: "Trade cannot start because the configured exchange providers cannot support the requested size right now: " + strings.Join(attemptErrors, "; "),
		Details: map[string]interface{}{
			"provider": "exchange",
			"asset":    asset,
			"check":    "authenticated_balance",
			"reason":   strings.Join(attemptErrors, "; "),
		},
	}
}

func (s *ApplicationService) GetProviderReadiness(ctx context.Context) (*domain.ProviderReadinessReport, error) {
	report := &domain.ProviderReadinessReport{
		GeneratedAt: timeNowUTC(),
	}

	report.Graph = s.graphReadiness(ctx)
	report.Binance = s.exchangeReadiness(ctx, s.primaryExchange, true, []string{"BTC", "ETH", "BNB"})
	report.Bybit = s.exchangeReadiness(ctx, s.fallbackExchange, s.options.BybitFallbackEnabled, []string{"BTC", "ETH", "BNB"})
	report.SmileID = domain.ProviderReadinessCheck{
		Enabled: s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier1(),
		Healthy: s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier1(),
		Summary: readinessSummary(s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier1(), "SmileID credentials are configured.", "SmileID credentials are missing or incomplete."),
		Details: map[string]interface{}{
			"tier": "TIER_1",
		},
	}
	report.Sumsub = domain.ProviderReadinessCheck{
		Enabled: s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier2(),
		Healthy: s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier2(),
		Summary: readinessSummary(s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier2(), "Sumsub credentials are configured.", "Sumsub credentials are missing or incomplete."),
		Details: map[string]interface{}{
			"tier": "TIER_2_PLUS",
		},
	}

	report.OverallHealthy = report.Graph.Healthy &&
		report.Binance.Healthy &&
		report.SmileID.Healthy &&
		report.Sumsub.Healthy &&
		(!report.Bybit.Enabled || report.Bybit.Healthy)

	return report, nil
}

func (s *ApplicationService) graphReadiness(ctx context.Context) domain.ProviderReadinessCheck {
	check := domain.ProviderReadinessCheck{
		Enabled: s.graph != nil,
		Healthy: false,
		Details: map[string]interface{}{
			"webhook_secret_configured":       strings.TrimSpace(s.options.GraphWebhookSecret) != "",
			"public_webhook_base_url":         strings.TrimSpace(s.options.GraphWebhookPublicBaseURL),
			"recommended_webhook_destination": graphWebhookDestinationURL(s.options.GraphWebhookPublicBaseURL),
		},
	}
	if s.graph == nil {
		check.Summary = "Graph client is not configured."
		return check
	}

	check.Details["sandbox"] = s.graph.IsSandbox()
	check.Details["sandbox_chunk_cap_kobo"] = graphSandboxMaxMockDepositKobo
	check.Details["sandbox_max_seed_chunks"] = graphSandboxMaxSeedChunks

	walletAccount, walletErr := s.graph.GetWalletAccountByCurrency(ctx, "NGN")
	if walletErr == nil && walletAccount != nil {
		check.Details["wallet_account_id"] = walletAccount.ID
	}
	fundingHealthy := true
	if s.graph.IsSandbox() {
		fundingAccount, err := s.graph.GetFundingBankAccountByCurrency(ctx, "NGN")
		if err != nil {
			fundingHealthy = false
			check.Details["sandbox_funding_error"] = err.Error()
		} else {
			check.Details["sandbox_funding_account_id"] = fundingAccount.ID
		}
	}

	webhookHealthy := strings.TrimSpace(s.options.GraphWebhookSecret) != "" && strings.TrimSpace(s.options.GraphWebhookPublicBaseURL) != ""
	check.Healthy = walletErr == nil && fundingHealthy && webhookHealthy
	switch {
	case walletErr != nil:
		check.Summary = "Graph API authentication or wallet lookup failed: " + walletErr.Error()
		check.Details["wallet_error"] = walletErr.Error()
	case !fundingHealthy:
		check.Summary = "Graph sandbox funding account lookup failed."
	case !webhookHealthy:
		check.Summary = "Graph API is reachable, but webhook secret or public base URL is missing."
	default:
		check.Summary = "Graph API, wallet account, sandbox funding account, and webhook diagnostics are ready."
	}
	return check
}

func (s *ApplicationService) exchangeReadiness(
	ctx context.Context,
	client exchange.ExchangeClient,
	enabled bool,
	assets []string,
) domain.ProviderReadinessCheck {
	check := domain.ProviderReadinessCheck{
		Enabled: enabled,
		Healthy: false,
		Details: map[string]interface{}{},
	}
	if client == nil {
		check.Summary = "Exchange client is not configured."
		return check
	}

	check.Details["client_name"] = client.Name()
	balances := map[string]string{}
	for _, asset := range assets {
		balance, err := client.GetBalance(ctx, asset)
		if err != nil {
			check.Details["error"] = err.Error()
			check.Summary = fmt.Sprintf("%s balance lookup failed: %v", client.Name(), err)
			return check
		}
		balances[asset] = strings.TrimSpace(balance)
	}

	check.Healthy = true
	check.Details["balances"] = balances
	if enabled {
		check.Summary = fmt.Sprintf("%s authenticated balance checks succeeded.", client.Name())
	} else {
		check.Summary = fmt.Sprintf("%s connectivity and balances were checked while fallback remains disabled.", client.Name())
	}
	return check
}

func (s *ApplicationService) enabledExchangeClients() []exchange.ExchangeClient {
	clients := make([]exchange.ExchangeClient, 0, 2)
	if s.primaryExchange != nil {
		clients = append(clients, s.primaryExchange)
	}
	if s.options.BybitFallbackEnabled && s.fallbackExchange != nil {
		if s.primaryExchange == nil || s.fallbackExchange.Name() != s.primaryExchange.Name() {
			clients = append(clients, s.fallbackExchange)
		}
	}
	return clients
}

func graphSandboxSeedChunkCount(payoutAmount int64) int64 {
	seedAmount := payoutAmount
	if seedAmount < graphSandboxMinMockDepositKobo {
		seedAmount = graphSandboxMinMockDepositKobo
	}
	chunks := seedAmount / graphSandboxMaxMockDepositKobo
	if seedAmount%graphSandboxMaxMockDepositKobo != 0 {
		chunks++
	}
	if chunks == 0 {
		return 1
	}
	return chunks
}

func graphWebhookDestinationURL(base string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return ""
	}
	return trimmed + "/webhooks/graph"
}

func readinessSummary(healthy bool, okMessage string, badMessage string) string {
	if healthy {
		return okMessage
	}
	return badMessage
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}
