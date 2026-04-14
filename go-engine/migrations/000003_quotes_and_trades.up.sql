-- ============================================================
-- Migration 003: Quotes, Trades & Trade Status History
-- These tables handle the core financial transaction flow.
-- ============================================================

-- ─────────────────────────────────────────────
-- QUOTES
-- A price offer shown to the user. Valid for 120 seconds.
-- If the user accepts, it becomes a trade.
-- If they don't, it expires.
-- ─────────────────────────────────────────────

CREATE TABLE quotes (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Which user requested this quote
  user_id           UUID NOT NULL REFERENCES users(id),

  -- What's being converted
  from_currency     currency_code NOT NULL,   -- What the user is selling (e.g., BTC)
  to_currency       currency_code NOT NULL,   -- What the user is receiving (e.g., NGN)

  -- 🚫 CRITICAL: All amounts are in MINOR UNITS (integers, never floats)
  -- BTC: satoshis (1 BTC = 100,000,000 satoshis)
  -- NGN: kobo (₦1 = 100 kobo)
  -- USD: cents ($1 = 100 cents)
  -- This prevents floating-point rounding errors that lose money
  from_amount       BIGINT NOT NULL,          -- Amount user is selling (in satoshis for BTC)
  to_amount         BIGINT NOT NULL,          -- Gross amount user will receive (in kobo for NGN)

  -- Exchange rates at the time of quote
  exchange_rate     NUMERIC(20, 8) NOT NULL,  -- Crypto/USDC rate (e.g., 67123.45000000)
  fiat_rate         NUMERIC(20, 8) NOT NULL,  -- USDC/NGN rate (e.g., 1625.00000000)

  -- Fee calculation
  fee_bps           INTEGER NOT NULL,          -- Fee in basis points (200 = 2.00%)
  fee_amount        BIGINT NOT NULL,           -- Fee in to_currency minor units
  net_amount        BIGINT NOT NULL,           -- to_amount minus fee (what user actually gets)

  -- Quote lifecycle
  valid_until       TIMESTAMPTZ NOT NULL,      -- When this quote expires (created_at + 120s)
  accepted_at       TIMESTAMPTZ,               -- When user accepted (NULL if not accepted)
  expired_at        TIMESTAMPTZ,               -- When it expired (NULL if accepted)

  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_quotes_user ON quotes(user_id);
CREATE INDEX idx_quotes_valid_until ON quotes(valid_until);

-- ─────────────────────────────────────────────
-- TRADES
-- The core transaction record. Created when a user accepts a quote.
-- Tracks the entire lifecycle from deposit to payout.
-- ─────────────────────────────────────────────

CREATE TABLE trades (
  id                    UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Human-readable reference (shown to user): TRD-A1B2C3D4
  trade_ref             VARCHAR(16) UNIQUE NOT NULL,

  -- Links
  user_id               UUID NOT NULL REFERENCES users(id),
  quote_id              UUID NOT NULL REFERENCES quotes(id),

  -- Current state in the trade lifecycle (13 possible values)
  status                trade_status NOT NULL DEFAULT 'TRADE_CREATED',

  -- Currency pair
  from_currency         currency_code NOT NULL,
  to_currency           currency_code NOT NULL,

  -- Amounts (all in minor units — BIGINT, never float)
  from_amount           BIGINT NOT NULL,          -- Crypto amount (satoshis)
  to_amount_expected    BIGINT NOT NULL,          -- Expected NGN payout (kobo)
  to_amount_actual      BIGINT,                   -- Actual NGN payout (may differ slightly)
  fee_amount            BIGINT NOT NULL,          -- Platform fee (kobo)

  -- Crypto deposit tracking
  deposit_address       VARCHAR(128),             -- Blockchain address to receive crypto
  deposit_txhash        VARCHAR(128),             -- Transaction hash on the blockchain
  deposit_confirmed_at  TIMESTAMPTZ,              -- When blockchain confirmed the deposit

  -- Exchange execution tracking
  exchange_order_id     VARCHAR(128),             -- Binance/Bybit order ID

  -- Graph Finance tracking
  graph_conversion_id   VARCHAR(128),             -- USDC→NGN conversion ID
  graph_payout_id       VARCHAR(128),             -- NIP bank payout ID

  -- Payout destination
  bank_account_id       UUID REFERENCES bank_accounts(id),

  -- Dispute handling
  dispute_reason        TEXT,

  -- Timing
  expires_at            TIMESTAMPTZ NOT NULL,     -- Deposit window (30 minutes from creation)
  completed_at          TIMESTAMPTZ,              -- When the full cycle finished

  created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trades_user ON trades(user_id);
CREATE INDEX idx_trades_status ON trades(status);
CREATE INDEX idx_trades_deposit_addr ON trades(deposit_address);
CREATE INDEX idx_trades_trade_ref ON trades(trade_ref);

-- ─────────────────────────────────────────────
-- TRADE STATUS HISTORY
-- An immutable log of every state change a trade goes through.
-- Used for audit trails and debugging.
-- Example: TRADE_CREATED → PENDING_DEPOSIT → DEPOSIT_RECEIVED → ...
-- ─────────────────────────────────────────────

CREATE TABLE trade_status_history (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Which trade this status change belongs to
  trade_id    UUID NOT NULL REFERENCES trades(id),

  -- The transition
  from_status trade_status,          -- NULL for the first entry (trade creation)
  to_status   trade_status NOT NULL, -- The new status

  -- Who/what caused the change
  actor       VARCHAR(50) NOT NULL,  -- 'system', 'user', 'admin', 'deposit_watcher'
  note        TEXT,                   -- Optional explanation

  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tsh_trade ON trade_status_history(trade_id);