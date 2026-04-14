-- ============================================================
-- Rollback Migration 001: Drop Enum Types & Extensions
-- ============================================================

-- Drop enum types in reverse order
-- (Types can only be dropped if no table uses them)
DROP TYPE IF EXISTS kyc_doc_type;
DROP TYPE IF EXISTS currency_code;
DROP TYPE IF EXISTS channel_type;
DROP TYPE IF EXISTS trade_status;
DROP TYPE IF EXISTS kyc_tier;
DROP TYPE IF EXISTS user_status;

-- Drop extensions
DROP EXTENSION IF EXISTS "pg_stat_statements";
DROP EXTENSION IF EXISTS "pgcrypto";
DROP EXTENSION IF EXISTS "uuid-ossp";