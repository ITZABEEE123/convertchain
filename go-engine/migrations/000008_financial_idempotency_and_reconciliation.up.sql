-- ============================================================
-- Migration 008: Financial Idempotency + Reconciliation Runs
-- ============================================================

-- Stores deduplication keys for money-moving operations.
-- This table is append-only and enforces once-only side effects per scope/key.
CREATE TABLE IF NOT EXISTS financial_operation_keys (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  scope VARCHAR(64) NOT NULL,
  operation_key VARCHAR(255) NOT NULL,
  trade_id UUID REFERENCES trades(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(scope, operation_key)
);

CREATE INDEX IF NOT EXISTS idx_financial_operation_keys_trade
  ON financial_operation_keys (trade_id, created_at DESC);

-- Daily reconciliation run headers.
CREATE TABLE IF NOT EXISTS reconciliation_runs (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  run_date DATE NOT NULL,
  run_type VARCHAR(48) NOT NULL,
  status VARCHAR(16) NOT NULL CHECK (status IN ('PENDING', 'PASS', 'FAIL')),
  created_by VARCHAR(64) NOT NULL DEFAULT 'system',
  summary JSONB,
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(run_date, run_type)
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_runs_date
  ON reconciliation_runs (run_date DESC, run_type);

-- Reconciliation line-items/evidence rows.
CREATE TABLE IF NOT EXISTS reconciliation_items (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  run_id UUID NOT NULL REFERENCES reconciliation_runs(id) ON DELETE CASCADE,
  item_key VARCHAR(128) NOT NULL,
  status VARCHAR(16) NOT NULL CHECK (status IN ('PASS', 'FAIL')),
  currency currency_code,
  expected_amount BIGINT,
  actual_amount BIGINT,
  reference VARCHAR(128),
  details JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(run_id, item_key)
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_items_run
  ON reconciliation_items (run_id, status);
