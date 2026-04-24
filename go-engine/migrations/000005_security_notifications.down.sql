-- ============================================================
-- Rollback Migration 005: Security, Notification Outbox & Account Deletion
-- ============================================================

DROP TABLE IF EXISTS account_deletion_events;
DROP TABLE IF EXISTS notification_events;

ALTER TABLE trades
  DROP COLUMN IF EXISTS payout_authorized_at,
  DROP COLUMN IF EXISTS payout_authorization_method;

ALTER TABLE users
  DROP COLUMN IF EXISTS txn_password_hash,
  DROP COLUMN IF EXISTS txn_password_set_at,
  DROP COLUMN IF EXISTS txn_password_failed_attempts,
  DROP COLUMN IF EXISTS txn_password_locked_until,
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS anonymized_at,
  DROP COLUMN IF EXISTS deletion_subject_hash;
