-- ============================================================
-- Migration 005: Security, Notification Outbox & Account Deletion
-- ============================================================

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS txn_password_hash TEXT,
  ADD COLUMN IF NOT EXISTS txn_password_set_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS txn_password_failed_attempts INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS txn_password_locked_until TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS anonymized_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS deletion_subject_hash TEXT;

ALTER TABLE trades
  ADD COLUMN IF NOT EXISTS payout_authorized_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS payout_authorization_method VARCHAR(32);

CREATE TABLE IF NOT EXISTS notification_events (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id UUID NOT NULL REFERENCES users(id),
  channel_type channel_type NOT NULL,
  trade_id UUID REFERENCES trades(id),
  event_type VARCHAR(64) NOT NULL,
  payload JSONB NOT NULL,
  dedupe_key VARCHAR(128) NOT NULL UNIQUE,
  delivered BOOLEAN NOT NULL DEFAULT FALSE,
  delivered_at TIMESTAMPTZ,
  delivery_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notification_events_pending
  ON notification_events(channel_type, delivered, created_at);

CREATE INDEX IF NOT EXISTS idx_notification_events_user
  ON notification_events(user_id, created_at);

CREATE TABLE IF NOT EXISTS account_deletion_events (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id UUID REFERENCES users(id),
  deletion_subject_hash TEXT NOT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMPTZ,
  status VARCHAR(32) NOT NULL,
  reason TEXT,
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_account_deletion_events_subject
  ON account_deletion_events(deletion_subject_hash, requested_at DESC);
