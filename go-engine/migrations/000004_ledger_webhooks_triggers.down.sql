-- ============================================================
-- Rollback Migration 004: Drop Ledger, Webhooks & Triggers
-- ============================================================

-- Drop triggers first
DROP TRIGGER IF EXISTS update_trades_updated_at ON trades;
DROP TRIGGER IF EXISTS update_users_updated_at ON users;

-- Drop the trigger function
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop tables
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS ledger_entries;