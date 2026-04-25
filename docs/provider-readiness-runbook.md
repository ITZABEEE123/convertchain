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

## 3A. Sumsub-first Telegram launch checks

The first production launch uses Telegram plus Sumsub. SmileID may remain sandbox-only until its production account is funded, but it must not be the active production KYC provider.

Required Go engine settings:

- `KYC_PRIMARY_PROVIDER=sumsub`
- `SUMSUB_APP_TOKEN` and `SUMSUB_SECRET_KEY` from the same Sumsub mode
- `SUMSUB_USE_SANDBOX=false` in production
- `SUMSUB_WEBHOOK_SECRET` matching the Sumsub webhook manager secret
- `SUMSUB_WEBHOOK_PUBLIC_BASE_URL=<public-go-engine-base-url>`
- `SUMSUB_TIER1_LEVEL_NAME`, `SUMSUB_TIER2_LEVEL_NAME`, `SUMSUB_TIER3_LEVEL_NAME`, and `SUMSUB_TIER4_LEVEL_NAME` matching dashboard level names

Point Sumsub webhook delivery to:

- `<public-go-engine-base-url>/webhooks/kyc/sumsub`

The Sumsub webhook must include `X-Payload-Digest` and `X-Payload-Digest-Alg`. ConvertChain verifies the raw request body with the configured webhook secret, persists the event, deduplicates by Sumsub correlation/event identifiers and payload hash, and only applies final approval/rejection when the review result contains a terminal answer such as `GREEN` or `RED`.

Required Python bot settings for the Telegram-only launch:

- `ENABLED_CHANNELS=telegram`
- `TELEGRAM_BOT_TOKEN=<BotFather token>`
- `TELEGRAM_WEBHOOK_SECRET=<random secret>` and configure the same secret when setting the Telegram webhook
- `TELEGRAM_TRUSTED_DELIVERY=false` unless an ingress layer authenticates Telegram delivery before the bot

Keep WhatsApp values empty or placeholder while `ENABLED_CHANNELS` does not include `whatsapp`. The bot will not initialize WhatsApp or poll WhatsApp notifications in Telegram-only mode.

Expected user flow:

1. User starts onboarding in Telegram.
2. Engine creates or updates the user and starts Sumsub Tier 1 verification.
3. Bot sends the Sumsub WebSDK verification link returned by the engine.
4. User completes Sumsub verification and returns to Telegram.
5. Sumsub webhook approves/rejects KYC.
6. User types `status`; the bot sees the updated KYC state and proceeds to transaction-password setup.

If the Sumsub link expires, `status` can return a fresh WebSDK link while the applicant is still pending.

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

## 9. Safe rollout and rollback toggles

Two enforcement controls are now staged behind reversible environment flags.

1. Legacy trade create endpoint (`POST /api/v1/trades`)

- Flag: `TRADE_CREATE_ENDPOINT_MODE`
- Allowed values: `allow`, `warn`, `enforce`
- Default: `warn`

Behavior:

- `allow`: legacy endpoint remains available with no deprecation response headers.
- `warn`: legacy endpoint is still available, but responds with deprecation headers (`Deprecation`, `Sunset`, `Warning`) to drive client migration telemetry.
- `enforce`: legacy endpoint is blocked with `410 Gone` and error code `ENDPOINT_DEPRECATED`.

Rollback:

- Immediate rollback is setting `TRADE_CREATE_ENDPOINT_MODE=warn` (or `allow`) and restarting the Go engine.

2. Graph webhook event identifier requirement

- Flag: `GRAPH_WEBHOOK_EVENT_ID_MODE`
- Allowed values: `off`, `warn`, `enforce`
- Default: `warn`

Behavior:

- `off`: no requirement for event-id header.
- `warn`: missing event-id header is accepted, but a warning header is returned and service warning logs are emitted.
- `enforce`: missing event-id header is rejected with `400` and error code `WEBHOOK_MISSING_EVENT_ID`.

Accepted event-id headers:

- `X-Graph-Event-Id`
- `Graph-Event-Id`
- `X-Webhook-Event-Id`

Rollback:

- Immediate rollback is setting `GRAPH_WEBHOOK_EVENT_ID_MODE=warn` (or `off`) and restarting the Go engine.

Recommended rollout order:

1. Deploy with defaults (`warn` modes).
2. Monitor warning volume and client/provider conformance for at least one full business cycle.
3. Move one flag at a time to `enforce`.
4. Keep rollback runbook and on-call ownership explicit for each cutover window.
