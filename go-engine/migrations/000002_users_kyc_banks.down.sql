-- ============================================================
-- Rollback Migration 002: Drop Users, KYC & Bank Accounts
-- ============================================================

-- Drop in reverse order of creation (respect foreign key dependencies)
DROP TABLE IF EXISTS bank_accounts;
DROP TABLE IF EXISTS kyc_documents;
DROP TABLE IF EXISTS users;