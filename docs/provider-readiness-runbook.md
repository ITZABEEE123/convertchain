# Provider Readiness And Webhook Runbook

This runbook is the operator checklist for sandbox and pre-live trade testing.

## 1. Start the stack

1. Start Docker Desktop before bringing up local dependencies.
2. Bring up Postgres, Redis, and Vault.
3. Bootstrap Vault secrets.
4. Start the Go engine.
5. Start the Python bot.

The Go engine startup log should show:

- `graph_webhook_secret_configured=true`
- `bybit_fallback_enabled=false` unless you intentionally enabled it
- `auto_approve_kyc=false` in production

## 2. Configure Graph webhooks correctly

Graph webhook delivery failures in the dashboard do **not** automatically mean the payout failed. The payout truth in ConvertChain comes from the payout state that the engine polls and reconciles.

Use this exact setup:

1. Expose the Go engine publicly.
2. Point Graph webhook destination to `<public-go-engine>/webhooks/graph`.
3. Do **not** point Graph to the Python bot root `/`.
4. Set the same `GRAPH_WEBHOOK_SECRET` in:
   - Graph dashboard webhook configuration
   - Go engine environment or Vault
5. Set `GRAPH_WEBHOOK_PUBLIC_BASE_URL` to the public Go engine base URL.
6. Restart the Go engine.
7. Confirm engine startup logs show `graph_webhook_secret_configured=true`.
8. Send a Graph test event.
9. Verify the webhook reaches `POST /webhooks/graph` with HTTP 200.
10. If you see Python bot logs like `POST / HTTP/1.1 404 Not Found`, Graph is still pointed at the wrong service.

## 3. Run provider readiness before trade tests

Call:

- `GET /api/v1/admin/providers/readiness`
- Telegram admin command: `admin readiness`

Expected checks:

- Graph:
  - API auth works
  - NGN wallet account exists
  - sandbox funding account exists when sandbox is enabled
  - webhook secret is configured
  - public webhook base URL is configured
- Binance:
  - authenticated balance lookup succeeds
  - balances are high enough for the intended sandbox trade amount
- Bybit:
  - connectivity and authenticated balance lookup succeed when fallback is enabled
  - if fallback is disabled, readiness is still useful for DNS and connectivity diagnostics
- SmileID and Sumsub:
  - credentials are present for the configured KYC tiers

## 4. Binance sandbox balance checks

Before testing a trade size, confirm the test account holds enough of the asset being sold.

Examples:

- `sell 2 ETH` requires at least `2 ETH` in the authenticated exchange test balance.
- `sell 0.1 BTC` requires at least `0.1 BTC`.

If the engine preflight rejects the trade, fix the exchange test balance first instead of forcing the user into deposit and dispute flow.

## 5. Graph sandbox funding limits

Graph sandbox mock deposits are capped per deposit. ConvertChain now chunks sandbox funding automatically, but readiness can still fail when the requested payout would require too many seed chunks.

If a trade is rejected with a Graph sandbox capability error:

1. Reduce the trade amount.
2. Re-run readiness.
3. Retry only after the readiness report is healthy.

## 6. Bybit DNS and fallback checks

Bybit fallback stays disabled by default.

Use it only after confirming:

1. DNS resolution works from the machine running the Go engine.
2. Authenticated balance lookup succeeds.
3. `BYBIT_FALLBACK_ENABLED=true` is intentionally set.

If readiness shows Bybit unhealthy while fallback is disabled, treat it as diagnostic information rather than an active trade blocker.

## 7. Dispute recovery flow

Use the admin API or admin Telegram commands:

- `admin disputes`
- `admin dispute TRD-XXXXXXX`
- `admin resolve TRD-XXXXXXX retry`
- `admin resolve TRD-XXXXXXX close`
- `admin resolve TRD-XXXXXXX force_paid`

Resolution guidance:

- `retry`:
  - use when conversion or payout can be safely retried
  - if conversion never completed, trade returns to `DEPOSIT_CONFIRMED`
  - if conversion completed already, trade returns to `CONVERSION_COMPLETED`
- `close`:
  - use when the trade should end without payout
  - resulting `DISPUTE_CLOSED` state no longer blocks account deletion
- `force_paid`:
  - use only for administrative recovery when the payout is already known to be settled externally

## 8. Status and notification expectations

Users should receive automatic updates for:

- deposit detected
- deposit confirmed
- conversion started
- conversion completed
- payout processing
- payout completed
- payout failed
- dispute opened
- dispute resolved

When no active trade exists, `status` should show the most recent relevant trade outcome instead of a dead end.
