-- ============================================================
-- Migration 004: Ledger, Webhook Events & Triggers
-- ============================================================

-- ─────────────────────────────────────────────
-- LEDGER (Double-Entry, Immutable)
--
-- This is the financial accounting backbone.
-- Every money movement creates TWO entries:
--   Debit (D): money coming INTO an account
--   Credit (C): money going OUT OF an account
--
-- For every trade, you might see:
--   D  user:{id}       BTC  25000000   (user deposits 0.25 BTC)
--   C  platform:holding BTC  25000000   (platform holds 0.25 BTC)
--   D  platform:holding USDC 1678086    (exchange gives USDC)
--   C  user:{id}       NGN  4156789000 (user gets ₦41,567.89)
--   D  platform:fees   NGN  93015000   (platform keeps ₦930.15 fee)
--
-- The sum of all Debits must ALWAYS equal the sum of all Credits.
-- If it doesn't, something is fundamentally broken.
-- ─────────────────────────────────────────────

CREATE TABLE ledger_entries (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Which trade this entry belongs to (NULL for system entries)
  trade_id      UUID REFERENCES trades(id),

  -- What kind of money movement
  entry_type    VARCHAR(30) NOT NULL,   -- DEPOSIT, FEE, PAYOUT, REFUND, ADJUSTMENT

  -- The money
  currency      currency_code NOT NULL,
  amount        BIGINT NOT NULL,        -- ALWAYS positive — direction determines sign

  -- Debit or Credit
  -- D = money coming in (increases balance)
  -- C = money going out (decreases balance)
  direction     CHAR(1) NOT NULL CHECK (direction IN ('D', 'C')),

  -- Which account this affects
  -- Examples: "platform:fees", "platform:holding:BTC",
  --           "user:550e8400-e29b-41d4", "exchange:binance"
  account_ref   VARCHAR(64) NOT NULL,

  -- Running balance of this account after this entry
  balance_after BIGINT NOT NULL,

  -- Idempotency key: prevents duplicate entries
  -- If you accidentally process the same event twice,
  -- the UNIQUE constraint rejects the duplicate
  idempotency_key VARCHAR(128) UNIQUE NOT NULL,

  -- Flexible additional data (JSON)
  metadata      JSONB,

  -- Immutable: created_at only, no updated_at
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX idx_ledger_trade ON ledger_entries(trade_id);
CREATE INDEX idx_ledger_account ON ledger_entries(account_ref);

-- ─────────────────────────────────────────────
-- WEBHOOK EVENTS
-- Stores every inbound webhook from external services.
-- This is your "inbox" — webhooks arrive, get stored,
-- then processed asynchronously by background workers.
--
-- Sources: Binance (deposit notifications), Graph Finance
-- (payout confirmations), SmileID (KYC results), etc.
-- ─────────────────────────────────────────────

CREATE TABLE webhook_events (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Where this webhook came from
  source       VARCHAR(32) NOT NULL,  -- 'binance', 'graph', 'smileid', 'sumsub'

  -- What type of event
  event_type   VARCHAR(64) NOT NULL,  -- 'deposit.confirmed', 'payout.completed', etc.

  -- The full webhook payload (stored as structured JSON)
  payload      JSONB NOT NULL,

  -- The signature header (for validation)
  signature    TEXT,

  -- Processing status
  processed    BOOLEAN NOT NULL DEFAULT FALSE,
  processed_at TIMESTAMPTZ,
  error        TEXT,                   -- Error message if processing failed

  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_processed ON webhook_events(processed);
CREATE INDEX idx_webhook_source ON webhook_events(source);

-- ─────────────────────────────────────────────
-- AUTOMATIC TRIGGERS
-- These functions run automatically when data changes.
-- ─────────────────────────────────────────────

-- This function sets updated_at = NOW() whenever a row is modified
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply the trigger to tables that have updated_at columns
CREATE TRIGGER update_users_updated_at
  BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();

CREATE TRIGGER update_trades_updated_at
  BEFORE UPDATE ON trades
  FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();