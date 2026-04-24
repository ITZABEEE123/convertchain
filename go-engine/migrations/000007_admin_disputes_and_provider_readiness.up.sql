ALTER TYPE trade_status ADD VALUE IF NOT EXISTS 'DISPUTE_CLOSED';

CREATE TABLE IF NOT EXISTS trade_disputes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_ref TEXT NOT NULL UNIQUE,
    trade_id UUID NOT NULL REFERENCES trades(id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK (source IN ('system', 'user')),
    status TEXT NOT NULL CHECK (status IN ('OPEN', 'CLOSED')),
    reason TEXT NOT NULL,
    resolution_mode TEXT CHECK (
        resolution_mode IS NULL OR resolution_mode IN ('retry_processing', 'close_no_payout', 'force_complete')
    ),
    resolution_note TEXT,
    resolver TEXT,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_trade_disputes_one_open_per_trade
    ON trade_disputes (trade_id)
    WHERE status = 'OPEN';

CREATE INDEX IF NOT EXISTS idx_trade_disputes_status_created_at
    ON trade_disputes (status, created_at DESC);

DROP TRIGGER IF EXISTS set_trade_disputes_updated_at ON trade_disputes;
CREATE TRIGGER set_trade_disputes_updated_at
    BEFORE UPDATE ON trade_disputes
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

INSERT INTO trade_disputes (
    ticket_ref,
    trade_id,
    source,
    status,
    reason,
    created_at,
    updated_at
)
SELECT
    'DSP-' || UPPER(SUBSTRING(REPLACE(gen_random_uuid()::text, '-', '') FROM 1 FOR 8)),
    t.id,
    'system',
    'OPEN',
    COALESCE(NULLIF(t.dispute_reason, ''), 'Migrated from trade dispute state'),
    t.updated_at,
    t.updated_at
FROM trades t
WHERE t.status = 'DISPUTE'::trade_status
  AND NOT EXISTS (
      SELECT 1
      FROM trade_disputes d
      WHERE d.trade_id = t.id
        AND d.status = 'OPEN'
  );
