DROP INDEX IF EXISTS idx_notification_events_retry;

ALTER TABLE notification_events
  DROP COLUMN IF EXISTS dead_lettered_at,
  DROP COLUMN IF EXISTS dead_lettered,
  DROP COLUMN IF EXISTS claimed_at,
  DROP COLUMN IF EXISTS claim_token,
  DROP COLUMN IF EXISTS next_attempt_at,
  DROP COLUMN IF EXISTS delivery_attempts;
