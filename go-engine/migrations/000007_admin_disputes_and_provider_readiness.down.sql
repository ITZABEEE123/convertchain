DROP TRIGGER IF EXISTS set_trade_disputes_updated_at ON trade_disputes;
DROP INDEX IF EXISTS idx_trade_disputes_status_created_at;
DROP INDEX IF EXISTS idx_trade_disputes_one_open_per_trade;
DROP TABLE IF EXISTS trade_disputes;
