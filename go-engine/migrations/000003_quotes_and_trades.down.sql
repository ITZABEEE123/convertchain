-- ============================================================
-- Rollback Migration 003: Drop Quotes & Trades
-- ============================================================

DROP TABLE IF EXISTS trade_status_history;
DROP TABLE IF EXISTS trades;
DROP TABLE IF EXISTS quotes;